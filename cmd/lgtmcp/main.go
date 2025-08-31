// Package main implements the LGTMCP MCP server.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/shields/lgtmcp/internal/config"
	"github.com/shields/lgtmcp/internal/logging"
	mcpserver "github.com/shields/lgtmcp/pkg/mcp"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Load configuration.
	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error loading config from %s: %v\n", config.GetConfigPath(), err)

		return 1
	}

	// Initialize logging system.
	logConfig := logging.Config{
		Level:     cfg.Logging.Level,
		Output:    cfg.Logging.Output,
		Directory: cfg.Logging.Directory,
	}

	appLogger, err := logging.New(logConfig)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error initializing logger: %v\n", err)

		return 1
	}
	defer func() {
		if closeErr := appLogger.Close(); closeErr != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Error closing logger: %v\n", closeErr)
		}
	}()

	// Create server.
	server, err := mcpserver.New(cfg, appLogger)
	if err != nil {
		appLogger.Error("Error creating server", "error", err)

		return 1
	}

	// Run server in goroutine.
	errChan := make(chan error, 1)
	go func() {
		appLogger.Info("Starting lgtmcp server...")
		errChan <- server.Run(ctx)
	}()

	// Wait for shutdown signal or error.
	select {
	case <-sigChan:
		appLogger.Info("Received shutdown signal")

		return 0
	case err := <-errChan:
		if err != nil {
			appLogger.Error("Server error", "error", err)

			return 1
		}
	}

	return 0
}
