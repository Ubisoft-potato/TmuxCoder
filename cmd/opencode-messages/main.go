package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	messagespanel "github.com/opencode/tmux_coder/internal/panels/messages"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := messagespanel.DefaultRunConfig()
	if err != nil {
		log.Fatalf("messages panel config error: %v", err)
	}

	if err := messagespanel.Run(ctx, cfg, nil); err != nil {
		log.Fatalf("messages panel exited: %v", err)
	}
}
