package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/renja-g/RiftRelay/internal/app"
	"github.com/renja-g/RiftRelay/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	server, err := app.New(cfg)
	if err != nil {
		log.Fatalf("startup error: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.Start(ctx); err != nil {
		log.Fatalf("server exited with error: %v", err)
	}
}
