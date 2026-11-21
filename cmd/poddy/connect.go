package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"poddy/internal/meshclient"
)

func runCLIConnect(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	controlURL := fs.String("control-url", "", "mesh control server URL")
	authKey := fs.String("auth-key", "", "mesh auth key")
	authKeyFile := fs.String("auth-key-file", "", "file containing the mesh auth key")
	workspace := fs.String("workspace", "", "workspace name")
	hostname := fs.String("hostname", "", "mesh hostname for this client")
	stateDir := fs.String("state-dir", "", "writable mesh state directory")
	caFile := fs.String("ca-file", "", "additional CA bundle for mesh TLS")
	port := fs.Int("port", 22, "workspace SSH port")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 2
	}
	key := strings.TrimSpace(*authKey)
	if key == "" && strings.TrimSpace(*authKeyFile) != "" {
		data, err := os.ReadFile(*authKeyFile)
		if err != nil {
			fmt.Fprintf(stderr, "poddy: read auth key file: %v\n", err)
			return 1
		}
		key = strings.TrimSpace(string(data))
	}
	if strings.TrimSpace(*workspace) == "" || strings.TrimSpace(*controlURL) == "" || key == "" {
		fmt.Fprintln(stderr, "poddy: --control-url, --workspace and --auth-key-file are required")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := meshclient.Run(ctx, meshclient.Config{
		ControlURL: *controlURL,
		AuthKey:    key,
		Workspace:  *workspace,
		Hostname:   *hostname,
		StateDir:   *stateDir,
		CAFile:     *caFile,
		Port:       *port,
	}, os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintf(stderr, "poddy: %v\n", err)
		return 1
	}
	return 0
}
