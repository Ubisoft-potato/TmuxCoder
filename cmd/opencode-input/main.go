package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	inputpanel "github.com/opencode/tmux_coder/internal/panels/input"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := inputpanel.DefaultRunConfig()
	if err != nil {
		log.Fatalf("input panel config error: %v", err)
	}

	if err := inputpanel.Run(ctx, cfg, nil); err != nil {
		log.Fatalf("input panel exited: %v", err)
	}
}
