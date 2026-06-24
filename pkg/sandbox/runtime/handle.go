/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"k8s.io/klog/v2"
)

// baseRuntimeHandle provides a base implementation of RuntimeHandle.
type baseRuntimeHandle struct {
	mu sync.RWMutex

	id          string
	runtimeType string
	ready       bool
	pid         int
	createdAt   time.Time
	spec        *RuntimeSpec

	// exitCh is closed when the runtime exits.
	exitCh chan int
}

// ID returns the unique identifier of the runtime.
func (h *baseRuntimeHandle) ID() string {
	return h.id
}

// RuntimeType returns the type of the runtime.
func (h *baseRuntimeHandle) RuntimeType() string {
	return h.runtimeType
}

// IsReady returns whether the runtime is ready.
func (h *baseRuntimeHandle) IsReady() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.ready
}

// PID returns the process ID of the runtime.
func (h *baseRuntimeHandle) PID() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.pid
}

// CreatedAt returns the creation time.
func (h *baseRuntimeHandle) CreatedAt() time.Time {
	return h.createdAt
}

// Stop stops the runtime.
func (h *baseRuntimeHandle) Stop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.ready {
		return nil
	}

	// Send SIGTERM
	if h.pid > 0 {
		if proc, err := os.FindProcess(h.pid); err == nil {
			_ = proc.Signal(syscall.Signal(0xf)) // SIGTERM
		}
	}

	h.ready = false
	return nil
}

// Pause pauses the runtime.
func (h *baseRuntimeHandle) Pause(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.ready {
		return fmt.Errorf("runtime %s is not running", h.id)
	}

	// Pause is not supported on Windows
	if h.pid > 0 {
		if proc, err := os.FindProcess(h.pid); err == nil {
			_ = proc.Signal(syscall.Signal(0x13)) // SIGSTOP on Linux
		}
	}

	klog.Infof("Paused runtime %s", h.id)
	return nil
}

// Resume resumes the runtime.
func (h *baseRuntimeHandle) Resume(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.ready {
		return fmt.Errorf("runtime %s is not running", h.id)
	}

	// Resume is not supported on Windows
	if h.pid > 0 {
		if proc, err := os.FindProcess(h.pid); err == nil {
			_ = proc.Signal(syscall.Signal(0x12)) // SIGCONT on Linux
		}
	}

	klog.Infof("Resumed runtime %s", h.id)
	return nil
}

// Stats returns runtime statistics.
func (h *baseRuntimeHandle) Stats(ctx context.Context) (*RuntimeStats, error) {
	stats := &RuntimeStats{
		CollectedAt: time.Now(),
	}

	// Read from cgroup if available
	if h.pid > 0 {
		h.collectCgroupStats(stats)
	}

	return stats, nil
}

// Wait waits for the runtime to exit.
func (h *baseRuntimeHandle) Wait(ctx context.Context) (int, error) {
	select {
	case exitCode := <-h.exitCh:
		return exitCode, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

// collectCgroupStats collects stats from cgroup.
func (h *baseRuntimeHandle) collectCgroupStats(stats *RuntimeStats) {
	// Read CPU stats from cgroup
	cpuPath := fmt.Sprintf("/proc/%d/stat", h.pid)
	if data, err := os.ReadFile(cpuPath); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 13 {
			// utime + stime (fields 13, 14 in /proc/pid/stat, 0-indexed: 12, 13)
			// These are in clock ticks, convert to nanoseconds
			var utime, stime uint64
			fmt.Sscanf(fields[13], "%d", &utime)
			fmt.Sscanf(fields[14], "%d", &stime)
			stats.CPUUsageNanoCores = (utime + stime) * uint64(time.Millisecond/time.Duration(100))
		}
	}

	// Read memory stats from cgroup
	memPath := fmt.Sprintf("/proc/%d/status", h.pid)
	if data, err := os.ReadFile(memPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				var kb uint64
				fmt.Sscanf(strings.TrimPrefix(line, "VmRSS:"), "%d", &kb)
				stats.MemoryUsageBytes = kb * 1024
			} else if strings.HasPrefix(line, "VmSize:") {
				var kb uint64
				fmt.Sscanf(strings.TrimPrefix(line, "VmSize:"), "%d", &kb)
				stats.MemoryWorkingSetBytes = kb * 1024
			}
		}
	}
}

// kataBaseHandle is a base RuntimeHandle for Kata Containers.
type kataBaseHandle struct {
	baseRuntimeHandle

	// vmPID is the PID of the VM process.
	vmPID int

	// bundlePath is the path to the OCI bundle.
	bundlePath string

	// containerID is the container ID.
	containerID string
}

