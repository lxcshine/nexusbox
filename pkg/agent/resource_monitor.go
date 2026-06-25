package agent

import (
	"context"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

// ResourceMonitor monitors node and sandbox resource usage.
// It collects metrics at regular intervals and provides
// real-time resource information for scheduling decisions.
type ResourceMonitor struct {
	mu sync.RWMutex

	// interval is how often to collect metrics.
	interval time.Duration

	// nodeResources holds the current node resource information.
	nodeResources *NodeResourceInfo

	// sandboxMetrics maps sandbox key to its metrics.
	sandboxMetrics map[string]*SandboxMetrics

	// memoryStats holds memory statistics.
	memoryStats *MemoryStats

	// diskStats holds disk statistics.
	diskStats *DiskStats

	// cpuStats holds CPU statistics.
	cpuStats *CPUStats

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// NodeResourceInfo holds node resource information.
type NodeResourceInfo struct {
	Capacity    *NodeResources
	Allocatable *NodeResources
	Allocated   *NodeResources
	Available   *NodeResources
}

// SandboxMetrics holds metrics for a single sandbox.
type SandboxMetrics struct {
	Key         string
	CPUUsage    float64 // CPU usage in cores
	MemoryUsage uint64  // Memory usage in bytes
	DiskUsage   uint64  // Disk usage in bytes
	NetworkRx   uint64  // Network received bytes
	NetworkTx   uint64  // Network transmitted bytes
	CollectedAt time.Time
}

// MemoryStats holds memory statistics.
type MemoryStats struct {
	Total        uint64
	Used         uint64
	Available    uint64
	UsagePercent float64
}

// DiskStats holds disk statistics.
type DiskStats struct {
	Total        uint64
	Used         uint64
	Available    uint64
	UsagePercent float64
}

// CPUStats holds CPU statistics.
type CPUStats struct {
	UsagePercent float64
	CoreCount    int
	LoadAvg1m    float64
	LoadAvg5m    float64
	LoadAvg15m   float64
}

// NewResourceMonitor creates a new ResourceMonitor.
func NewResourceMonitor(interval time.Duration) *ResourceMonitor {
	rm := &ResourceMonitor{
		interval:       interval,
		sandboxMetrics: make(map[string]*SandboxMetrics),
		memoryStats:    &MemoryStats{},
		diskStats:      &DiskStats{},
		cpuStats:       &CPUStats{},
		stopCh:         make(chan struct{}),
	}

	// Initialize with default values
	rm.nodeResources = &NodeResourceInfo{
		Capacity: &NodeResources{
			CPU:              4000,
			Memory:           16 * 1024 * 1024 * 1024,
			GPU:              0,
			EphemeralStorage: 100 * 1024 * 1024 * 1024,
			PersistentStorage: 500 * 1024 * 1024 * 1024,
			Pods:             100,
		},
		Allocatable: &NodeResources{
			CPU:              3800,
			Memory:           14 * 1024 * 1024 * 1024,
			GPU:              0,
			EphemeralStorage: 90 * 1024 * 1024 * 1024,
			PersistentStorage: 450 * 1024 * 1024 * 1024,
			Pods:             95,
		},
		Allocated: &NodeResources{},
		Available: &NodeResources{
			CPU:              3800,
			Memory:           14 * 1024 * 1024 * 1024,
			GPU:              0,
			EphemeralStorage: 90 * 1024 * 1024 * 1024,
			PersistentStorage: 450 * 1024 * 1024 * 1024,
			Pods:             95,
		},
	}

	return rm
}

// Start starts the resource monitor.
func (rm *ResourceMonitor) Start(ctx context.Context) {
	klog.Info("Starting resource monitor")

	go wait.Until(rm.collectMetrics, rm.interval, rm.stopCh)

	klog.Info("Resource monitor started")
}

// Stop stops the resource monitor.
func (rm *ResourceMonitor) Stop() {
	klog.Info("Stopping resource monitor")
	close(rm.stopCh)
}

// collectMetrics collects resource metrics.
func (rm *ResourceMonitor) collectMetrics() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Collect memory stats
	rm.collectMemoryStats()

	// Collect CPU stats
	rm.collectCPUStats()

	// Collect disk stats
	rm.collectDiskStats()

	// Update node resources
	rm.updateNodeResources()

	klog.V(6).Info("Collected resource metrics")
}

// collectMemoryStats collects memory statistics.
func (rm *ResourceMonitor) collectMemoryStats() {
	// In production, read from /proc/meminfo or use cgroup stats
	// For now, use simulated values
	rm.memoryStats = &MemoryStats{
		Total:        16 * 1024 * 1024 * 1024,
		Used:         8 * 1024 * 1024 * 1024,
		Available:    8 * 1024 * 1024 * 1024,
		UsagePercent: 50.0,
	}
}

// collectCPUStats collects CPU statistics.
func (rm *ResourceMonitor) collectCPUStats() {
	// In production, read from /proc/stat or use cgroup stats
	rm.cpuStats = &CPUStats{
		UsagePercent: 35.0,
		CoreCount:    4,
		LoadAvg1m:    1.2,
		LoadAvg5m:    1.0,
		LoadAvg15m:   0.8,
	}
}

// collectDiskStats collects disk statistics.
func (rm *ResourceMonitor) collectDiskStats() {
	// In production, read from df or use filesystem stats
	rm.diskStats = &DiskStats{
		Total:        100 * 1024 * 1024 * 1024,
		Used:         40 * 1024 * 1024 * 1024,
		Available:    60 * 1024 * 1024 * 1024,
		UsagePercent: 40.0,
	}
}

// updateNodeResources updates the node resource information.
func (rm *ResourceMonitor) updateNodeResources() {
	// Recalculate available resources
	if rm.nodeResources.Allocatable != nil && rm.nodeResources.Allocated != nil {
		rm.nodeResources.Available = &NodeResources{
			CPU:              rm.nodeResources.Allocatable.CPU - rm.nodeResources.Allocated.CPU,
			Memory:           rm.nodeResources.Allocatable.Memory - rm.nodeResources.Allocated.Memory,
			GPU:              rm.nodeResources.Allocatable.GPU - rm.nodeResources.Allocated.GPU,
			EphemeralStorage: rm.nodeResources.Allocatable.EphemeralStorage - rm.nodeResources.Allocated.EphemeralStorage,
			PersistentStorage: rm.nodeResources.Allocatable.PersistentStorage - rm.nodeResources.Allocated.PersistentStorage,
			Pods:             rm.nodeResources.Allocatable.Pods - rm.nodeResources.Allocated.Pods,
		}
	}
}

// GetNodeResources returns the current node resource information.
func (rm *ResourceMonitor) GetNodeResources() *NodeResourceInfo {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	copy := *rm.nodeResources
	return &copy
}

// GetMemoryStats returns the current memory statistics.
func (rm *ResourceMonitor) GetMemoryStats() *MemoryStats {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	copy := *rm.memoryStats
	return &copy
}

// GetDiskStats returns the current disk statistics.
func (rm *ResourceMonitor) GetDiskStats() *DiskStats {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	copy := *rm.diskStats
	return &copy
}

// GetCPUStats returns the current CPU statistics.
func (rm *ResourceMonitor) GetCPUStats() *CPUStats {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	copy := *rm.cpuStats
	return &copy
}

// GetMetrics returns all collected metrics.
func (rm *ResourceMonitor) GetMetrics() map[string]interface{} {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	metrics := map[string]interface{}{
		"memory": rm.memoryStats,
		"cpu":    rm.cpuStats,
		"disk":   rm.diskStats,
		"node":   rm.nodeResources,
	}

	return metrics
}

// UpdateSandboxMetrics updates metrics for a specific sandbox.
func (rm *ResourceMonitor) UpdateSandboxMetrics(key string, metrics *SandboxMetrics) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	metrics.CollectedAt = time.Now()
	rm.sandboxMetrics[key] = metrics
}

