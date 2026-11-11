package local

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

const HomePath = "/home/dev"

type Local struct {
	Dir     string
	KeyPath string
	Public  string
	Editor  string

	WindowsEditor  string
	WindowsConfig  string
	WindowsKeyFile string
	WindowsKeyPath string

	CursorEditor        string
	WindowsCursorEditor string
}

func New(editor string) (*Local, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".poddy")
	keyPath := filepath.Join(dir, "id_ed25519")
	public, err := ensureKey(keyPath)
	if err != nil {
		return nil, err
	}

	l := &Local{Dir: dir, KeyPath: keyPath, Public: public, Editor: editor}
	if os.Getenv("PODDY_NO_WINDOWS") == "" {
		l.detectWindows()
	}
	l.detectCursor()
	return l, nil
}

// HasVSCode reports whether a local VS Code command is available.
func (l *Local) HasVSCode() bool {
	return l != nil && (l.Editor != "" || l.WindowsEditor != "")
}

// HasCursor reports whether a local Cursor command is available.
func (l *Local) HasCursor() bool {
	return l != nil && (l.CursorEditor != "" || l.WindowsCursorEditor != "")
}

func (l *Local) Windows() bool {
	return l.WindowsConfig != "" && l.WindowsKeyPath != ""
}

func (l *Local) PreferredKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return l.KeyPath
	}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		path := filepath.Join(home, ".ssh", name)
		if fileExists(path) {
			return path
		}
	}
	return l.KeyPath
}

func (l *Local) AuthorizedKeys() []string {
	keys := []string{l.Public}
	home, err := os.UserHomeDir()
	if err != nil {
		return keys
	}
	for _, name := range []string{"id_ed25519.pub", "id_rsa.pub"} {
		data, err := os.ReadFile(filepath.Join(home, ".ssh", name))
		if err != nil {
			continue
		}
		if key := strings.TrimSpace(string(data)); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func (l *Local) WriteSSHConfig(name string, port int) error {
	path, err := sshConfigPath()
	if err != nil {
		return err
	}
	return writeConfig(path, name, port, l.KeyPath, "/dev/null")
}

func (l *Local) WriteWindowsSSHConfig(name string, port int) error {
	if !l.Windows() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.WindowsConfig), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.WindowsKeyFile), 0o700); err != nil {
		return err
	}

	key, err := os.ReadFile(l.KeyPath)
	if err != nil {
		return err
	}
	_ = os.Remove(l.WindowsKeyFile)
	if err := os.WriteFile(l.WindowsKeyFile, key, 0o600); err != nil {
		return err
	}
	_ = restrictWindowsKey(l.WindowsKeyPath)

	return writeConfig(l.WindowsConfig, name, port, l.WindowsKeyPath, "NUL")
}

func (l *Local) RemoveSSHConfig(name string) error {
	path, err := sshConfigPath()
	if err != nil {
		return err
	}
	if err := removeFromConfig(path, name); err != nil {
		return err
	}
	if l.Windows() {
		_ = removeFromConfig(l.WindowsConfig, name)
	}
	return nil
}

func (l *Local) Launch(name string) error {
	return l.launch(l.Editor, l.WindowsEditor, "VS Code", name)
}

// LaunchCursor opens the workspace in a local Cursor window.
func (l *Local) LaunchCursor(name string) error {
	return l.launch(l.CursorEditor, l.WindowsCursorEditor, "Cursor", name)
}

func (l *Local) launch(editor, windowsEditor, label, name string) error {
	args := []string{"--new-window", "--remote", "ssh-remote+" + Alias(name), HomePath}

	if windowsEditor != "" {
		cmd := exec.Command("cmd.exe", append([]string{"/c", windowsEditor}, args...)...)
		cmd.Dir = "/mnt/c"
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("launch Windows %s: %w", label, err)
		}
		go cmd.Wait()
		return nil
	}
	if editor == "" {
		return fmt.Errorf("%s is not installed", label)
	}

	cmd := exec.Command(editor, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch %s: %w", label, err)
	}
	go cmd.Wait()
	return nil
}

func Alias(name string) string {
	return "poddy-" + name
}

func (l *Local) detectWindows() {
	if os.Getenv("PODDY_WINDOWS_EDITOR") == "off" {
		return
	}
	winHome := detectWindowsHome()
	if winHome == "" {
		return
	}
	wslHome := winToWSL(winHome)
	if wslHome == "" {
		return
	}

	sshDir := filepath.Join(wslHome, ".ssh")
	l.WindowsConfig = filepath.Join(sshDir, "config")
	l.WindowsKeyFile = filepath.Join(sshDir, "poddy_id_ed25519")
	l.WindowsKeyPath = winHome + `\.ssh\poddy_id_ed25519`
	l.WindowsEditor = detectWindowsCode()
}

