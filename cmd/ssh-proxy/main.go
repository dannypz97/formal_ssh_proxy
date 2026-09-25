package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dparv/ssh-proxy/internal/config"
	"github.com/dparv/ssh-proxy/internal/proxy"
)

// shutdownGrace is how long to wait for in-flight LLM summaries on shutdown.
const shutdownGrace = 30 * time.Second

func main() {
	configPath := flag.String("config", "", "path to the YAML configuration file (required)")
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: ssh-proxy -config <path>")
		flag.PrintDefaults()
		os.Exit(2)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("config loaded: listen=%s upstream=%s users=%d llm=%v",
		cfg.Listen, cfg.Upstream.Addr(), len(cfg.Users), cfg.LLMEnabled())

	if err := cfg.EnsureLogDir(); err != nil {
		log.Fatalf("failed to create log directory: %v", err)
	}

	srv, err := proxy.NewServer(cfg)
	if err != nil {
		log.Fatalf("failed to create server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := srv.Serve(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}

	log.Printf("shutting down; waiting up to %s for pending session summaries", shutdownGrace)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	srv.Shutdown(shutdownCtx)
}
