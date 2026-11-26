package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/backup"
	"github.com/mhsanaei/3x-ui/v3/internal/config"
	"github.com/mhsanaei/3x-ui/v3/internal/crypto/nodetoken"
	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/nodejoin"
	"github.com/mhsanaei/3x-ui/v3/internal/subedge"
	"github.com/mhsanaei/3x-ui/v3/internal/web/runtime"
)

// NodeRestoreNoBackup asks for a fresh node instead of a backup.
const NodeRestoreNoBackup = "none"

const nodeRestoreTimeout = 20 * time.Minute

// nodeInstaller is the script run on the new server; tests swap in a stand-in.
var nodeInstaller = nodejoin.InstallScript

// NodeRestoreRequest names the fresh server a lost node moves to.
type NodeRestoreRequest struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	Password   string `json:"password"`
	PrivateKey string `json:"privateKey"`
	// Backup is a file from the node's backups; empty takes the newest, "none" none.
	Backup string `json:"backup"`
}

// NodeRestoreStatus is the progress of the current or last node restore.
type NodeRestoreStatus struct {
	NodeId     int      `json:"nodeId" example:"3"`
	Running    bool     `json:"running" example:"false"`
	Error      string   `json:"error" example:""`
	Log        []string `json:"log" example:"[\"Connecting to 203.0.113.20\"]"`
	StartedAt  int64    `json:"startedAt" example:"1764090000"`
	FinishedAt int64    `json:"finishedAt" example:"1764090180"`
}

var restoreState struct {
	mu     sync.Mutex
	status NodeRestoreStatus
}

// ErrRestoreRunning means another node restore has not finished.
var ErrRestoreRunning = errors.New("a node restore is already running")

func restoreLog(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	logger.Info("node restore: " + line)
	restoreState.mu.Lock()
	restoreState.status.Log = append(restoreState.status.Log, line)
	restoreState.mu.Unlock()
}

func (s *BackupService) RestoreStatus() NodeRestoreStatus {
	restoreState.mu.Lock()
	defer restoreState.mu.Unlock()
	st := restoreState.status
	st.Log = append([]string{}, st.Log...)
	return st
}

// RestoreNode starts moving a node onto a fresh server in the background;
// onDone runs after the node's record points at the new server.
func (s *BackupService) RestoreNode(nodeID int, req NodeRestoreRequest, onDone func()) error {
	req.Host = strings.Trim(strings.TrimSpace(req.Host), "[]")
	if req.Host == "" {
		return errors.New("give the new server's address")
	}
	if req.Password == "" && strings.TrimSpace(req.PrivateKey) == "" {
		return errors.New("give the new server's root password or SSH key")
	}
	if req.Backup != "" && req.Backup != NodeRestoreNoBackup && backup.SafeName(req.Backup) != req.Backup {
		return fmt.Errorf("invalid backup name %q", req.Backup)
	}
	node, err := s.nodeService.GetById(nodeID)
	if err != nil {
		return err
	}
	restoreState.mu.Lock()
	if restoreState.status.Running {
		restoreState.mu.Unlock()
		return ErrRestoreRunning
	}
	restoreState.status = NodeRestoreStatus{NodeId: nodeID, Running: true, StartedAt: time.Now().Unix()}
	restoreState.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), nodeRestoreTimeout)
		defer cancel()
		err := s.restoreNode(ctx, node, req)
		restoreState.mu.Lock()
		restoreState.status.Running = false
		restoreState.status.FinishedAt = time.Now().Unix()
		if err != nil {
			restoreState.status.Error = err.Error()
		}
		restoreState.mu.Unlock()
		if err != nil {
			logger.Warningf("restore of node %s failed: %v", node.Name, err)
			return
		}
		if onDone != nil {
			onDone()
		}
	}()
	return nil
}

func (s *BackupService) restoreNode(ctx context.Context, node *model.Node, req NodeRestoreRequest) error {
	archive, err := s.pickNodeArchive(ctx, node.Id, req.Backup)
	if err != nil {
		return err
	}
	if archive != "" {
		defer os.Remove(archive)
	}

	restoreLog("Connecting to %s over SSH", req.Host)
	client, fingerprint, err := backup.Dial(ctx, backup.SSHLogin{Host: req.Host, Port: req.Port, User: req.User, Password: req.Password, PrivateKey: req.PrivateKey})
	if err != nil {
		return fmt.Errorf("SSH to %s: %w", req.Host, err)
	}
	defer client.Close()
	restoreLog("Connected; the server's host key is %s", fingerprint)

	ident, err := installRestoredNode(client, node, archive, req.User)
	if err != nil {
		return err
	}
	address, err := restoredNodeAddress(ctx, node.Address, req.Host)
	if err != nil {
		return err
	}
	if err := s.nodeService.PointAtRestoredServer(node.Id, address, ident); err != nil {
		return err
	}
	restoreLog("The node now points at %s:%d; the master is pushing its inbounds and clients", address, ident.Port)
	return nil
}

