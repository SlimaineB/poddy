package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type cliSettings struct {
	PortalURL string `json:"portalUrl"`
	Token     string `json:"token"`
	KeyPath   string `json:"keyPath,omitempty"`
}

type cliMe struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Method   string `json:"method"`
	IsAdmin  bool   `json:"isAdmin"`
}

type cliWorkspace struct {
	Name   string `json:"name"`
	Phase  string `json:"phase"`
	Owner  string `json:"owner"`
	Region string `json:"region"`
}

type cliPortalConfig struct {
	SSHEnabled     bool   `json:"ssh"`
	SSHHost        string `json:"sshHost"`
	SSHPort        string `json:"sshPort"`
	SSHKeyPath     string `json:"sshKeyPath"`
	WindowsKeyPath string `json:"windowsSSHKeyPath"`
	Regions        []struct {
		Name    string `json:"name"`
		SSHHost string `json:"sshHost"`
		SSHPort string `json:"sshPort"`
	} `json:"regions"`
}

func runCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: poddy <login|logout|status|list|config|ssh|port-forward> [options]")
		return 2
	}
	switch args[0] {
	case "login":
		return runCLILogin(args[1:], stdout, stderr)
	case "logout":
		return runCLILogout(args[1:], stdout, stderr)
	case "status":
		return runCLIStatus(args[1:], stdout, stderr)
	case "list", "ls":
		return runCLIList(args[1:], stdout, stderr)
	case "config":
		return runCLIConfig(args[1:], stdout, stderr)
	case "ssh":
		return runCLISSH(args[1:], stdout, stderr)
	case "port-forward", "forward":
		return runCLIPortForward(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "poddy: unknown command %q\n", args[0])
		return 2
	}
}

func runCLILogin(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	portal := fs.String("portal", "", "portal URL")
	token := fs.String("token", "", "API token (otherwise a browser login is started)")
	key := fs.String("key", "", "default SSH private key path")
	noOpen := fs.Bool("no-open", false, "do not open a browser")
	timeout := fs.Duration("timeout", 5*time.Minute, "how long to wait for browser login")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: poddy login [options]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	settings, err := loadCLISettings()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	if strings.TrimSpace(*portal) != "" {
		settings.PortalURL = strings.TrimSpace(*portal)
	}
	if strings.TrimSpace(*token) != "" {
		settings.Token = strings.TrimSpace(*token)
	}
	if strings.TrimSpace(*key) != "" {
		settings.KeyPath = expandHome(strings.TrimSpace(*key))
	}
	if settings.PortalURL == "" {
		fmt.Fprintln(stderr, "poddy: --portal is required (or set PODDY_PORTAL_URL)")
		return 2
	}
	settings.PortalURL, err = normalizePortal(settings.PortalURL)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 2
	}

	ctx, stop := context.WithTimeout(context.Background(), *timeout)
	defer stop()
	if settings.Token == "" {
		settings.Token, err = browserLogin(ctx, settings.PortalURL, !*noOpen, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "poddy: %v\n", err)
			return 1
		}
	}
	me, err := cliRequest[cliMe](ctx, settings, http.MethodGet, "/api/me", nil)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: login failed: %v\n", err)
		return 1
	}
	if err := saveCLISettings(settings); err != nil {
		fmt.Fprintf(stderr, "poddy: save config: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Logged in to %s as %s\n", settings.PortalURL, me.Username)
	return 0
}

func runCLILogout(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path, err := cliConfigPath()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "Logged out.")
	return 0
}

func runCLIStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	me, err := cliRequest[cliMe](ctx, settings, http.MethodGet, "/api/me", nil)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Portal: %s\nUser:   %s\nMethod: %s\n", settings.PortalURL, me.Username, me.Method)
	return 0
}

func runCLIList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	settings, err := loadCLISettings()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	workspaces, err := cliRequest[[]cliWorkspace](ctx, settings, http.MethodGet, "/api/workspaces", nil)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].Name < workspaces[j].Name })
	for _, workspace := range workspaces {
		region := ""
		if workspace.Region != "" {
			region = " " + workspace.Region
		}
		fmt.Fprintf(stdout, "%-32s %-10s %s%s\n", workspace.Name, workspace.Phase, workspace.Owner, region)
	}
	return 0
}

