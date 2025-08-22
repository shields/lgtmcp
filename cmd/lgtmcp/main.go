package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/shields/lgtmcp/internal/config"
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

	// Set log level based on config.
	setLogLevel(cfg.Logging.Level)

	// Create server.
	server, err := mcpserver.New(cfg)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error creating server: %v\n", err)

		return 1
	}

	// Run server in goroutine.
	errChan := make(chan error, 1)
	go func() {
		slog.Info("Starting lgtmcp server...")
		errChan <- server.Run(ctx)
	}()

	// Wait for shutdown signal or error.
	select {
	case <-sigChan:
		slog.Info("Received shutdown signal")

		return 0
	case err := <-errChan:
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Server error: %v\n", err)

			return 1
		}
	}

	return 0
}

// setLogLevel configures the slog level based on config.
func setLogLevel(level string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	})
	slog.SetDefault(slog.New(handler))
}
