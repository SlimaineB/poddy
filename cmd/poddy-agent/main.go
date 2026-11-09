package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"poddy/internal/tunnel"
)

func main() {
	url := flag.String("url", os.Getenv("PODDY_AGENT_URL"), "portal tunnel URL")
	token := flag.String("token", os.Getenv("PODDY_AGENT_TOKEN"), "workspace agent token")
	workspace := flag.String("workspace", os.Getenv("PODDY_WORKSPACE_NAME"), "workspace name")
	target := flag.String("target", env("PODDY_AGENT_TARGET", "127.0.0.1:22"), "local TCP target")
	insecure := flag.Bool("insecure", os.Getenv("PODDY_AGENT_INSECURE") == "true", "skip TLS verification (development only)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	agent := tunnel.NewAgent(tunnel.AgentConfig{
		URL:       *url,
		Token:     *token,
		Workspace: *workspace,
		Target:    *target,
		Insecure:  *insecure,
	})
	log.Printf("workspace agent starting for %s", *workspace)
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