func runCLIConfig(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	output := fs.String("output", "", "write the generated config to this file")
	install := fs.Bool("install", false, "merge the config into ~/.ssh/config")
	key := fs.String("key", "", "SSH private key path")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: poddy config [options]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	portal, err := cliRequest[cliPortalConfig](ctx, settings, http.MethodGet, "/api/config", nil)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	if !portal.SSHEnabled {
		fmt.Fprintln(stderr, "poddy: SSH is not enabled on this portal")
		return 1
	}
	keyPath := settings.KeyPath
	if strings.TrimSpace(*key) != "" {
		keyPath = expandHome(strings.TrimSpace(*key))
	}
	if keyPath == "" {
		keyPath = defaultCLIKeyPath()
	}
	text := generateCLIConfig(portal, keyPath)
	switch {
	case *install:
		path := expandHome("~/.ssh/config")
		if err := installManagedConfig(path, text); err != nil {
			fmt.Fprintf(stderr, "poddy: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Updated %s\n", path)
	case *output != "":
		path := expandHome(*output)
		if err := writeSecureFile(path, []byte(text), 0o600); err != nil {
			fmt.Fprintf(stderr, "poddy: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Wrote %s\n", path)
	default:
		fmt.Fprint(stdout, text)
	}
	return 0
}

func runCLISSH(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("ssh", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.String("key", "", "SSH private key path")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: poddy ssh [options] <workspace> [-- command...]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return 2
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	if workspace == "" {
		fs.Usage()
		return 2
	}
	command := fs.Args()[1:]
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	settings, err := loadCLISettings()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	keyPath := settings.KeyPath
	if strings.TrimSpace(*key) != "" {
		keyPath = expandHome(strings.TrimSpace(*key))
	}
	if keyPath == "" {
		keyPath = defaultCLIWorkspaceKeyPath(workspace)
	}
	keyPath, err = ensureCLIKey(keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	if err := prepareCLIAccess(settings, workspace, keyPath, stderr); err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	host, port, err := cliSSHEndpoint(settings, workspace)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	sshArgs := cliSSHArgs(host, port, keyPath, workspace, false, "")
	sshArgs = append(sshArgs, command...)
	return runSSHCommand(sshArgs)
}

func runCLIPortForward(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("port-forward", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.String("key", "", "SSH private key path")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: poddy port-forward [options] <workspace> <local>:<remote>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return 2
	}
	workspace := strings.TrimSpace(fs.Arg(0))
	forward, err := normalizeForward(fs.Arg(1))
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 2
	}
	settings, err := loadCLISettings()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	keyPath := settings.KeyPath
	if strings.TrimSpace(*key) != "" {
		keyPath = expandHome(strings.TrimSpace(*key))
	}
	if keyPath == "" {
		keyPath = defaultCLIWorkspaceKeyPath(workspace)
	}
	keyPath, err = ensureCLIKey(keyPath)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	if err := prepareCLIAccess(settings, workspace, keyPath, stderr); err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	host, port, err := cliSSHEndpoint(settings, workspace)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	sshArgs := cliSSHArgs(host, port, keyPath, workspace, true, forward)
	return runSSHCommand(sshArgs)
}

func runSSHCommand(args []string) int {
	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		fmt.Fprintln(os.Stderr, "poddy: ssh client not found in PATH")
		return 1
	}
	cmd := exec.Command(sshPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "poddy: %v\n", err)
		return 1
	}
	return 0
}

func cliSSHArgs(host, port, keyPath, workspace string, noCommand bool, forward string) []string {
	nullFile := "/dev/null"
	if runtime.GOOS == "windows" {
		nullFile = "NUL"
	}
	proxy := fmt.Sprintf("ssh -W %%h:%%p -p %s -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o LogLevel=ERROR poddy@%s",
		shellQuote(port), shellQuote(keyPath), shellQuote(host))
	args := []string{
		"-o", "ProxyCommand=" + proxy,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + nullFile,
		"-o", "IdentitiesOnly=yes",
		"-i", keyPath,
		"dev@poddy-ssh-" + workspace,
	}
	if noCommand {
		args = append([]string{"-N", "-o", "ExitOnForwardFailure=yes", "-L", forward}, args...)
	}
	return args
}

func prepareCLIAccess(settings cliSettings, workspace, keyPath string, stderr io.Writer) error {
	if settings.PortalURL == "" || settings.Token == "" {
		return nil
	}
	public, err := publicKeyFor(keyPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cert, err := cliRequest[struct {
		Certificate string `json:"certificate"`
	}](ctx, settings, http.MethodPost, "/api/me/ssh-certificate", map[string]string{
		"key":       public,
		"workspace": workspace,
	})
	if err == nil && strings.TrimSpace(cert.Certificate) != "" {
		return os.WriteFile(keyPath+"-cert.pub", []byte(strings.TrimSpace(cert.Certificate)+"\n"), 0o644)
	}
	var statusErr *cliHTTPError
	if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
		return errors.New("portal rejected the CLI token; run 'poddy login'")
	}
	// Certificate authentication may be disabled. Fall back to enrolling the
	// public key so the bastion and workspace can authorize it directly.
	_, enrollErr := cliRequest[map[string]any](ctx, settings, http.MethodPost, "/api/me/ssh-keys", map[string]string{
		"key": public,
	})
	if enrollErr != nil {
		var enrollStatus *cliHTTPError
		if errors.As(enrollErr, &enrollStatus) &&
			(enrollStatus.StatusCode == http.StatusConflict || enrollStatus.StatusCode == http.StatusNotFound || enrollStatus.StatusCode == http.StatusNotImplemented) {
			return nil
		}
		return fmt.Errorf("prepare SSH access: %w", enrollErr)
	}
	return nil
}

func cliSSHEndpoint(settings cliSettings, workspace string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	portal, err := cliRequest[cliPortalConfig](ctx, settings, http.MethodGet, "/api/config", nil)
	if err != nil {
		return "", "", err
	}
	if !portal.SSHEnabled || portal.SSHHost == "" {
		return "", "", errors.New("SSH is not enabled on this portal")
	}
	host, port := portal.SSHHost, portal.SSHPort
	if port == "" {
		port = "2222"
	}
	workspaces, listErr := cliRequest[[]cliWorkspace](ctx, settings, http.MethodGet, "/api/workspaces", nil)
	if listErr == nil {
		for _, item := range workspaces {
			if item.Name != workspace || item.Region == "" {
				continue
			}
			for _, region := range portal.Regions {
				if region.Name == item.Region && region.SSHHost != "" {
					host = region.SSHHost
					if region.SSHPort != "" {
						port = region.SSHPort
					}
					break
				}
			}
		}
	}
	return host, port, nil
}

func generateCLIConfig(portal cliPortalConfig, keyPath string) string {
	key := expandHome(keyPath)
	regions := portal.Regions
	if len(regions) == 0 {
		regions = []struct {
			Name    string `json:"name"`
			SSHHost string `json:"sshHost"`
			SSHPort string `json:"sshPort"`
		}{{SSHHost: portal.SSHHost, SSHPort: portal.SSHPort}}
	}
	var out strings.Builder
	out.WriteString("# Poddy: generated by `poddy config`\n")
	for _, region := range regions {
		slug := ""
		if region.Name != "" {
			slug = cliSlug(region.Name) + "-"
		}
		proxy := "poddy-portal-" + slug
		out.WriteString("Host poddy-ssh-" + slug + "*\n")
		out.WriteString("    User dev\n")
		out.WriteString("    ProxyCommand ssh -W %h:%p " + proxy + "\n")
		out.WriteString("    IdentityFile " + key + "\n")
		out.WriteString("    IdentitiesOnly yes\n")
		out.WriteString("    StrictHostKeyChecking no\n")
		out.WriteString("    UserKnownHostsFile /dev/null\n")
		out.WriteString("    LogLevel ERROR\n\n")
	}
	for _, region := range regions {
		slug := ""
		if region.Name != "" {
			slug = cliSlug(region.Name) + "-"
		}
		host := region.SSHHost
		if host == "" {
			host = portal.SSHHost
		}
		port := region.SSHPort
		if port == "" {
			port = portal.SSHPort
		}
		if port == "" {
			port = "2222"
		}
		out.WriteString("Host poddy-portal-" + slug + "\n")
		out.WriteString("    HostName " + host + "\n")
		out.WriteString("    Port " + port + "\n")
		out.WriteString("    User poddy\n")
		out.WriteString("    IdentityFile " + key + "\n")
		out.WriteString("    IdentitiesOnly yes\n")
		out.WriteString("    StrictHostKeyChecking accept-new\n")
		out.WriteString("    LogLevel ERROR\n\n")
	}
	return out.String()
}

func browserLogin(ctx context.Context, portal string, open bool, stderr io.Writer) (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/callback"
	stateBytes := make([]byte, 24)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	authURL := portal + "/api/cli/authorize?redirect_uri=" + url.QueryEscape(redirect) + "&state=" + url.QueryEscape(state)
	fmt.Fprintf(stderr, "Open this URL to sign in:\n\n  %s\n\n", authURL)
	if open {
		if err := openBrowser(authURL); err != nil {
			fmt.Fprintf(stderr, "poddy: could not open browser: %v\n", err)
		}
	}
	codes := make(chan string, 1)
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			if r.URL.Query().Get("state") != state {
				http.Error(w, "invalid state", http.StatusBadRequest)
				return
			}
			code := strings.TrimSpace(r.URL.Query().Get("code"))
			if code == "" {
				http.Error(w, "missing code", http.StatusBadRequest)
				return
			}
			select {
			case codes <- code:
			default:
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<!doctype html><title>Poddy</title><p>Poddy CLI authenticated. You can close this window.</p>")
		}),
	}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case code := <-codes:
		return exchangeCLICode(ctx, portal, code)
	}
}

