package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
)

// NodeJoinTokenTTL bounds how long an install command stays usable.
const NodeJoinTokenTTL = time.Hour

// ErrNodeJoinTokenInvalid hides whether a token is unknown, used or expired.
var ErrNodeJoinTokenInvalid = errors.New("join token is invalid, used or expired")

// NodeJoinTokenView is what the master UI needs to print the install command.
type NodeJoinTokenView struct {
	Token     string `json:"token" example:"m3Yx0QmRZ2bq0l5w2b3h1C9rYx0QmRZ2bq0l5w2b3h0"`
	ExpiresAt int64  `json:"expiresAt" example:"1700003600"`
	Arch      string `json:"arch" example:"amd64"`
}

// NodeJoinRequest is what install-node.sh posts once the node panel is up.
type NodeJoinRequest struct {
	Join       string `json:"join"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	BasePath   string `json:"basePath"`
	ApiToken   string `json:"apiToken"`
	CertSha256 string `json:"certSha256"`
}

func hashJoinToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateJoinToken mints a one-time join token; only its hash is stored.
func (s *NodeService) CreateJoinToken(arch string) (*NodeJoinTokenView, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	row := &model.NodeJoinToken{
		TokenHash: hashJoinToken(token),
		ExpiresAt: time.Now().Add(NodeJoinTokenTTL).Unix(),
	}
	if err := database.GetDB().Create(row).Error; err != nil {
		return nil, err
	}
	return &NodeJoinTokenView{Token: token, ExpiresAt: row.ExpiresAt, Arch: arch}, nil
}

func usableJoinToken(tx *gorm.DB, token string) (*model.NodeJoinToken, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrNodeJoinTokenInvalid
	}
	row := &model.NodeJoinToken{}
	err := tx.Where("token_hash = ?", hashJoinToken(token)).First(row).Error
	if err != nil || row.UsedAt != 0 || row.ExpiresAt < time.Now().Unix() {
		return nil, ErrNodeJoinTokenInvalid
	}
	return row, nil
}

// CheckJoinToken reports whether a token may still download the node bundle.
func (s *NodeService) CheckJoinToken(token string) error {
	_, err := usableJoinToken(database.GetDB(), token)
	return err
}

// uniqueNodeName appends -2, -3, ... so re-joining a host never collides.
func uniqueNodeName(tx *gorm.DB, name string) (string, error) {
	candidate := name
	for i := 2; i < 1000; i++ {
		var count int64
		if err := tx.Model(model.Node{}).Where("name = ?", candidate).Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", name, i)
	}
	return "", common.NewError("could not pick a free node name")
}

// JoinNode consumes a join token and registers the calling server as a node
// pinned to the certificate it reported; remoteIP is used when no address is given.
func (s *NodeService) JoinNode(req *NodeJoinRequest, remoteIP string) (*NodeView, error) {
	if req == nil {
		return nil, ErrNodeJoinTokenInvalid
	}
	address := strings.TrimSpace(req.Address)
	if address == "" {
		address = remoteIP
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "node-" + strings.NewReplacer(".", "-", ":", "-").Replace(address)
	}
	node := &model.Node{
		Scheme:           "https",
		Address:          address,
		Port:             req.Port,
		BasePath:         req.BasePath,
		ApiToken:         strings.TrimSpace(req.ApiToken),
		Enable:           true,
		TlsVerifyMode:    "pin",
		PinnedCertSha256: strings.TrimSpace(req.CertSha256),
		InboundSyncMode:  "all",
	}
	if ip := net.ParseIP(address); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
		node.AllowPrivateAddress = true
	}
	if node.ApiToken == "" {
		return nil, common.NewError("node api token is required")
	}
	node.Name = name
	if err := s.normalize(node); err != nil {
		return nil, err
	}

	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		row, err := usableJoinToken(tx, req.Join)
		if err != nil {
			return err
		}
		// Burn the token first so two racing registrations cannot both succeed.
		res := tx.Model(model.NodeJoinToken{}).Where("id = ? AND used_at = 0", row.Id).
			Update("used_at", time.Now().Unix())
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrNodeJoinTokenInvalid
		}
		if node.Name, err = uniqueNodeName(tx, name); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.Create(node); err != nil {
		return nil, err
	}
	database.GetDB().Model(model.NodeJoinToken{}).Where("token_hash = ?", hashJoinToken(req.Join)).
		Update("node_id", node.Id)
	return toNodeView(node), nil
}
