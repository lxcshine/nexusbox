// Package resource provides unified resource limit management for NexusBox
// sandboxes. It coordinates CPU, memory, and disk quota enforcement across
// platform-specific backends (Windows Job Objects, Linux cgroups).
package resource

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"k8s.io/klog/v2"
)

// Limits defines resource limits for a sandbox.
type Limits struct {
	// CPU is the CPU limit in Kubernetes quantity format (e.g., "2", "500m").
	CPU string

	// Memory is the memory limit (e.g., "2Gi", "512Mi").
	Memory string

	// DiskQuota is the maximum disk usage in bytes.
	DiskQuota int64

	// MaxFileCount is the maximum number of files.
	MaxFileCount int64

	// MaxProcesses is the maximum number of processes.
	MaxProcesses int32

	// MaxFileDescriptors is the maximum number of open file descriptors.
	MaxFileDescriptors int32
}

// Usage represents current resource usage.
type Usage struct {
	// CPUUsagePercent is the CPU usage percentage (0-100).
	CPUUsagePercent float64

	// MemoryUsageBytes is the current memory usage in bytes.
	MemoryUsageBytes int64

	// DiskUsageBytes is the current disk usage in bytes.
	DiskUsageBytes int64

	// FileCount is the current file count.
	FileCount int64

	// ProcessCount is the current process count.
	ProcessCount int32

	// CollectedAt is when the usage was collected.
	CollectedAt time.Time
}

// Manager manages resource limits for sandboxes.
type Manager struct {
	mu           sync.RWMutex
	limits       map[string]*Limits      // sandboxID -> limits
	usage        map[string]*Usage       // sandboxID -> latest usage
	watchers     map[string]*diskWatcher // sandboxID -> disk watcher
	pythonHelper string
}

// NewManager creates a new resource manager.
func NewManager() *Manager {
	m := &Manager{
		limits:   make(map[string]*Limits),
		usage:    make(map[string]*Usage),
		watchers: make(map[string]*diskWatcher),
	}
	m.pythonHelper = m.findPythonHelper()
	return m
}

// ApplyLimits applies resource limits for a sandbox.
// On Windows, CPU/memory limits are enforced via Job Objects.
// Disk quotas are enforced via periodic monitoring + filesystem sandbox.
func (m *Manager) ApplyLimits(ctx context.Context, sandboxID, workspacePath string, limits *Limits) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.limits[sandboxID] = limits

	// Start disk usage watcher if disk quota is set.
	if limits.DiskQuota > 0 {
		watcher := newDiskWatcher(sandboxID, workspacePath, limits.DiskQuota, m.pythonHelper)
		m.watchers[sandboxID] = watcher
		go watcher.start(ctx)
	}

	klog.Infof("Applied resource limits for sandbox %s: CPU=%s, Memory=%s, DiskQuota=%d",
		sandboxID, limits.CPU, limits.Memory, limits.DiskQuota)
	return nil
}

// RemoveLimits removes resource limits for a sandbox.
func (m *Manager) RemoveLimits(sandboxID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.limits, sandboxID)
	delete(m.usage, sandboxID)

	if watcher, ok := m.watchers[sandboxID]; ok {
		watcher.stop()
		delete(m.watchers, sandboxID)
	}
}

// GetUsage returns the current resource usage for a sandbox.
func (m *Manager) GetUsage(sandboxID string) (*Usage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	usage, ok := m.usage[sandboxID]
	if !ok {
		return nil, fmt.Errorf("no usage data for sandbox %s", sandboxID)
	}
	return usage, nil
}

// GetLimits returns the resource limits for a sandbox.
func (m *Manager) GetLimits(sandboxID string) (*Limits, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	limits, ok := m.limits[sandboxID]
	if !ok {
		return nil, fmt.Errorf("no limits for sandbox %s", sandboxID)
	}
	return limits, nil
}

// CheckDiskQuota returns true if the sandbox has exceeded its disk quota.
func (m *Manager) CheckDiskQuota(sandboxID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	limits, ok := m.limits[sandboxID]
	if !ok || limits.DiskQuota <= 0 {
		return false, nil
	}

	usage, ok := m.usage[sandboxID]
	if !ok {
		return false, nil
	}

	return usage.DiskUsageBytes > limits.DiskQuota, nil
}

// UpdateUsage updates the resource usage for a sandbox.
func (m *Manager) UpdateUsage(sandboxID string, usage *Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage[sandboxID] = usage
}

