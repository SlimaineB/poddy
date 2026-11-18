package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const usage = `poddy <command> [options]

Commands:
  external-auth status [provider]   Show external auth connection status
  external-auth login <provider>    Reconnect an external account
  external-auth token <provider>    Print a refreshed access token
  git-askpass <prompt>              Git askpass helper using the portal
  help                              Show this help

The workspace must provide PODDY_PORTAL_URL, PODDY_WORKSPACE_NAME and
PODDY_WORKSPACE_TOKEN.`

var version = "dev"

type config struct {
	PortalURL string
	Workspace string
	Token     string
}

type apiError struct {
	StatusCode int
	Message    string
}

func (e *apiError) Error() string {
	if e.Message == "" {
		return http.StatusText(e.StatusCode)
	}
	return e.Message
}

type tokenResponse struct {
	AccessToken string     `json:"accessToken"`
	TokenType   string     `json:"tokenType"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

type loginResponse struct {
	Provider  string     `json:"provider"`
	LoginURL  string     `json:"loginUrl"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

type apiClient struct {
	cfg  config
	http *http.Client
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, usage)
		return 0
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, version)
		return 0
	case "external-auth", "ext-auth":
		return runExternalAuth(args[1:], stdout, stderr)
	case "git-askpass":
		return runGitAskpass(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "poddy: unknown command %q\n\n%s\n", args[0], usage)
		return 2
	}
}

func runExternalAuth(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: poddy external-auth <status|login|token> [options]")
		return 2
	}
	switch args[0] {
	case "status":
		return runExternalAuthStatus(args[1:], stdout, stderr)
	case "login":
		return runExternalAuthLogin(args[1:], stdout, stderr)
	case "token":
		return runExternalAuthToken(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "poddy: unknown external-auth command %q\n", args[0])
		return 2
	}
}

func runExternalAuthStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("external-auth status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: poddy external-auth status [provider]")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return 2
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	providers := []string{}
	if fs.NArg() == 1 {
		providers = append(providers, fs.Arg(0))
	} else {
		providers = splitList(os.Getenv("PODDY_EXTERNAL_AUTH_IDS"))
	}
	if len(providers) == 0 {
		fmt.Fprintln(stderr, "poddy: provider is required (or set PODDY_EXTERNAL_AUTH_IDS)")
		return 2
	}
	for _, provider := range providers {
		if !validProvider(provider) {
			fmt.Fprintf(stderr, "poddy: invalid provider id: %s\n", provider)
			return 2
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := newAPIClient(cfg)
	disconnected := 0
	for _, provider := range providers {
		token, err := client.token(ctx, provider)
		if err == nil {
			expiry := ""
			if token.ExpiresAt != nil && !token.ExpiresAt.IsZero() {
				expiry = " (expires " + token.ExpiresAt.Format(time.RFC3339) + ")"
			}
			fmt.Fprintf(stdout, "%s: connected%s\n", provider, expiry)
			continue
		}
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
			fmt.Fprintf(stdout, "%s: not connected\n", provider)
			disconnected++
			continue
		}
		fmt.Fprintf(stderr, "poddy: %s: %v\n", provider, err)
		return 1
	}
	if disconnected > 0 {
		return 1
	}
	return 0
}

func runExternalAuthLogin(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("external-auth login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	next := fs.String("next", "/", "portal path to open after authentication")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for authentication")
	interval := fs.Duration("interval", 2*time.Second, "how often to poll the portal")
	printToken := fs.Bool("print-token", false, "print the access token after authentication")
	forceOpen := fs.Bool("open", false, "open the login URL in a browser")
	noOpen := fs.Bool("no-open", false, "do not open the login URL in a browser")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: poddy external-auth login [options] <provider>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	provider := fs.Arg(0)
	if !validProvider(provider) {
		fmt.Fprintf(stderr, "poddy: invalid provider id: %s\n", provider)
		return 2
	}
	if *interval <= 0 {
		fmt.Fprintln(stderr, "poddy: --interval must be positive")
		return 2
	}
	if *timeout <= 0 {
		fmt.Fprintln(stderr, "poddy: --timeout must be positive")
		return 2
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := newAPIClient(cfg)
	login, err := client.login(ctx, provider, *next)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	if login.LoginURL == "" {
		fmt.Fprintln(stderr, "poddy: portal returned no login URL")
		return 1
	}
	fmt.Fprintf(stderr, "Reconnect %s external auth:\n\n  %s\n\n", provider, login.LoginURL)
	if *forceOpen || (!*noOpen && canOpenBrowser()) {
		if err := openBrowser(login.LoginURL); err != nil {
			fmt.Fprintf(stderr, "poddy: could not open browser: %v\n", err)
		}
	}
	fmt.Fprintln(stderr, "Waiting for authentication...")

	deadline := time.Now().Add(*timeout)
	if login.ExpiresAt != nil && !login.ExpiresAt.IsZero() && login.ExpiresAt.Before(deadline) {
		deadline = *login.ExpiresAt
	}
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Fprintln(stderr, "poddy: cancelled")
			return 130
		case <-ticker.C:
			token, err := client.token(ctx, provider)
			if err == nil {
				if *printToken {
					fmt.Fprintln(stdout, token.AccessToken)
				} else {
					fmt.Fprintf(stderr, "Connected to %s.\n", provider)
				}
				return 0
			}
			var apiErr *apiError
			if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
				if time.Now().After(deadline) {
					fmt.Fprintf(stderr, "poddy: timed out waiting for %s authentication\n", provider)
					return 1
				}
				continue
			}
			if time.Now().After(deadline) {
				fmt.Fprintf(stderr, "poddy: %v\n", err)
				return 1
			}
		}
	}
}