// pickNodeArchive downloads the chosen backup into a temporary file; "" means
// none exists or none was asked for, and the node is set up fresh.
func (s *BackupService) pickNodeArchive(ctx context.Context, nodeID int, name string) (string, error) {
	if name == NodeRestoreNoBackup {
		restoreLog("Installing a fresh node without a backup")
		return "", nil
	}
	st, err := s.openStore(ctx)
	if err != nil {
		if name == "" {
			restoreLog("Backup server not reachable (%v); installing a fresh node", err)
			return "", nil
		}
		return "", err
	}
	defer st.Close()
	if name == "" {
		files, err := st.List(NodeBackupFolder(nodeID))
		if err != nil {
			return "", err
		}
		if len(files) == 0 {
			restoreLog("This node has no backups yet; installing a fresh node")
			return "", nil
		}
		name = files[0].Name
	}
	restoreLog("Downloading backup %s", name)
	rc, err := st.Open(NodeBackupFolder(nodeID), name)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp("", "node-restore-*.tar.gz")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := io.Copy(tmp, io.LimitReader(rc, backup.MaxNodeArchiveBytes)); err != nil {
		return "", err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	info, err := backup.InspectNodeArchive(tmp)
	if err != nil {
		return "", err
	}
	if !info.HasDB {
		restoreLog("The backup has no node database; the node keeps its files but is set up fresh")
	}
	keep = true
	return tmp.Name(), nil
}

// RestoredNodeIdentity is what install-node.sh --identity reports.
type RestoredNodeIdentity struct {
	Port       int
	BasePath   string
	ApiToken   string
	CertSha256 string
}

const identityPrefix = "node-identity: "

// ParseNodeIdentity finds the identity line in the installer's output.
func ParseNodeIdentity(out string) (RestoredNodeIdentity, error) {
	var id RestoredNodeIdentity
	for line := range strings.SplitSeq(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), identityPrefix)
		if !ok {
			continue
		}
		for field := range strings.FieldsSeq(rest) {
			k, v, _ := strings.Cut(field, "=")
			switch k {
			case "port":
				id.Port, _ = strconv.Atoi(v)
			case "basePath":
				id.BasePath = v
			case "apiToken":
				id.ApiToken = v
			case "certSha256":
				id.CertSha256 = v
			}
		}
	}
	if id.Port < 1 || id.Port > 65535 || id.BasePath == "" || id.ApiToken == "" || id.CertSha256 == "" {
		return id, errors.New("the installer did not report the node's port, path, token and certificate")
	}
	return id, nil
}

var unameArch = map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64", "armv7l": "armv7"}