// Stop stops the Kata Containers runtime.
func (h *kataBaseHandle) Stop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.ready {
		return nil
	}

	// Use kata-runtime to kill the container
	cmd := exec.CommandContext(ctx, "kata-runtime", "kill", h.containerID, "--signal", "SIGTERM")
	if err := cmd.Run(); err != nil {
		klog.Warningf("Failed to kill kata container %s: %v", h.containerID, err)
	}

	// Wait for the container to stop
	time.Sleep(100 * time.Millisecond)

	// Delete the container
	cmd = exec.CommandContext(ctx, "kata-runtime", "delete", h.containerID)
	if err := cmd.Run(); err != nil {
		klog.Warningf("Failed to delete kata container %s: %v", h.containerID, err)
	}

	h.ready = false
	klog.Infof("Stopped Kata Containers runtime %s", h.containerID)
	return nil
}

// Pause pauses the Kata Containers runtime.
func (h *kataBaseHandle) Pause(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "kata-runtime", "pause", h.containerID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pause kata container %s: %w", h.containerID, err)
	}
	klog.Infof("Paused Kata Containers runtime %s", h.containerID)
	return nil
}

// Resume resumes the Kata Containers runtime.
func (h *kataBaseHandle) Resume(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "kata-runtime", "resume", h.containerID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to resume kata container %s: %w", h.containerID, err)
	}
	klog.Infof("Resumed Kata Containers runtime %s", h.containerID)
	return nil
}

// gvisorBaseHandle is a RuntimeHandle for gVisor.
type gvisorBaseHandle struct {
	baseRuntimeHandle

	// containerID is the container ID.
	containerID string
}

// Stop stops the gVisor runtime.
func (h *gvisorBaseHandle) Stop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.ready {
		return nil
	}

	// Use runsc to kill the container
	cmd := exec.CommandContext(ctx, "runsc", "kill", h.containerID, "SIGTERM")
	if err := cmd.Run(); err != nil {
		klog.Warningf("Failed to kill gVisor container %s: %v", h.containerID, err)
	}

	// Wait and delete
	time.Sleep(100 * time.Millisecond)
	cmd = exec.CommandContext(ctx, "runsc", "delete", h.containerID)
	if err := cmd.Run(); err != nil {
		klog.Warningf("Failed to delete gVisor container %s: %v", h.containerID, err)
	}

	h.ready = false
	klog.Infof("Stopped gVisor runtime %s", h.containerID)
	return nil
}

// runcBaseHandle is a RuntimeHandle for runc.
type runcBaseHandle struct {
	baseRuntimeHandle

	// containerID is the container ID.
	containerID string

	// bundlePath is the path to the OCI bundle.
	bundlePath string
}

// Stop stops the runc runtime.
func (h *runcBaseHandle) Stop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.ready {
		return nil
	}

	// Use runc to kill the container
	cmd := exec.CommandContext(ctx, "runc", "kill", h.containerID, "SIGTERM")
	if err := cmd.Run(); err != nil {
		klog.Warningf("Failed to kill runc container %s: %v", h.containerID, err)
	}

	// Wait and delete
	time.Sleep(100 * time.Millisecond)
	cmd = exec.CommandContext(ctx, "runc", "delete", h.containerID)
	if err := cmd.Run(); err != nil {
		klog.Warningf("Failed to delete runc container %s: %v", h.containerID, err)
	}

	h.ready = false
	klog.Infof("Stopped runc runtime %s", h.containerID)
	return nil
}