func exchangeCLICode(ctx context.Context, portal, code string) (string, error) {
	body, _ := json.Marshal(map[string]string{"code": code})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, portal+"/api/cli/token", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("portal returned %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("portal returned an empty token")
	}
	return out.Token, nil
}

type cliHTTPError struct {
	StatusCode int
	Message    string
}

func (e *cliHTTPError) Error() string {
	if e.Message == "" {
		return http.StatusText(e.StatusCode)
	}
	return e.Message
}

func cliRequest[T any](ctx context.Context, settings cliSettings, method, path string, body any) (T, error) {
	var out T
	if settings.PortalURL == "" {
		return out, errors.New("portal URL is required; run 'poddy login --portal URL'")
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return out, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, settings.PortalURL+path, reader)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if settings.Token != "" {
		req.Header.Set("Authorization", "Bearer "+settings.Token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &cliHTTPError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(payload))}
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return out, fmt.Errorf("decode portal response: %w", err)
	}
	return out, nil
}

func cliConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "poddy", "config.json"), nil
}

func loadCLISettings() (cliSettings, error) {
	var settings cliSettings
	path, err := cliConfigPath()
	if err != nil {
		return settings, err
	}
	if data, err := os.ReadFile(path); err == nil { // #nosec G304 -- User configuration path.
		if err := json.Unmarshal(data, &settings); err != nil {
			return settings, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return settings, err
	}
	if value := strings.TrimSpace(os.Getenv("PODDY_PORTAL_URL")); value != "" {
		settings.PortalURL = value
	}
	if value := strings.TrimSpace(os.Getenv("PODDY_TOKEN")); value != "" {
		settings.Token = value
	}
	if value := strings.TrimSpace(os.Getenv("PODDY_SSH_KEY")); value != "" {
		settings.KeyPath = expandHome(value)
	}
	if settings.PortalURL != "" {
		settings.PortalURL = strings.TrimRight(settings.PortalURL, "/")
	}
	return settings, nil
}

func saveCLISettings(settings cliSettings) error {
	path, err := cliConfigPath()
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return writeSecureFile(path, append(raw, '\n'), 0o600)
}

func writeSecureFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".poddy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func normalizePortal(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid portal URL %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("portal URL must use http or https")
	}
	return raw, nil
}

