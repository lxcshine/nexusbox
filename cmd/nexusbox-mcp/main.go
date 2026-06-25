// Package main implements the nexusbox-mcp command, an stdio MCP server
// entry point that can be launched directly by AI editors such as Trae
// and Cursor via the MCP "command" configuration.
//
// Usage:
//
//	nexusbox-mcp -workspace /path/to/project
//
// The server reads JSON-RPC 2.0 messages from stdin and writes responses
// to stdout, one message per line. All diagnostic logs go to stderr so
// they never corrupt the stdio channel used for protocol traffic.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/mcp"
)

func main() {
	var (
		workspace = flag.String("workspace", "", "Workspace directory the sandbox is allowed to access (default: current dir)")
		logLevel  = flag.String("log-level", "warn", "Log level (debug|info|warn|error). Logs are written to stderr.")
	)
	klog.InitFlags(nil)
	flag.Parse()

	// klog must write to stderr only — stdout is reserved for MCP JSON-RPC.
	klog.SetOutput(os.Stderr)

	switch *logLevel {
	case "debug":
		_ = flag.Set("v", "4")
	case "info":
		_ = flag.Set("v", "2")
	case "warn":
		_ = flag.Set("v", "1")
	case "error":
		_ = flag.Set("v", "0")
	}

	ws, err := resolveWorkspace(*workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexusbox-mcp: invalid workspace: %v\n", err)
		os.Exit(2)
	}

	hub := mcp.NewHub(&mcp.HubConfig{
		Port:      0, // unused in stdio mode
		Workspace: ws,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle SIGINT/SIGTERM for clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-sigCh
		klog.Infof("nexusbox-mcp: received signal %s, shutting down", s)
		cancel()
	}()

	klog.Infof("nexusbox-mcp: starting stdio transport (workspace=%s)", ws)
	klog.Infof("nexusbox-mcp: registered servers: %v", hub.ListServers())

	if err := hub.ServeStdio(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "nexusbox-mcp: %v\n", err)
		os.Exit(1)
	}
}

// resolveWorkspace absolutizes and validates the workspace path.
func resolveWorkspace(ws string) (string, error) {
	if ws == "" {
		ws = "."
	}
	abs, err := filepath.Abs(ws)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("not a directory: %s", abs)
	}
	return abs, nil
}
