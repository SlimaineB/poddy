package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureKeyAndConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PODDY_NO_WINDOWS", "1")

	l, err := New("code")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(l.Public, "ssh-ed25519 ") {
		t.Fatalf("public key = %q", l.Public)
	}

	if err := l.WriteSSHConfig("app", 2222); err != nil {
		t.Fatal(err)
	}
	if err := l.WriteSSHConfig("app", 2223); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(home, ".ssh", "config")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Count(content, "# >>> poddy:app") != 1 {
		t.Fatalf("config = %s", content)
	}
	if !strings.Contains(content, "Port 2223") || strings.Contains(content, "Port 2222") {
		t.Fatalf("config = %s", content)
	}

	if err := l.RemoveSSHConfig("app"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "poddy:app") {
		t.Fatalf("config = %s", data)
	}
}

func TestWriteWindowsSSHConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PODDY_NO_WINDOWS", "1")

	l, err := New("code")
	if err != nil {
		t.Fatal(err)
	}
	l.WindowsConfig = filepath.Join(home, "windows", "config")
	l.WindowsKeyFile = filepath.Join(home, "windows", "poddy_id_ed25519")
	l.WindowsKeyPath = `C:\Users\test\.ssh\poddy_id_ed25519`

	if err := l.WriteWindowsSSHConfig("app", 2200); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.WindowsKeyFile); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(l.WindowsConfig)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "IdentityFile \"C:\\Users\\test\\.ssh\\poddy_id_ed25519\"") {
		t.Fatalf("config = %s", content)
	}
	if !strings.Contains(content, "UserKnownHostsFile NUL") {
		t.Fatalf("config = %s", content)
	}
}
