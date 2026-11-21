package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

type copySpec struct {
	Workspace string
	Path      string
	Dir       bool
}

type copySSHTarget struct {
	KeyPath string
	Host    string
	Port    string
	Mesh    *cliMeshConfig
}

func runCLICP(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("cp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.String("key", "", "SSH private key path")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: poddy cp [options] <workspace>:<path> <workspace>:<path>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return 2
	}
	source, err := parseCopySpec(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "poddy: source: %v\n", err)
		return 2
	}
	target, err := parseCopySpec(fs.Arg(1))
	if err != nil {
		fmt.Fprintf(stderr, "poddy: target: %v\n", err)
		return 2
	}
	if copyTargetInsideSource(source, target) {
		fmt.Fprintln(stderr, "poddy: refusing to copy a directory into itself")
		return 2
	}

	settings, err := loadCLISettings()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	if settings.PortalURL == "" || settings.Token == "" {
		fmt.Fprintln(stderr, "poddy: not logged in; run 'poddy login --portal URL'")
		return 1
	}

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		fmt.Fprintln(stderr, "poddy: ssh client not found in PATH")
		return 1
	}
	keyPath := strings.TrimSpace(*key)
	if keyPath == "" {
		keyPath = settings.KeyPath
	}
	targets, cleanup, err := resolveCopyTargets(settings, keyPath, []string{source.Workspace, target.Workspace}, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	defer cleanup()

	sourceTarget := targets[source.Workspace]
	targetTarget := targets[target.Workspace]
	sourceBase := path.Base(source.Path)

	sourceArgs := copySSHArgs(sourceTarget, source.Workspace, false, "")
	sourceArgs = append(sourceArgs, copySourceCommand(source.Path))
	targetArgs := copySSHArgs(targetTarget, target.Workspace, false, "")
	targetArgs = append(targetArgs, copyTargetCommand(target.Path, target.Dir, sourceBase))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runCopyPipeline(ctx, sshPath, sourceArgs, targetArgs, stdout, stderr)
}

func parseCopySpec(value string) (copySpec, error) {
	workspace, rawPath, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return copySpec{}, errors.New("expected <workspace>:<path>")
	}
	workspace = strings.TrimSpace(workspace)
	rawPath = strings.TrimSpace(rawPath)
	if workspace == "" {
		return copySpec{}, errors.New("workspace is required")
	}
	if rawPath == "" {
		return copySpec{}, errors.New("path is required")
	}
	cleaned := path.Clean(rawPath)
	base := path.Base(cleaned)
	if base == "." || base == ".." || base == "/" || base == "~" || base == "" {
		return copySpec{}, fmt.Errorf("refusing to copy path %q", rawPath)
	}
	return copySpec{
		Workspace: workspace,
		Path:      cleaned,
		Dir:       strings.HasSuffix(rawPath, "/"),
	}, nil
}

func copyTargetInsideSource(source, target copySpec) bool {
	if source.Workspace != target.Workspace {
		return false
	}
	sourcePath := copyComparisonPath(source.Path)
	targetPath := copyComparisonPath(target.Path)
	if sourcePath == targetPath {
		return false
	}
	prefix := sourcePath
	if prefix != "/" {
		prefix = strings.TrimRight(prefix, "/") + "/"
	}
	return strings.HasPrefix(targetPath, prefix)
}

// copyComparisonPath resolves the paths used by the remote shell so a tilde or
// relative path is compared against the workspace home consistently.
func copyComparisonPath(value string) string {
	cleaned := path.Clean(value)
	switch {
	case cleaned == "~":
		return cliWorkspaceHome
	case strings.HasPrefix(cleaned, "~/"):
		return path.Join(cliWorkspaceHome, cleaned[2:])
	case path.IsAbs(cleaned):
		return cleaned
	default:
		return path.Join(cliWorkspaceHome, cleaned)
	}
}

func resolveCopyTargets(settings cliSettings, keyFlag string, workspaces []string, stderr io.Writer) (map[string]copySSHTarget, func(), error) {
	workspaces = uniqueCopyWorkspaces(workspaces)
	keys, cleanup, err := copyKeyPaths(keyFlag, workspaces)
	if err != nil {
		return nil, func() {}, err
	}
	targets := make(map[string]copySSHTarget, len(workspaces))
	for _, workspace := range workspaces {
		keyPath := keys[workspace]
		if err := prepareCLIAccess(settings, workspace, keyPath, stderr); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		meshCfg, err := fetchCLIMesh(settings, workspace)
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		host, port := "", ""
		if meshCfg == nil {
			host, port, err = cliSSHEndpoint(settings, workspace)
			if err != nil {
				cleanup()
				return nil, func() {}, err
			}
		}
		targets[workspace] = copySSHTarget{KeyPath: keyPath, Host: host, Port: port, Mesh: meshCfg}
	}
	return targets, cleanup, nil
}

