// Copyright (c) 2024 simplemon
// SPDX-License-Identifier: MIT
//
// Main entry point for the simplemon monitoring agent.

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"simplemon-agent/config"
	"simplemon-agent/internal/agent"
	"syscall"
)

// Injected at build time via -ldflags in scripts/deploy-agent.sh
var (
	version = "dev"
	commit  = "none"
)

func main() {
	// Checked manually: config.LoadConfig owns flag parsing.
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Printf("simplemon-agent %s (commit %s)\n", version, commit)
		return
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading configuration: %v\n", err)
		os.Exit(1)
	}

	// Validate config
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: Invalid configuration: %v\n", err)
		os.Exit(1)
	}

	// Create context with signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		fmt.Printf("\nReceived signal: %v\n", sig)
		cancel()
	}()

	// Create and run agent
	fmt.Printf("[agent] simplemon-agent %s (commit %s)\n", version, commit)
	agent.SetBuildInfo(version, commit)
	a := agent.New(cfg)
	if err := a.Run(ctx); err != nil {
		fmt.Println("Agent error:", err)
		os.Exit(1)
	}
}
