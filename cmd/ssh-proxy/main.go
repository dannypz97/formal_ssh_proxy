package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/dparv/ssh-proxy/internal/config"
	"github.com/dparv/ssh-proxy/internal/proxy"
)

func main() {
	configPath := flag.String("config", "", "path to the YAML configuration file (required)")
	flag.Parse()

	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: ssh-proxy -config <path>")
		flag.PrintDefaults()
		os.Exit(1)
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

	log.Fatal(srv.ListenAndServe())
}