// HealthChecker performs health checks on runtime providers.
type HealthChecker struct {
	mu sync.RWMutex

	// interval is how often to perform health checks.
	interval time.Duration

	// providers maps runtime type to its health status.
	providers map[string]*ProviderHealth

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// ProviderHealth holds health information for a runtime provider.
type ProviderHealth struct {
	// Type is the runtime type.
	Type string

	// Healthy indicates whether the provider is healthy.
	Healthy bool

	// LastCheck is the last time a health check was performed.
	LastCheck time.Time

	// ConsecutiveFailures is the number of consecutive health check failures.
	ConsecutiveFailures int

	// Message is a human-readable health message.
	Message string
}

// NewHealthChecker creates a new HealthChecker.
func NewHealthChecker(interval time.Duration) *HealthChecker {
	return &HealthChecker{
		interval: interval,
		providers: map[string]*ProviderHealth{
			"kata-containers": {Type: "kata-containers", Healthy: false},
			"gvisor":          {Type: "gvisor", Healthy: false},
			"runc":            {Type: "runc", Healthy: false},
		},
		stopCh: make(chan struct{}),
	}
}

// Start starts the health checker.
func (hc *HealthChecker) Start(ctx context.Context) {
	go hc.run(ctx)
	klog.Info("Runtime health checker started")
}

// Stop stops the health checker.
func (hc *HealthChecker) Stop() {
	close(hc.stopCh)
}

// run is the main health check loop.
func (hc *HealthChecker) run(ctx context.Context) {
	ticker := time.NewTicker(hc.interval)
	defer ticker.Stop()

	// Initial check
	hc.checkAll()

	for {
		select {
		case <-ticker.C:
			hc.checkAll()
		case <-hc.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// checkAll performs health checks on all providers.
func (hc *HealthChecker) checkAll() {
	for runtimeType := range hc.providers {
		hc.checkProvider(runtimeType)
	}
}

// checkProvider performs a health check on a specific provider.
func (hc *HealthChecker) checkProvider(runtimeType string) {
	hc.mu.Lock()
	defer hc.mu.Unlock()

	health := hc.providers[runtimeType]
	health.LastCheck = time.Now()

	var cmd *exec.Cmd
	switch runtimeType {
	case "kata-containers":
		cmd = exec.Command("kata-runtime", "check")
	case "gvisor":
		cmd = exec.Command("runsc", "--version")
	case "runc":
		cmd = exec.Command("runc", "--version")
	default:
		health.Healthy = false
		health.Message = fmt.Sprintf("Unknown runtime type: %s", runtimeType)
		return
	}

	if err := cmd.Run(); err != nil {
		health.Healthy = false
		health.ConsecutiveFailures++
		health.Message = fmt.Sprintf("Health check failed: %v", err)

		if health.ConsecutiveFailures == 1 {
			klog.Warningf("Runtime provider %s health check failed: %v", runtimeType, err)
		}
	} else {
		health.Healthy = true
		health.ConsecutiveFailures = 0
		health.Message = "OK"

		if !health.Healthy {
			klog.Infof("Runtime provider %s is healthy again", runtimeType)
		}
	}
}

// IsHealthy returns whether a runtime provider is healthy.
func (hc *HealthChecker) IsHealthy(runtimeType string) bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	if health, exists := hc.providers[runtimeType]; exists {
		return health.Healthy
	}
	return false
}

// GetHealth returns the health status of a runtime provider.
func (hc *HealthChecker) GetHealth(runtimeType string) *ProviderHealth {
	hc.mu.RLock()
	defer hc.mu.RUnlock()
	if health, exists := hc.providers[runtimeType]; exists {
		return health
	}
	return nil
}

// GarbageCollector cleans up stale runtime resources.
type GarbageCollector struct {
	mu sync.RWMutex

	// runtimeManager is the parent runtime manager.
	runtimeManager *RuntimeManager

	// interval is how often to run garbage collection.
	interval time.Duration

	// maxAge is the maximum age of a stale resource before cleanup.
	maxAge time.Duration

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// NewGarbageCollector creates a new GarbageCollector.
func NewGarbageCollector(rm *RuntimeManager, interval, maxAge time.Duration) *GarbageCollector {
	return &GarbageCollector{
		runtimeManager: rm,
		interval:       interval,
		maxAge:         maxAge,
		stopCh:         make(chan struct{}),
	}
}

// Start starts the garbage collector.
func (gc *GarbageCollector) Start(ctx context.Context) {
	go gc.run(ctx)
	klog.Info("Runtime garbage collector started")
}

// Stop stops the garbage collector.
func (gc *GarbageCollector) Stop() {
	close(gc.stopCh)
}

// run is the main garbage collection loop.
func (gc *GarbageCollector) run(ctx context.Context) {
	ticker := time.NewTicker(gc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			gc.collect()
		case <-gc.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// collect performs garbage collection.
func (gc *GarbageCollector) collect() {
	gc.mu.Lock()
	defer gc.mu.Unlock()

	now := time.Now()
	cleanedCount := 0

	// Clean up stale bundle directories
	bundleDirs := []string{
		"/run/nexusbox/bundles",
		"/tmp/nexusbox/bundles",
	}

	for _, dir := range bundleDirs {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					continue
				}

				if now.Sub(info.ModTime()) > gc.maxAge {
					path := filepath.Join(dir, entry.Name())
					if err := os.RemoveAll(path); err != nil {
						klog.Warningf("Failed to remove stale bundle %s: %v", path, err)
					} else {
						cleanedCount++
						klog.V(4).Infof("Removed stale bundle %s", path)
					}
				}
			}
		}
	}

	// Clean up stale PID files
	pidDirs := []string{
		"/run/nexusbox/pids",
		"/tmp/nexusbox/pids",
	}

	for _, dir := range pidDirs {
		if entries, err := os.ReadDir(dir); err == nil {
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					continue
				}

				if now.Sub(info.ModTime()) > gc.maxAge {
					path := filepath.Join(dir, entry.Name())
					if err := os.RemoveAll(path); err != nil {
						klog.Warningf("Failed to remove stale PID file %s: %v", path, err)
					} else {
						cleanedCount++
						klog.V(4).Infof("Removed stale PID file %s", path)
					}
				}
			}
		}
	}

	if cleanedCount > 0 {
		klog.Infof("Garbage collector cleaned up %d stale resources", cleanedCount)
	}
}
