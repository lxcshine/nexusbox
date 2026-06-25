package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	goRuntime "runtime"
	"syscall"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/gateway"
	"github.com/nexusbox/nexusbox/pkg/mcp"
	"github.com/nexusbox/nexusbox/pkg/network/ebpf"
	"github.com/nexusbox/nexusbox/pkg/network/egress"
	"github.com/nexusbox/nexusbox/pkg/proxy"
	"github.com/nexusbox/nexusbox/pkg/sandbox/lifecycle"
	sandboxRuntime "github.com/nexusbox/nexusbox/pkg/sandbox/runtime"
	"github.com/nexusbox/nexusbox/pkg/template"
	"github.com/nexusbox/nexusbox/pkg/tenant"
	"github.com/nexusbox/nexusbox/pkg/tenant/quota"
)

func main() {
	var (
		port       = flag.Int("port", 8080, "Gateway HTTP server port")
		mcpPort    = flag.Int("mcp-port", 8079, "MCP Hub HTTP server port")
		proxyPort  = flag.Int("proxy-port", 6081, "Port proxy server port")
		workspace  = flag.String("workspace", "", "Workspace directory (default: current dir)")
		logLevel   = flag.String("log-level", "info", "Log level (debug|info|warn|error)")
		egressPort = flag.Int("egress-port", 8082, "Egress gateway port (0=disabled)")
	)
	klog.InitFlags(nil)
	flag.Parse()

	// Set log level
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

	fmt.Println("")
	fmt.Println("============================================================")
	fmt.Println("  NexusBox Sandbox - Local Development Server")
	fmt.Println("  Version: 0.1.0")
	fmt.Printf("  Time: %s\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("  Platform: %s/%s\n", goRuntime.GOOS, goRuntime.GOARCH)
	fmt.Println("============================================================")
	fmt.Println("")

	// --- Environment Check ---
	klog.Info("=== Environment Check ===")

	// Workspace
	ws := *workspace
	if ws == "" {
		ws, _ = os.Getwd()
	}
	klog.Infof("Workspace: %s", ws)

	// Check binary dependencies
	checkBinaries := []string{"python3", "python", "node", "npm", "go"}
	for _, bin := range checkBinaries {
		path, err := exec.LookPath(bin)
		if err != nil {
			klog.Warningf("  %s: NOT FOUND", bin)
		} else {
			klog.Infof("  %s: %s", bin, path)
		}
	}

	// System info
	klog.Infof("OS: %s, Arch: %s, CPUs: %d", goRuntime.GOOS, goRuntime.GOARCH, goRuntime.NumCPU())

	fmt.Println("")

	// --- Initialize Services ---
	klog.Info("=== Initializing Services ===")

	// Create managers (minimal setup for local dev)
	runtimeManager := sandboxRuntime.NewRuntimeManager(nil)
	quotaManager := quota.NewQuotaManager()
	tenantManager := tenant.NewTenantManager(quotaManager, nil, nil)
	lifecycleManager := lifecycle.NewLifecycleManager(runtimeManager, tenantManager, nil, nil)

	// Initialize template manager and seed defaults
	templateMgr := template.NewManager()
	if err := templateMgr.SeedDefaults(context.Background()); err != nil {
		klog.Warningf("Failed to seed default templates: %v", err)
	} else {
		klog.Info("Default templates seeded")
	}

	// Initialize eBPF network policy engine
	netPolicyEngine := ebpf.NewEngine(&ebpf.EngineConfig{})
	if err := netPolicyEngine.Init(); err != nil {
		klog.Warningf("Failed to init network policy engine: %v", err)
	} else {
		klog.Infof("Network policy engine initialized (backend=%s)", netPolicyEngine.GetStats().Backend)
	}

	// Register default tenant
	defaultTenant := &v1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: v1alpha1.TenantSpec{
			DisplayName: "Default Tenant",
			AllowedRuntimes: []v1alpha1.SandboxRuntimeType{
				v1alpha1.RuntimeRunc,
				v1alpha1.RuntimeGVisor,
				v1alpha1.RuntimeKataContainers,
			},
			AllowedSchedulingPolicies: []v1alpha1.SandboxSchedulingPolicy{
				v1alpha1.ScheduleBinPack,
				v1alpha1.ScheduleSpread,
			},
			ResourceQuota: v1alpha1.TenantResourceQuota{
				CPU:                 "64",
				Memory:              "128Gi",
				MaxInstances:        100,
				MaxInstancesPerNode: 50,
			},
			MaxConcurrentSandboxes: 100,
			IsolationLevel:         v1alpha1.IsolationLevelStandard,
		},
	}
	if err := tenantManager.RegisterTenant(context.Background(), defaultTenant); err != nil {
		klog.Warningf("Failed to register default tenant: %v (may already exist)", err)
	} else {
		klog.Info("Default tenant registered")
	}

	// Create Gateway
	gatewayConfig := &gateway.GatewayConfig{
		Port:             *port,
		RuntimeManager:   runtimeManager,
		TenantManager:    tenantManager,
		QuotaManager:     quotaManager,
		LifecycleManager: lifecycleManager,
		Workspace:        ws,
		TemplateManager:  templateMgr,
	}
	gw := gateway.NewGateway(gatewayConfig)
	klog.Infof("Gateway created on port %d", *port)

	// Create Egress Gateway (optional)
	var egressGW *egress.Gateway
	if *egressPort > 0 {
		egressGW = egress.NewGateway(&egress.GatewayConfig{
			ListenAddr: fmt.Sprintf(":%d", *egressPort),
		})
		klog.Infof("Egress gateway created on port %d", *egressPort)
	}

	// Create MCP Hub (automatically registers shell, file, code, browser servers)
	mcpHub := mcp.NewHub(&mcp.HubConfig{Port: *mcpPort, Workspace: *workspace})
	klog.Infof("MCP Hub created on port %d with 4 servers (shell, file, code, browser)", *mcpPort)

	// Create Port Proxy
	portProxy := proxy.NewPortProxy(&proxy.PortProxyConfig{Port: *proxyPort})
	klog.Infof("Port Proxy created on port %d", *proxyPort)

	fmt.Println("")

	// --- Start Services ---
	klog.Info("=== Starting Services ===")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start Gateway
	if err := gw.Start(ctx); err != nil {
		klog.Fatalf("Failed to start Gateway: %v", err)
	}
	klog.Info("Gateway started")

	// Start MCP Hub
	if err := mcpHub.Start(ctx); err != nil {
		klog.Fatalf("Failed to start MCP Hub: %v", err)
	}
	klog.Info("MCP Hub started")

	// Start Port Proxy
	if err := portProxy.Start(ctx); err != nil {
		klog.Fatalf("Failed to start Port Proxy: %v", err)
	}
	klog.Info("Port Proxy started")

	// Start Egress Gateway
	if egressGW != nil {
		if err := egressGW.Start(ctx); err != nil {
			klog.Fatalf("Failed to start Egress Gateway: %v", err)
		}
		klog.Info("Egress Gateway started")
	}

	// Start Network Policy Engine
	netPolicyEngine.Start(ctx)
	klog.Info("Network Policy Engine started")

	fmt.Println("")

	// --- Service Summary ---
	fmt.Println("============================================================")
	fmt.Println("  NexusBox Sandbox - Services Running")
	fmt.Println("============================================================")
	fmt.Printf("  Gateway API:   http://localhost:%d/v1/\n", *port)
	fmt.Printf("  Health Check:  http://localhost:%d/healthz\n", *port)
	fmt.Printf("  MCP Endpoint:  http://localhost:%d/mcp\n", *mcpPort)
	fmt.Printf("  Port Proxy:    http://localhost:%d/proxy/\n", *proxyPort)
	if egressGW != nil {
		fmt.Printf("  Egress GW:     http://localhost:%d/v1/egress/stats\n", *egressPort)
	}
	fmt.Printf("  Workspace:     %s\n", ws)
	fmt.Println("")
	fmt.Println("  API Endpoints:")
	fmt.Println("    POST /v1/shell/exec         - Execute shell command")
	fmt.Println("    POST /v1/shell/sessions     - Create shell session")
	fmt.Println("    POST /v1/file/read          - Read file")
	fmt.Println("    POST /v1/file/write         - Write file")
	fmt.Println("    POST /v1/file/list          - List directory")
	fmt.Println("    POST /v1/browser/navigate   - Navigate browser")
	fmt.Println("    POST /v1/browser/screenshot - Take screenshot")
	fmt.Println("    POST /v1/code/execute       - Execute code")
	fmt.Println("    GET  /v1/system/env         - System environment")
	fmt.Println("    GET  /v1/templates           - List sandbox templates")
	fmt.Println("    POST /e2b/v1/sandboxes      - E2B SDK: create sandbox")
	fmt.Println("    GET  /e2b/v1/health         - E2B SDK: health check")
	fmt.Println("")
	fmt.Println("  Press Ctrl+C to stop")
	fmt.Println("============================================================")
	fmt.Println("")

	// --- Wait for shutdown ---
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		klog.Infof("Received signal %v, shutting down...", sig)
	case <-ctx.Done():
		klog.Info("Context cancelled, shutting down...")
	}

	// Graceful shutdown
	fmt.Println("")
	klog.Info("=== Shutting Down ===")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	gw.Shutdown()
	portProxy.Shutdown()
	if egressGW != nil {
		egressGW.Stop()
	}
	netPolicyEngine.Stop()
	_ = shutdownCtx

	klog.Info("All services stopped. Goodbye!")
}
