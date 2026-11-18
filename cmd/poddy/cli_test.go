package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeForward(t *testing.T) {
	tests := map[string]string{
		"8080:80":           "8080:127.0.0.1:80",
		"8080:localhost:80": "8080:localhost:80",
		"127.0.0.1:8080:80": "127.0.0.1:8080:80",
	}
	for input, want := range tests {
		got, err := normalizeForward(input)
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if got != want {
			t.Fatalf("%s: got %q want %q", input, got, want)
		}
	}
	for _, input := range []string{"", "8080", "a:b:c:d"} {
		if _, err := normalizeForward(input); err == nil {
			t.Fatalf("%q accepted", input)
		}
	}
}

func TestGenerateCLIConfig(t *testing.T) {
	portal := cliPortalConfig{
		SSHEnabled: true,
		SSHHost:    "ssh.example.com",
		SSHPort:    "2222",
	}
	text := generateCLIConfig(portal, "/home/alice/.ssh/poddy_ed25519")
	for _, want := range []string{
		"Host poddy-ssh-*",
		"ProxyCommand ssh -W %h:%p poddy-portal-",
		"HostName ssh.example.com",
		"Port 2222",
		"IdentityFile /home/alice/.ssh/poddy_ed25519",
		"StrictHostKeyChecking accept-new",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "StrictHostKeyChecking no") || strings.Contains(text, "UserKnownHostsFile /dev/null") {
		t.Fatalf("config disables host key verification:\n%s", text)
	}
}

func TestCLISlug(t *testing.T) {
	if got := cliSlug("EU West/1"); got != "eu-west-1" {
		t.Fatalf("slug = %q", got)
	}
}

func TestInstallManagedConfigIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("Host existing\n  User dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := installManagedConfig(path, "Host poddy-ssh-*\n  User dev\n"); err != nil {
		t.Fatal(err)
	}
	if err := installManagedConfig(path, "Host poddy-ssh-*\n  User dev\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Count(got, "# >>> poddy:cli") != 1 {
		t.Fatalf("managed block duplicated:\n%s", got)
	}
	if !strings.Contains(got, "Host existing") {
		t.Fatalf("existing config removed:\n%s", got)
	}
}

func TestCLISSHArgs(t *testing.T) {
	args := cliSSHArgs("ssh.example.com", "2222", "/home/alice/.ssh/key", "demo", false, "")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"ProxyCommand=ssh -W %h:%p -p 2222 -i /home/alice/.ssh/key",
		"dev@poddy-ssh-demo",
		"StrictHostKeyChecking=accept-new",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args do not contain %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "StrictHostKeyChecking=no") || strings.Contains(joined, "UserKnownHostsFile=") {
		t.Fatalf("args disable host key verification: %v", args)
	}
}

func TestPKCEChallenge(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := pkceChallenge(verifier); got != challenge {
		t.Fatalf("challenge = %q want %q", got, challenge)
	}
}