func installRestoredNode(client *ssh.Client, node *model.Node, archive, user string) (RestoredNodeIdentity, error) {
	machine, err := backup.Run(client, "uname -m")
	if err != nil {
		return RestoredNodeIdentity{}, fmt.Errorf("uname: %w", err)
	}
	if arch := unameArch[strings.TrimSpace(machine)]; arch != nodejoin.Arch() {
		return RestoredNodeIdentity{}, fmt.Errorf("the server is %s but this master's build is %s", strings.TrimSpace(machine), nodejoin.Arch())
	}
	dirOut, err := backup.Run(client, "mktemp -d /tmp/my-panel-vpn-restore.XXXXXX")
	if err != nil {
		return RestoredNodeIdentity{}, fmt.Errorf("mktemp: %w: %s", err, dirOut)
	}
	dir := strings.TrimSpace(dirOut)
	if !strings.HasPrefix(dir, "/tmp/my-panel-vpn-restore.") {
		return RestoredNodeIdentity{}, fmt.Errorf("mktemp printed %q", dir)
	}
	defer func() { _, _ = backup.Run(client, "rm -rf "+backup.ShellQuote(dir)) }()

	sc, err := sftp.NewClient(client)
	if err != nil {
		return RestoredNodeIdentity{}, fmt.Errorf("SFTP on the new server: %w", err)
	}
	defer sc.Close()
	restoreLog("Uploading the panel build")
	if err := uploadFunc(sc, path.Join(dir, "install-node.sh"), func(w io.Writer) error {
		_, err := w.Write(nodeInstaller())
		return err
	}); err != nil {
		return RestoredNodeIdentity{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return RestoredNodeIdentity{}, err
	}
	if err := uploadFunc(sc, path.Join(dir, "bundle.tar.gz"), func(w io.Writer) error {
		return nodejoin.WriteBundle(w, exe, config.GetBinFolderPath())
	}); err != nil {
		return RestoredNodeIdentity{}, err
	}
	args := []string{"--bundle", path.Join(dir, "bundle.tar.gz"), "--identity", "--replace", "--port", strconv.Itoa(node.Port)}
	if archive != "" {
		if err := uploadFunc(sc, path.Join(dir, "node.tar.gz"), func(w io.Writer) error {
			f, err := os.Open(archive)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(w, f)
			return err
		}); err != nil {
			return RestoredNodeIdentity{}, err
		}
		args = append(args, "--restore", path.Join(dir, "node.tar.gz"))
	}
	if node.Country != "" {
		args = append(args, "--country", node.Country)
	}
	cmd := "bash " + backup.ShellQuote(path.Join(dir, "install-node.sh"))
	for _, a := range args {
		cmd += " " + backup.ShellQuote(a)
	}
	if u := strings.TrimSpace(user); u != "" && u != "root" {
		cmd = "sudo -n " + cmd
	}
	restoreLog("Installing the node (this takes a minute)")
	out, err := backup.Run(client, cmd)
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" && !strings.HasPrefix(line, identityPrefix) {
			restoreLog("  %s", line)
		}
	}
	if err != nil {
		return RestoredNodeIdentity{}, fmt.Errorf("the installer failed: %w", err)
	}
	return ParseNodeIdentity(out)
}

func uploadFunc(sc *sftp.Client, name string, write func(io.Writer) error) error {
	f, err := sc.Create(name)
	if err != nil {
		return fmt.Errorf("upload %s: %w", path.Base(name), err)
	}
	if err := write(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("upload %s: %w", path.Base(name), err)
	}
	return f.Close()
}

// restoredNodeAddress keeps a node's domain, pointing it at the new server
// through Cloudflare, and replaces a bare IP with the new server's address.
func restoredNodeAddress(ctx context.Context, oldAddress, newHost string) (string, error) {
	if net.ParseIP(strings.Trim(oldAddress, "[]")) != nil {
		return newHost, nil
	}
	ip := newHost
	if net.ParseIP(ip) == nil {
		addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", newHost)
		if err != nil || len(addrs) == 0 {
			restoreLog("Could not resolve %s; point %s at the new server yourself", newHost, oldAddress)
			return oldAddress, nil
		}
		ip = addrs[0].String()
	}
	token, err := dnsToken()
	if err != nil || token == "" {
		restoreLog("No Cloudflare token is saved; point %s at %s in your DNS", oldAddress, ip)
		return oldAddress, nil
	}
	cf := &subedge.Cloudflare{Token: token, BaseURL: cloudflareAPI}
	changed, err := cf.PointName(ctx, oldAddress, ip)
	if err != nil {
		restoreLog("Cloudflare did not update %s (%v); point it at %s yourself", oldAddress, err, ip)
		return oldAddress, nil
	}
	restoreLog("Cloudflare: %s now points at %s (%d record(s) changed)", oldAddress, ip, changed)
	return oldAddress, nil
}

// PointAtRestoredServer moves a node's record to the server it was restored
// on and marks it dirty so the next sync pushes its whole configuration.
func (s *NodeService) PointAtRestoredServer(id int, address string, ident RestoredNodeIdentity) error {
	if _, err := runtime.DecodeCertPin(ident.CertSha256); err != nil {
		return err
	}
	token, err := nodetoken.Encrypt(id, ident.ApiToken)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"scheme":             "https",
		"address":            address,
		"port":               ident.Port,
		"base_path":          ident.BasePath,
		"api_token":          token,
		"tls_verify_mode":    "pin",
		"pinned_cert_sha256": ident.CertSha256,
		"status":             "unknown",
		"enable":             true,
		"last_error":         "",
	}
	if ip := net.ParseIP(address); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
		updates["allow_private_address"] = true
	}
	if err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(model.Node{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		return s.MarkNodeDirtyTx(tx, id)
	}); err != nil {
		return err
	}
	if mgr := runtime.GetManager(); mgr != nil {
		mgr.InvalidateNode(id)
	}
	return nil
}
