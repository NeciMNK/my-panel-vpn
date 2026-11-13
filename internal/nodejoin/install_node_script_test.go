package nodejoin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func installNodeFunction(t *testing.T, name string) string {
	t.Helper()
	source, err := os.ReadFile("install-node.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut("\n"+string(source), "\n"+name+"() {\n")
	if !ok {
		t.Fatalf("install-node.sh: missing function %s", name)
	}
	body, _, ok = strings.Cut(body, "\n}\n")
	if !ok {
		t.Fatalf("install-node.sh: missing closing brace for %s", name)
	}
	return name + "() {\n" + body + "\n}\n"
}

// The installer printed the node panel's emergency password to the console,
// leaving it in scrollback and cloud-init/CI logs; it must go to a 0600 file.
func TestInstallNodeKeepsEmergencyLoginOffTheConsole(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	const pass = "0123456789abcdef01234567"
	file := filepath.Join(t.TempDir(), "x-ui", "node-login.txt")
	driver := "yellow= plain=\n" + installNodeFunction(t, "save_emergency_login") +
		`save_emergency_login "$LOGIN_FILE" https://203.0.113.7:2053/abc/ admin-1a2b3c "$PASS"` + "\n"
	cmd := exec.Command(bash, "-c", driver)
	cmd.Env = append(os.Environ(), "LOGIN_FILE="+file, "PASS="+pass)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("save_emergency_login failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), pass) {
		t.Fatalf("password printed to the console:\n%s", out)
	}
	if !strings.Contains(string(out), file) {
		t.Fatalf("console output does not name %s:\n%s", file, out)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("login file mode = %o, want 600", mode)
	}
	saved, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := "url=https://203.0.113.7:2053/abc/\nusername=admin-1a2b3c\npassword=" + pass + "\n"
	if string(saved) != want {
		t.Fatalf("login file = %q, want %q", saved, want)
	}

	source, err := os.ReadFile("install-node.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(source), "\n") {
		if strings.Contains(line, "echo") && strings.Contains(line, "ADMIN_PASS") {
			t.Fatalf("install-node.sh still echoes the password: %s", strings.TrimSpace(line))
		}
	}
}
