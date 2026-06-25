package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/klog/v2"

	"github.com/nexusbox/nexusbox/pkg/metrics"
	"github.com/nexusbox/nexusbox/pkg/scheduler"
	"github.com/nexusbox/nexusbox/pkg/scheduler/framework"
	"github.com/nexusbox/nexusbox/pkg/scheduler/plugins"
	"github.com/nexusbox/nexusbox/pkg/scheduler/queue"
)

// SandboxScheduler is the standalone scheduler entry point.
// It can run independently from the manager for scaling purposes.
func main() {
	var (
		_                 = flag.Int("port", 8081, "Scheduler HTTP server port")
		maxAttempts       = flag.Int("max-attempts", 5, "Maximum scheduling attempts")
		workers           = flag.Int("workers", 3, "Number of scheduler workers")
		batchEnabled      = flag.Bool("batch-enabled", true, "Enable batch scheduling")
	)
	klog.InitFlags(nil)
	flag.Parse()

	klog.Info("Starting NexusBox Sandbox Scheduler")
	klog.Info("====================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Initialize metrics
	metricsCollector := metrics.NewMetricsCollector(metrics.DefaultMetricsConfig())
	metricsCollector.Start(ctx)

	// Create scheduling plugins
	schedulingPlugins := []framework.Plugin{
		plugins.NewResourceFit(),
		plugins.NewNodeResourcesFit(),
		plugins.NewTenantAffinity(),
		plugins.NewTenantIsolation(),
		plugins.NewPrioritySort(),
		plugins.NewRuntimeCompatibility(),
		plugins.NewNodeResourcesBalancedAllocation(),
		plugins.NewImageLocality(),
		plugins.NewDefaultBinder(),
		plugins.NewDefaultPreBinder(),
		plugins.NewDefaultReserve(),
		plugins.NewDefaultPostBinder(),
	}

	if *batchEnabled {
		schedulingPlugins = append(schedulingPlugins, plugins.NewBatchScheduling(nil))
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

	// Create batch queue if enabled
	var batchQueue *queue.BatchSchedulingQueue
	if *batchEnabled {
		batchQueue = queue.NewBatchSchedulingQueue()
		batchQueue.Run()
		klog.Info("Batch scheduling queue initialized")
	}

	// Create scheduler
	sched := scheduler.NewScheduler(
		fwk,
		schedulingQueue,
		nil, // informer
		int32(*maxAttempts),
	)
	sched.Start(ctx)

	klog.Infof("Scheduler started with %d workers", *workers)

	// Wait for shutdown signal
	select {
	case sig := <-sigCh:
		klog.Infof("Received signal %v, shutting down", sig)
	case <-ctx.Done():
		klog.Info("Context cancelled, shutting down")
	}

	// Graceful shutdown
	klog.Info("Shutting down scheduler...")
	sched.Stop()
	metricsCollector.Stop()

	klog.Info("Scheduler stopped")
	fmt.Println("NexusBox Sandbox Scheduler exited cleanly")
}