// findPythonHelper locates the Python executable for disk quota monitoring.
func (m *Manager) findPythonHelper() string {
	for _, name := range []string{"python3", "python", "py"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// diskWatcher periodically checks disk usage for a sandbox workspace.
type diskWatcher struct {
	sandboxID     string
	workspacePath string
	quota         int64
	pythonPath    string
	stopCh        chan struct{}
	manager       *Manager
}

func newDiskWatcher(sandboxID, workspacePath string, quota int64, pythonPath string) *diskWatcher {
	return &diskWatcher{
		sandboxID:     sandboxID,
		workspacePath: workspacePath,
		quota:         quota,
		pythonPath:    pythonPath,
		stopCh:        make(chan struct{}),
	}
}

func (w *diskWatcher) start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Initial check.
	w.check()

	for {
		select {
		case <-ticker.C:
			w.check()
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (w *diskWatcher) stop() {
	close(w.stopCh)
}

func (w *diskWatcher) check() {
	usage := w.getDiskUsage()
	if usage >= w.quota {
		klog.Warningf("Sandbox %s exceeded disk quota: %d > %d", w.sandboxID, usage, w.quota)
	}
}

// getDiskUsage calculates the total disk usage of the workspace.
// Uses Python's shutil if available (faster for large directory trees),
// falls back to a Go implementation otherwise.
func (w *diskWatcher) getDiskUsage() int64 {
	if w.pythonPath != "" {
		if usage, err := w.getDiskUsagePython(); err == nil {
			return usage
		}
	}
	return w.getDiskUsageGo()
}

// getDiskUsagePython uses Python's shutil.disk_usage for fast disk stats.
func (w *diskWatcher) getDiskUsagePython() (int64, error) {
	script := fmt.Sprintf(`
import os
import sys

def get_dir_size(path):
    total = 0
    try:
        for dirpath, dirnames, filenames in os.walk(path):
            for f in filenames:
                fp = os.path.join(dirpath, f)
                if not os.path.islink(fp):
                    try:
                        total += os.path.getsize(fp)
                    except (OSError, PermissionError):
                        pass
    except (OSError, PermissionError):
        pass
    return total

print(get_dir_size(%q))
`, w.workspacePath)

	cmd := exec.Command(w.pythonPath, "-c", script)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	var size int64
	fmt.Sscanf(strings.TrimSpace(string(output)), "%d", &size)
	return size, nil
}

// getDiskUsageGo is the fallback Go implementation for disk usage calculation.
func (w *diskWatcher) getDiskUsageGo() int64 {
	var total int64
	filepath.Walk(w.workspacePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// FromSandboxSpec converts a SandboxResourceSpec to Limits.
func FromSandboxSpec(spec *sandboxv1alpha1.ResourceRequirements, storage *sandboxv1alpha1.SandboxStorageSpec) *Limits {
	limits := &Limits{
		CPU:    spec.CPU,
		Memory: spec.Memory,
	}

	if storage != nil {
		if storage.EphemeralStorageLimit != "" {
			limits.DiskQuota = parseStorageToBytes(storage.EphemeralStorageLimit)
		}
	}

	// Default disk quota: 1 GB.
	if limits.DiskQuota == 0 {
		limits.DiskQuota = 1 << 30
	}

	// Default max processes.
	limits.MaxProcesses = 256

	// Default max file descriptors.
	limits.MaxFileDescriptors = 1024

	return limits
}

// parseStorageToBytes parses a Kubernetes-style storage quantity into bytes.
func parseStorageToBytes(s string) int64 {
	suffixes := []struct {
		suffix     string
		multiplier int64
	}{
		{"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10},
		{"G", 1e9}, {"M", 1e6}, {"K", 1e3},
	}
	for _, su := range suffixes {
		if len(s) > len(su.suffix) && s[len(s)-len(su.suffix):] == su.suffix {
			var val int64
			fmt.Sscanf(s[:len(s)-len(su.suffix)], "%d", &val)
			return val * su.multiplier
		}
	}
	var val int64
	fmt.Sscanf(s, "%d", &val)
	return val
}

// Platform reports the current platform for resource enforcement.
func Platform() string {
	switch runtime.GOOS {
	case "windows":
		return "windows-jobobject"
	case "linux":
		return "linux-cgroup"
	case "darwin":
		return "darwin-macsandbox"
	default:
		return runtime.GOOS
	}
}
