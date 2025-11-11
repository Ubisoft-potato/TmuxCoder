package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	sessionspanel "github.com/opencode/tmux_coder/internal/panels/sessions"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := sessionspanel.DefaultRunConfig()
	if err != nil {
		log.Fatalf("sessions panel config error: %v", err)
	}

	if err := sessionspanel.Run(ctx, cfg, nil); err != nil {
		log.Fatalf("sessions panel exited: %v", err)
	}
}
