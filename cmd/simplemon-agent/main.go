// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Command simplemon-agent collects Linux host metrics and sends them to a
// SimpleMon backend.

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/cookiebinary1/simplemon-agent/config"
	"github.com/cookiebinary1/simplemon-agent/internal/agent"
)

// Set at build time with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = "none"
)

func main() {
	configPath := flag.String("config", "", "config file (default ./simplemon.conf, then /etc/simplemon.conf)")
	verbose := flag.Bool("verbose", false, "enable debug logging")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("simplemon-agent %s (commit %s)\n", version, commit)
		return
	}

	cfg, path, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "simplemon-agent:", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "simplemon-agent: invalid config %s: %v\n", path, err)
		os.Exit(1)
	}
	if *verbose {
		cfg.Verbose = true
	}

	level := slog.LevelInfo
	if cfg.Verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	slog.Info("starting simplemon-agent", "version", version, "commit", commit, "config", path)
	slog.Info("config",
		"serviceHost", cfg.ServiceHost,
		"secure", cfg.Secure,
		"serverToken", config.MaskSecret(cfg.ServerToken),
		"metricInterval_ms", cfg.MetricInterval,
		"storeInterval_ms", cfg.StoreInterval,
		"batchInterval_ms", cfg.BatchInterval,
		"healthPort", cfg.HealthPort,
	)
	if !cfg.Secure {
		slog.Warn("secure is false: the server token is sent unencrypted")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	agent.Version = version
	if err := agent.New(cfg).Run(ctx); err != nil {
		slog.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}