func copySSHArgs(target copySSHTarget, workspace string, noCommand bool, forward string) []string {
	if target.Mesh != nil {
		return cliMeshSSHArgs(*target.Mesh, target.KeyPath, workspace, noCommand, forward)
	}
	return cliSSHArgs(target.Host, target.Port, target.KeyPath, workspace, noCommand, forward)
}

func copyKeyPaths(keyFlag string, workspaces []string) (map[string]string, func(), error) {
	keys := make(map[string]string, len(workspaces))
	keyFlag = expandHome(strings.TrimSpace(keyFlag))
	if keyFlag == "" {
		for _, workspace := range workspaces {
			keyPath, err := ensureCLIKey(defaultCLIWorkspaceKeyPath(workspace))
			if err != nil {
				return nil, func() {}, err
			}
			keys[workspace] = keyPath
		}
		return keys, func() {}, nil
	}
	data, err := os.ReadFile(keyFlag) // #nosec G304 -- User-selected key path.
	if err != nil {
		return nil, func() {}, fmt.Errorf("read SSH key: %w", err)
	}
	temporary, err := os.MkdirTemp("", "poddy-cp-*")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(temporary) }
	for i, workspace := range workspaces {
		keyPath := filepath.Join(temporary, fmt.Sprintf("%d_%s", i, cliSlug(workspace)))
		if err := writeSecureFile(keyPath, data, 0o600); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		keys[workspace] = keyPath
	}
	return keys, cleanup, nil
}

func uniqueCopyWorkspaces(workspaces []string) []string {
	seen := make(map[string]bool, len(workspaces))
	unique := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		if seen[workspace] {
			continue
		}
		seen[workspace] = true
		unique = append(unique, workspace)
	}
	return unique
}

func copySourceCommand(pathValue string) string {
	return "set -e; " + remotePathExpansion(pathValue) + "; " +
		`d=$(dirname -- "$p"); b=$(basename -- "$p"); tar -C "$d" -cf - -- "$b"`
}

func copyTargetCommand(pathValue string, dir bool, sourceBase string) string {
	if dir {
		pathValue = strings.TrimRight(pathValue, "/") + "/"
	}
	return "set -e; " + remotePathExpansion(pathValue) + "; " +
		"src=" + posixQuote(sourceBase) + "; " +
		`case "$p" in
  */) dst="${p%/}"; [ -n "$dst" ] || dst=/; mkdir -p -- "$dst"; dir=$dst; name= ;;
  *) if [ -d "$p" ]; then dir=$p; name=; else dir=$(dirname -- "$p"); name=$(basename -- "$p"); mkdir -p -- "$dir"; fi ;;
esac; ` +
		`tmp=$(mktemp -d "$dir/.poddy-cp.XXXXXX"); trap 'rm -rf -- "$tmp"' EXIT; ` +
		`tar -C "$tmp" -xf -; ` +
		`if [ -n "$name" ]; then rm -rf -- "$dir/$name"; mv -- "$tmp/$src" "$dir/$name"; else rm -rf -- "$dir/$src"; mv -- "$tmp/$src" "$dir/"; fi`
}

func remotePathExpansion(value string) string {
	return "p=" + posixQuote(value) + `; case "$p" in "~/"*) p="$HOME/${p#\~/}";; esac`
}

func posixQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func runCopyPipeline(ctx context.Context, sshPath string, sourceArgs, targetArgs []string, stdout, stderr io.Writer) int {
	lockedStderr := &lockedWriter{writer: stderr}
	target := exec.CommandContext(ctx, sshPath, targetArgs...)
	source := exec.CommandContext(ctx, sshPath, sourceArgs...)
	source.Stderr = lockedStderr
	target.Stderr = lockedStderr
	target.Stdout = stdout
	pipe, err := source.StdoutPipe()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	target.Stdin = pipe
	if err := target.Start(); err != nil {
		_ = pipe.Close()
		fmt.Fprintf(stderr, "poddy: start target: %v\n", err)
		return 1
	}
	if err := source.Start(); err != nil {
		_ = pipe.Close()
		_ = target.Process.Kill()
		_ = target.Wait()
		fmt.Fprintf(lockedStderr, "poddy: start source: %v\n", err)
		return 1
	}
	sourceErr := source.Wait()
	targetErr := target.Wait()
	if ctx.Err() != nil {
		return 130
	}
	if code, ok := commandExitCode(sourceErr); ok {
		return code
	}
	if sourceErr != nil {
		fmt.Fprintf(lockedStderr, "poddy: source: %v\n", sourceErr)
		return 1
	}
	if code, ok := commandExitCode(targetErr); ok {
		return code
	}
	if targetErr != nil {
		fmt.Fprintf(lockedStderr, "poddy: target: %v\n", targetErr)
		return 1
	}
	return 0
}

func commandExitCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	code := exitErr.ExitCode()
	if code < 0 {
		code = 1
	}
	return code, true
}

type lockedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