func detectWindowsHome() string {
	if value := os.Getenv("PODDY_WINDOWS_HOME"); value != "" {
		return value
	}
	if _, err := os.Stat("/mnt/c"); err != nil {
		return ""
	}
	out, err := runCmd("echo", "%USERPROFILE%")
	if err != nil {
		return ""
	}
	home := strings.TrimSpace(strings.TrimRight(string(out), "\r\n"))
	if len(home) < 3 || home[1] != ':' || !strings.Contains(home, `\`) {
		return ""
	}
	return home
}

func (l *Local) detectCursor() {
	if value := os.Getenv("PODDY_CURSOR"); value != "" && value != "off" {
		l.CursorEditor = value
	} else if path, err := exec.LookPath("cursor"); err == nil {
		l.CursorEditor = path
	}

	if l.WindowsConfig == "" {
		return
	}
	if value := os.Getenv("PODDY_WINDOWS_CURSOR"); value != "" && value != "off" {
		l.WindowsCursorEditor = value
		return
	}
	l.WindowsCursorEditor = detectWindowsCursor()
}

func detectWindowsCursor() string {
	out, err := runCmd("where", "cursor")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		lower := strings.ToLower(line)
		if strings.HasSuffix(lower, `\cursor.cmd`) || strings.HasSuffix(lower, `\cursor.exe`) {
			return line
		}
	}
	return ""
}

func detectWindowsCode() string {
	if value := os.Getenv("PODDY_WINDOWS_EDITOR"); value != "" && value != "off" {
		return value
	}
	out, err := runCmd("where", "code")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if strings.HasSuffix(strings.ToLower(line), `\code.cmd`) {
			return line
		}
	}
	return ""
}

func runCmd(args ...string) ([]byte, error) {
	cmd := exec.Command("cmd.exe", append([]string{"/c"}, args...)...)
	cmd.Dir = "/mnt/c"
	return cmd.Output()
}

func winToWSL(path string) string {
	if out, err := exec.Command("wslpath", "-u", path).Output(); err == nil {
		if value := strings.TrimSpace(string(out)); value != "" {
			return value
		}
	}
	if len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/') {
		drive := strings.ToLower(string(path[0]))
		rest := strings.ReplaceAll(path[3:], "\\", "/")
		return "/mnt/" + drive + "/" + rest
	}
	return ""
}

func restrictWindowsKey(path string) error {
	cmd := exec.Command("cmd.exe", "/c", "icacls", path, "/inheritance:r", "/grant:r", "%USERNAME%:F")
	cmd.Dir = "/mnt/c"
	return cmd.Run()
}

func ensureKey(path string) (string, error) {
	pubPath := path + ".pub"
	if fileExists(path) && fileExists(pubPath) {
		data, err := os.ReadFile(pubPath)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, "poddy")
	if err != nil {
		return "", err
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		return "", err
	}
	public := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(pubPath, []byte(public+"\n"), 0o644); err != nil {
		return "", err
	}
	return public, nil
}

func sshConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

func writeConfig(path, name string, port int, keyPath, knownHosts string) error {
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := strings.TrimRight(removeBlock(string(body), name), "\n")
	if content != "" {
		content += "\n\n"
	}
	content += block(name, port, keyPath, knownHosts)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func removeFromConfig(path, name string) error {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte(removeBlock(string(body), name)), 0o600)
}

func block(name string, port int, keyPath, knownHosts string) string {
	return fmt.Sprintf(`# >>> poddy:%s
Host %s
    HostName 127.0.0.1
    Port %d
    User dev
    IdentityFile "%s"
    IdentitiesOnly yes
    StrictHostKeyChecking no
    UserKnownHostsFile %s
    LogLevel ERROR
# <<< poddy:%s
`, name, Alias(name), port, keyPath, knownHosts, name)
}

func removeBlock(content, name string) string {
	start := "# >>> poddy:" + name
	end := "# <<< poddy:" + name

	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case start:
			skip = true
		case end:
			skip = false
		default:
			if !skip {
				out = append(out, line)
			}
		}
	}
	return strings.TrimLeft(strings.Join(out, "\n"), "\n")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
