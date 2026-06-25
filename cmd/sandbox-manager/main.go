package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/controller"
	"github.com/nexusbox/nexusbox/pkg/metrics"
	"github.com/nexusbox/nexusbox/pkg/sandbox/lifecycle"
	"github.com/nexusbox/nexusbox/pkg/sandbox/runtime"
	"github.com/nexusbox/nexusbox/pkg/scheduler"
	"github.com/nexusbox/nexusbox/pkg/scheduler/framework"
	"github.com/nexusbox/nexusbox/pkg/scheduler/plugins"
	"github.com/nexusbox/nexusbox/pkg/scheduler/queue"
	"github.com/nexusbox/nexusbox/pkg/tenant"
	"github.com/nexusbox/nexusbox/pkg/tenant/quota"
)

// SandboxManager is the main entry point for the sandbox management system.
// It orchestrates all components: controllers, scheduler, lifecycle manager,
// runtime manager, and tenant manager.
func main() {
	// Parse command line flags
	var (
		_                = flag.Int("port", 8080, "HTTP server port")
		_                = flag.Int("metrics-port", 9090, "Metrics server port")
		_                = flag.Int("workers", 5, "Number of controller workers")
		_                = flag.Int("scheduler-workers", 3, "Number of scheduler workers")
		enableScheduler   = flag.Bool("enable-scheduler", true, "Enable the scheduler")
		enableController  = flag.Bool("enable-controller", true, "Enable the controllers")
		kataEndpoint      = flag.String("kata-endpoint", "/run/kata-containers/containerd/kata.sock", "Kata Containers endpoint")
		gvisorEndpoint    = flag.String("gvisor-endpoint", "/run/containerd/runsc.sock", "gVisor endpoint")
		runcEndpoint      = flag.String("runc-endpoint", "/run/containerd/containerd.sock", "runc endpoint")
		poolEnabled       = flag.Bool("pool-enabled", true, "Enable sandbox pooling")
	)
	klog.InitFlags(nil)
	flag.Parse()

	klog.Info("Starting NexusBox Sandbox Manager")
	klog.Info("===============================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Initialize metrics collector
	metricsCollector := metrics.NewMetricsCollector(metrics.DefaultMetricsConfig())
	metricsCollector.Start(ctx)

	// Initialize quota manager
	quotaManager := quota.NewQuotaManager()

	// Initialize tenant manager
	tenantManager := tenant.NewTenantManager(nil, nil, nil)

	// Initialize runtime manager
	runtimeConfig := runtime.DefaultRuntimeManagerConfig()
	runtimeConfig.KataContainersEndpoint = *kataEndpoint
	runtimeConfig.GVisorEndpoint = *gvisorEndpoint
	runtimeConfig.RuncEndpoint = *runcEndpoint
	runtimeConfig.PoolEnabled = *poolEnabled

	runtimeManager := runtime.NewRuntimeManager(runtimeConfig)
	runtimeManager.Start(ctx)

	// Initialize lifecycle manager
	lifecycleManager := lifecycle.NewLifecycleManager(
		runtimeManager,
		tenantManager,
		nil, // informer will be set up later
		nil, // event recorder
	)
	lifecycleManager.Start(ctx)

	// Initialize controllers
	if *enableController {
		sandboxController := controller.NewSandboxController(
			lifecycleManager,
			runtimeManager,
			tenantManager,
			nil, // informer
			nil, // event recorder
		)
		sandboxController.Start(ctx)

		tenantController := controller.NewTenantController(
			tenantManager,
			quotaManager,
			nil, // informer
			nil, // event recorder
		)
		tenantController.Start(ctx)

		quotaController := controller.NewQuotaController(
			quotaManager,
			tenantManager,
			nil, // informer
			nil, // event recorder
		)
		quotaController.Start(ctx)

		klog.Info("Controllers started")
	}

	// Initialize scheduler
	if *enableScheduler {
		// Create scheduling plugins
		schedulingPlugins := []framework.Plugin{
			plugins.NewResourceFit(),
			plugins.NewNodeResourcesFit(),
			plugins.NewTenantAffinity(),
			plugins.NewTenantIsolation(),
			plugins.NewBatchScheduling(nil),
			plugins.NewPrioritySort(),
			plugins.NewRuntimeCompatibility(),
			plugins.NewNodeResourcesBalancedAllocation(),
			plugins.NewImageLocality(),
			plugins.NewDefaultBinder(),
			plugins.NewDefaultPreBinder(),
			plugins.NewDefaultReserve(),
			plugins.NewDefaultPostBinder(),
		}

		// Create scheduling framework
		fwk := framework.NewFramework(&framework.FrameworkConfig{
			Plugins: &framework.Plugins{
				PreFilter: []framework.PluginSpec{
					{Name: "ResourceFit"},
					{Name: "PrioritySort"},
				},
				Filter: []framework.PluginSpec{
					{Name: "ResourceFit"},
					{Name: "TenantAffinity"},
					{Name: "TenantIsolation"},
					{Name: "RuntimeCompatibility"},
				},
				Score: []framework.PluginSpec{
					{Name: "NodeResourcesFit", Weight: 1},
					{Name: "NodeResourcesBalancedAllocation", Weight: 1},
					{Name: "ImageLocality", Weight: 1},
				},
				Reserve: []framework.PluginSpec{
					{Name: "DefaultReserve"},
				},
				Permit: []framework.PluginSpec{
					{Name: "BatchScheduling"},
				},
				PreBind: []framework.PluginSpec{
					{Name: "DefaultPreBinder"},
				},
				Bind: []framework.PluginSpec{
					{Name: "DefaultBinder"},
				},
				PostBind: []framework.PluginSpec{
					{Name: "DefaultPostBinder"},
				},
			},
		}, schedulingPlugins...)

		// Create scheduling queue
		schedulingQueue := queue.NewPriorityQueue()
		schedulingQueue.Run()

		// Create scheduler
		sched := scheduler.NewScheduler(fwk, schedulingQueue, nil, 5)
		sched.Start(ctx)

		klog.Info("Scheduler started")
	}

	klog.Info("Sandbox Manager is ready")

	// Wait for shutdown signal
	select {
	case sig := <-sigCh:
		klog.Infof("Received signal %v, shutting down", sig)
	case <-ctx.Done():
		klog.Info("Context cancelled, shutting down")
	}

	// Graceful shutdown
	klog.Info("Shutting down components...")

	lifecycleManager.Stop()
	runtimeManager.Stop()
	metricsCollector.Stop()

	klog.Info("Sandbox Manager stopped")
	fmt.Println("NexusBox Sandbox Manager exited cleanly")
}
