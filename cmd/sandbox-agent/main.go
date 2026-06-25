package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/agent"
	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// SandboxAgent is the main entry point for the sandbox agent.
// It runs on each node and manages local sandbox runtimes.
func main() {
	var (
		nodeName          = flag.String("node-name", "", "Name of this node")
		nodeIP            = flag.String("node-ip", "", "IP address of this node")
		controlPlaneURL   = flag.String("control-plane-url", "http://localhost:8080", "Control plane API URL")
		listenPort        = flag.Int("port", 9090, "Agent HTTP server port")
		heartbeatInterval = flag.Duration("heartbeat-interval", 10, "Heartbeat interval in seconds")
		maxSandboxes      = flag.Int("max-sandboxes", 100, "Maximum sandboxes per node")
	)
	klog.InitFlags(nil)
	flag.Parse()

	klog.Info("Starting NexusBox Sandbox Agent")
	klog.Info("=============================")

	// Build agent config
	config := &agent.AgentConfig{
		NodeName:          *nodeName,
		NodeIP:            *nodeIP,
		ControlPlaneURL:   *controlPlaneURL,
		ListenPort:        *listenPort,
		HeartbeatInterval: *heartbeatInterval,
		MetricsInterval:   15 * time.Second,
		MaxSandboxes:      int32(*maxSandboxes),
		SupportedRuntimes: []sandboxv1alpha1.SandboxRuntimeType{
			sandboxv1alpha1.RuntimeKataContainers,
			sandboxv1alpha1.RuntimeGVisor,
			sandboxv1alpha1.RuntimeRunc,
		},
	}

	// Create agent
	ag, err := agent.NewAgent(config)
	if err != nil {
		klog.Fatalf("Failed to create agent: %v", err)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start agent
	if err := ag.Start(ctx); err != nil {
		klog.Fatalf("Failed to start agent: %v", err)
	}

	klog.Infof("Sandbox Agent started on node %s (%s)", config.NodeName, config.NodeIP)

	// Wait for shutdown signal
	select {
	case sig := <-sigCh:
		klog.Infof("Received signal %v, shutting down", sig)
	case <-ctx.Done():
		klog.Info("Context cancelled, shutting down")
	}

	// Graceful shutdown
	if err := ag.Stop(); err != nil {
		klog.Errorf("Error stopping agent: %v", err)
	}

	klog.Info("Sandbox Agent stopped")
	fmt.Println("NexusBox Sandbox Agent exited cleanly")
}