func defaultCLIKeyPath() string {
	return expandHome("~/.ssh/poddy_ed25519")
}

func defaultCLIWorkspaceKeyPath(workspace string) string {
	return expandHome("~/.ssh/poddy_" + cliSlug(workspace) + "_ed25519")
}

func ensureCLIKey(path string) (string, error) {
	path = expandHome(path)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(private, "poddy")
	if err != nil {
		return "", err
	}
	if err := writeSecureFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		return "", err
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path+".pub", ssh.MarshalAuthorizedKey(sshPublic), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func publicKeyFor(path string) (string, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- User-selected key path.
	if err != nil {
		return "", err
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return "", fmt.Errorf("parse SSH key: %w", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), nil
}

func normalizeForward(value string) (string, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return "", fmt.Errorf("forward must be <local>:<remote> or <local>:<host>:<remote>")
	}
	local := parts[0]
	remoteHost := "127.0.0.1"
	remotePort := parts[1]
	if len(parts) == 3 {
		remoteHost = parts[1]
		remotePort = parts[2]
	}
	if local == "" || remoteHost == "" || remotePort == "" {
		return "", fmt.Errorf("forward must include local port, host, and remote port")
	}
	return local + ":" + remoteHost + ":" + remotePort, nil
}

func installManagedConfig(path, block string) error {
	path = expandHome(path)
	current := ""
	if data, err := os.ReadFile(path); err == nil { // #nosec G304 -- Standard SSH config path.
		current = string(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	const begin = "# >>> poddy:cli"
	const end = "# <<< poddy:cli"
	if start := strings.Index(current, begin); start >= 0 {
		if stop := strings.Index(current[start:], end); stop >= 0 {
			current = current[:start] + current[start+stop+len(end):]
		}
	}
	current = strings.TrimRight(current, "\n")
	if current != "" {
		current += "\n\n"
	}
	current += begin + "\n" + strings.TrimRight(block, "\n") + "\n" + end + "\n"
	return writeSecureFile(path, []byte(current), 0o600)
}

func cliSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && out.Len() > 0 {
				out.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(out.String(), "-")
}

func shellQuote(value string) string {
	if runtime.GOOS == "windows" {
		if !strings.ContainsAny(value, " \t\"&|<>^") {
			return value
		}
		return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t'\"\\$`;&|()<>*?[]{}!#~") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
