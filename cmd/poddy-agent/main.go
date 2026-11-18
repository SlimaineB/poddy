package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"poddy/internal/tunnel"
)

func main() {
	url := flag.String("url", os.Getenv("PODDY_AGENT_URL"), "portal tunnel URL")
	token := flag.String("token", os.Getenv("PODDY_AGENT_TOKEN"), "workspace agent token")
	tokenFile := flag.String("token-file", os.Getenv("PODDY_AGENT_TOKEN_FILE"), "file used to persist a renewed agent token")
	workspace := flag.String("workspace", os.Getenv("PODDY_WORKSPACE_NAME"), "workspace name")
	target := flag.String("target", env("PODDY_AGENT_TARGET", "127.0.0.1:22"), "local TCP target")
	insecure := flag.Bool("insecure", os.Getenv("PODDY_AGENT_INSECURE") == "true", "skip TLS verification (development only)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	agent := tunnel.NewAgent(tunnel.AgentConfig{
		URL:       *url,
		Token:     *token,
		TokenFile: *tokenFile,
		Workspace: *workspace,
		Target:    *target,
		Insecure:  *insecure,
	})
	log.Printf("workspace agent starting for %s", strings.ReplaceAll(strings.ReplaceAll(*workspace, "\n", " "), "\r", " "))
	if err := agent.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