// GetSandboxMetrics returns metrics for a specific sandbox.
func (rm *ResourceMonitor) GetSandboxMetrics(key string) (*SandboxMetrics, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	metrics, exists := rm.sandboxMetrics[key]
	if !exists {
		return nil, false
	}

	copy := *metrics
	return &copy, true
}

// DeleteSandboxMetrics removes metrics for a specific sandbox.
func (rm *ResourceMonitor) DeleteSandboxMetrics(key string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	delete(rm.sandboxMetrics, key)
}

// AllocateResources allocates resources on this node.
func (rm *ResourceMonitor) AllocateResources(cpu int64, memory int64, gpu int64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.nodeResources.Allocated.CPU += cpu
	rm.nodeResources.Allocated.Memory += memory
	rm.nodeResources.Allocated.GPU += gpu
	rm.nodeResources.Allocated.Pods++

	rm.updateNodeResources()
}

// ReleaseResources releases resources on this node.
func (rm *ResourceMonitor) ReleaseResources(cpu int64, memory int64, gpu int64) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.nodeResources.Allocated.CPU -= cpu
	rm.nodeResources.Allocated.Memory -= memory
	rm.nodeResources.Allocated.GPU -= gpu
	rm.nodeResources.Allocated.Pods--

	// Ensure no negative values
	if rm.nodeResources.Allocated.CPU < 0 {
		rm.nodeResources.Allocated.CPU = 0
	}
	if rm.nodeResources.Allocated.Memory < 0 {
		rm.nodeResources.Allocated.Memory = 0
	}
	if rm.nodeResources.Allocated.GPU < 0 {
		rm.nodeResources.Allocated.GPU = 0
	}
	if rm.nodeResources.Allocated.Pods < 0 {
		rm.nodeResources.Allocated.Pods = 0
	}

	rm.updateNodeResources()
}