func runExternalAuthToken(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("external-auth token", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: poddy external-auth token <provider>")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	provider := fs.Arg(0)
	if !validProvider(provider) {
		fmt.Fprintf(stderr, "poddy: invalid provider id: %s\n", provider)
		return 2
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	token, err := newAPIClient(cfg).token(ctx, provider)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
			fmt.Fprintf(stderr, "poddy: %s is not connected; run 'poddy external-auth login %s'\n", provider, provider)
			return 1
		}
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	if token.AccessToken == "" {
		fmt.Fprintln(stderr, "poddy: portal response has no access token")
		return 1
	}
	fmt.Fprintln(stdout, token.AccessToken)
	return 0
}

func runGitAskpass(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintln(stderr, "usage: poddy git-askpass <prompt>")
		return 2
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	value, err := newAPIClient(cfg).askpass(ctx, args[0])
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, strings.TrimRight(value, "\r\n"))
	return 0
}

func loadConfig() (config, error) {
	cfg := config{
		PortalURL: strings.TrimRight(strings.TrimSpace(os.Getenv("PODDY_PORTAL_URL")), "/"),
		Workspace: strings.TrimSpace(os.Getenv("PODDY_WORKSPACE_NAME")),
		Token:     strings.TrimSpace(os.Getenv("PODDY_WORKSPACE_TOKEN")),
	}
	missing := []string{}
	if cfg.PortalURL == "" {
		missing = append(missing, "PODDY_PORTAL_URL")
	}
	if cfg.Workspace == "" {
		missing = append(missing, "PODDY_WORKSPACE_NAME")
	}
	if cfg.Token == "" {
		missing = append(missing, "PODDY_WORKSPACE_TOKEN")
	}
	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	parsed, err := url.Parse(cfg.PortalURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return cfg, fmt.Errorf("invalid PODDY_PORTAL_URL: %q", cfg.PortalURL)
	}
	return cfg, nil
}

func newAPIClient(cfg config) *apiClient {
	return &apiClient{cfg: cfg, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *apiClient) token(ctx context.Context, provider string) (tokenResponse, error) {
	var out tokenResponse
	err := c.json(ctx, http.MethodGet, c.workspacePath("external-auth", provider, "token"), nil, &out)
	return out, err
}

func (c *apiClient) login(ctx context.Context, provider, next string) (loginResponse, error) {
	var out loginResponse
	query := url.Values{}
	if next != "" {
		query.Set("next", next)
	}
	err := c.json(ctx, http.MethodPost, c.workspacePath("external-auth", provider, "login"), query, &out)
	return out, err
}

func (c *apiClient) askpass(ctx context.Context, prompt string) (string, error) {
	query := url.Values{"prompt": {prompt}}
	body, err := c.request(ctx, http.MethodGet, c.workspacePath("git-askpass"), query)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *apiClient) workspacePath(parts ...string) string {
	escaped := make([]string, 0, len(parts)+3)
	escaped = append(escaped, "api", "workspaces", url.PathEscape(c.cfg.Workspace))
	for _, part := range parts {
		escaped = append(escaped, url.PathEscape(part))
	}
	return "/" + strings.Join(escaped, "/")
}

func (c *apiClient) json(ctx context.Context, method, path string, query url.Values, out any) error {
	body, err := c.request(ctx, method, path, query)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode portal response: %w", err)
	}
	return nil
}

func (c *apiClient) request(ctx context.Context, method, path string, query url.Values) ([]byte, error) {
	endpoint := c.cfg.PortalURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Poddy-Workspace-Token", c.cfg.Token)
	req.Header.Set("Accept", "application/json, text/plain")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return nil, &apiError{StatusCode: resp.StatusCode, Message: message}
	}
	return body, nil
}

func splitList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			out = append(out, field)
		}
	}
	return out
}

func validProvider(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func canOpenBrowser() bool {
	if os.Getenv("PODDY_NO_BROWSER") != "" {
		return false
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	return browserCommand() != ""
}

func browserCommand() string {
	if browser := strings.TrimSpace(os.Getenv("PODDY_BROWSER")); browser != "" {
		return browser
	}
	for _, name := range []string{"xdg-open", "open"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

func openBrowser(target string) error {
	browser := browserCommand()
	if browser == "" {
		return errors.New("no browser opener found")
	}
	fields := strings.Fields(browser)
	if len(fields) == 0 {
		return errors.New("empty browser command")
	}
	args := append(fields[1:], target)
	return exec.Command(fields[0], args...).Start() // #nosec G204 -- Command is configured by the local user.
}
