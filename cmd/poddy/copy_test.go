package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCopySpec(t *testing.T) {
	tests := []struct {
		input string
		spec  copySpec
	}{
		{"wb1:/path_source", copySpec{Workspace: "wb1", Path: "/path_source"}},
		{"wk2:/path_target/", copySpec{Workspace: "wk2", Path: "/path_target", Dir: true}},
		{"wk2:relative/file.txt", copySpec{Workspace: "wk2", Path: "relative/file.txt"}},
		{"wk2:~/data", copySpec{Workspace: "wk2", Path: "~/data"}},
		{"wk2:foo/../bar", copySpec{Workspace: "wk2", Path: "bar"}},
	}
	for _, test := range tests {
		got, err := parseCopySpec(test.input)
		if err != nil {
			t.Fatalf("%s: %v", test.input, err)
		}
		if got != test.spec {
			t.Fatalf("%s: got %+v want %+v", test.input, got, test.spec)
		}
	}
	for _, input := range []string{"", "ws", ":/foo", "ws:", "ws:/", "ws:.", "ws:..", "ws:~", "ws:foo/.."} {
		if _, err := parseCopySpec(input); err == nil {
			t.Fatalf("%q accepted", input)
		}
	}
}

func TestPosixQuote(t *testing.T) {
	if got := posixQuote("a b'c"); got != `'a b'"'"'c'` {
		t.Fatalf("posixQuote() = %q", got)
	}
}

func TestCopyTargetInsideSource(t *testing.T) {
	tests := []struct {
		source copySpec
		target copySpec
		want   bool
	}{
		{copySpec{Workspace: "ws", Path: "/foo"}, copySpec{Workspace: "ws", Path: "/foo/bar"}, true},
		{copySpec{Workspace: "ws", Path: "~/foo"}, copySpec{Workspace: "ws", Path: "~/foo/bar"}, true},
		{copySpec{Workspace: "ws", Path: "~/foo"}, copySpec{Workspace: "ws", Path: "/home/dev/foo/bar"}, true},
		{copySpec{Workspace: "ws", Path: "foo"}, copySpec{Workspace: "ws", Path: "/home/dev/foo/bar"}, true},
		{copySpec{Workspace: "ws", Path: "/foo"}, copySpec{Workspace: "ws", Path: "/foo"}, false},
		{copySpec{Workspace: "ws", Path: "/foo"}, copySpec{Workspace: "ws", Path: "/foobar"}, false},
		{copySpec{Workspace: "ws", Path: "/foo"}, copySpec{Workspace: "other", Path: "/foo/bar"}, false},
	}
	for _, test := range tests {
		if got := copyTargetInsideSource(test.source, test.target); got != test.want {
			t.Fatalf("%+v -> %+v: got %v want %v", test.source, test.target, got, test.want)
		}
	}
}

func TestCopyCommands(t *testing.T) {
	source := copySourceCommand("/home/dev/my data")
	for _, want := range []string{"p='/home/dev/my data'", `tar -C "$d" -cf - -- "$b"`} {
		if !strings.Contains(source, want) {
			t.Fatalf("source command missing %q: %s", want, source)
		}
	}
	target := copyTargetCommand("/home/dev/target", true, "my data")
	for _, want := range []string{"p='/home/dev/target/'", "src='my data'", `mktemp -d "$dir/.poddy-cp.XXXXXX"`, `mv -- "$tmp/$src"`} {
		if !strings.Contains(target, want) {
			t.Fatalf("target command missing %q: %s", want, target)
		}
	}
}

func TestRunCopyPipeline(t *testing.T) {
	dir := t.TempDir()
	capture := filepath.Join(dir, "capture")
	sshPath := filepath.Join(dir, "ssh")
	script := "#!/bin/sh\ncase \"$1\" in\nsource) printf 'hello' ;;\ntarget) cat > \"$TARGET_CAPTURE\" ;;\nesac\n"
	if err := os.WriteFile(sshPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TARGET_CAPTURE", capture)
	var stdout, stderr bytes.Buffer
	code := runCopyPipeline(context.Background(), sshPath, []string{"source"}, []string{"target"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("captured %q", data)
	}
}

func TestRunCopyPipelineReturnsSourceExitCode(t *testing.T) {
	dir := t.TempDir()
	sshPath := filepath.Join(dir, "ssh")
	script := "#!/bin/sh\nif [ \"$1\" = source ]; then exit 7; fi\ncat >/dev/null\n"
	if err := os.WriteFile(sshPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runCopyPipeline(context.Background(), sshPath, []string{"source"}, []string{"target"}, &stdout, &stderr)
	if code != 7 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
}

func TestCopyKeyPathsUsesSeparateTemporaryCopies(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	keys, cleanup, err := copyKeyPaths(keyPath, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if keys["a"] == keys["b"] {
		t.Fatalf("copies share a path: %q", keys["a"])
	}
	for _, workspace := range []string{"a", "b"} {
		if _, err := os.Stat(keys[workspace]); err != nil {
			t.Fatalf("%s: %v", workspace, err)
		}
	}
	temporary := filepath.Dir(keys["a"])
	cleanup()
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary directory still exists: %v", err)
	}
}

func TestRunCLICP(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/me/ssh-certificate":
			_, _ = w.Write([]byte(`{"certificate":"test-cert"}`))
		case "/api/config":
			_, _ = w.Write([]byte(`{"ssh":true,"sshHost":"ssh.example.com","sshPort":"2222"}`))
		case "/api/workspaces":
			_, _ = w.Write([]byte(`[{"name":"wb1"},{"name":"wk2"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("PODDY_PORTAL_URL", server.URL)
	t.Setenv("PODDY_TOKEN", "test-token")

	dir := t.TempDir()
	capture := filepath.Join(dir, "capture")
	t.Setenv("TARGET_CAPTURE", capture)
	sshPath := filepath.Join(dir, "ssh")
	script := "#!/bin/sh\ncase \"$*\" in\n*mktemp*) cat > \"$TARGET_CAPTURE\" ;;\n*basename*) printf hello ;;\nesac\n"
	if err := os.WriteFile(sshPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	var stdout, stderr bytes.Buffer
	if code := runCLICP([]string{"wb1:/source", "wk2:/target"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("captured %q", data)
	}
}
