package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/liza-mas/liza/internal/embedded"
	"github.com/liza-mas/liza/internal/mcp"
	"github.com/liza-mas/liza/internal/roles"
)

func main() {
	var projectRoot string
	flag.StringVar(&projectRoot, "project-root", ".", "Path to Liza project root directory")
	flag.Parse()

	absProjectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving project root: %v\n", err)
		os.Exit(1)
	}

	logPath := filepath.Join(absProjectRoot, ".liza", "log.yaml")

	// Determine agent role from environment variable (set by supervisor).
	// Empty string means no filtering — all tools exposed (backwards compat).
	role := os.Getenv("LIZA_ROLE")
	if role != "" && !roles.IsValidRuntime(role) {
		fmt.Fprintf(os.Stderr, "Warning: unknown LIZA_ROLE %q, ignoring\n", role)
		role = ""
	}

	// Set MCP version from build-time embedded variables
	mcp.Version = embedded.Version
	mcp.BuildCommit = embedded.GitCommit

	// Create MCP server with optional role-based tool filtering
	server := mcp.NewServer(absProjectRoot, logPath, role)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Run()
	}()

	select {
	case err := <-errChan:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
		// Normal exit (EOF from stdin)
		os.Exit(0)
	case sig := <-sigChan:
		fmt.Fprintf(os.Stderr, "Received signal %v, shutting down...\n", sig)
		os.Exit(0)
	}
}
