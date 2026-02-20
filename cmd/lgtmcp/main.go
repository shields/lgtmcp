// Copyright © 2025 Michael Shields
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package main implements the LGTMCP MCP server.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"msrl.dev/lgtmcp/internal/appinfo"
	"msrl.dev/lgtmcp/internal/config"
	"msrl.dev/lgtmcp/internal/logging"
	mcpserver "msrl.dev/lgtmcp/pkg/mcp"
)

var versionFlag = flag.Bool("version", false, "Show version information")

func main() {
	flag.Parse()
	os.Exit(run())
}

func run() int {
	if *versionFlag {
		_, _ = fmt.Fprintln(os.Stdout, appinfo.String()) //nolint:errcheck // stdout write failure is not actionable
		return 0
	}

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
