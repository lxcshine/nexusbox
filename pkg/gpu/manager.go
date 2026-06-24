package gpu

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"k8s.io/klog/v2"
)

// GPUManager manages GPU device allocation for sandboxes.
type GPUManager struct {
	mu      sync.RWMutex
	devices []GPUDevice
	allocs  map[string]*GPUAllocation // sandboxID -> allocation
}

// GPUDevice represents a GPU device on the node.
type GPUDevice struct {
	Index       int
	UUID        string
	Name        string
	MemoryTotal uint64 // bytes
	MemoryUsed  uint64 // bytes
	Path        string // e.g., /dev/nvidia0
}

// GPUAllocation tracks GPU allocation for a sandbox.
type GPUAllocation struct {
	SandboxID   string
	DeviceIDs   []int
	AllocatedAt time.Time
}

// NewGPUManager creates a new GPU manager.
func NewGPUManager() *GPUManager {
	gm := &GPUManager{
		allocs: make(map[string]*GPUAllocation),
	}
	// Discover available GPUs
	gm.discoverDevices()
	return gm
}

// AllocateGPUs allocates GPU devices for a sandbox.
func (gm *GPUManager) AllocateGPUs(ctx context.Context, sandboxID string, spec *sandboxv1alpha1.ResourceRequirements) ([]int, error) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if spec == nil || spec.GPU == "" {
		return nil, nil
	}

	requestedCount := parseGPUCount(spec.GPU)
	if requestedCount == 0 {
		return nil, nil
	}

	// Find available GPUs
	var available []int
	for i := range gm.devices {
		allocated := false
		for _, alloc := range gm.allocs {
			for _, id := range alloc.DeviceIDs {
				if id == i {
					allocated = true
					break
				}
			}
			if allocated {
				break
			}
		}
		if !allocated {
			available = append(available, i)
		}
	}

	if len(available) < requestedCount {
		return nil, fmt.Errorf("insufficient GPUs: requested %d, available %d", requestedCount, len(available))
	}

	// Allocate the first N available GPUs
	allocated := available[:requestedCount]
	gm.allocs[sandboxID] = &GPUAllocation{
		SandboxID:   sandboxID,
		DeviceIDs:   allocated,
		AllocatedAt: time.Now(),
	}

	klog.Infof("Allocated GPU devices %v for sandbox %s", allocated, sandboxID)
	return allocated, nil
}

// ReleaseGPUs releases GPU allocation for a sandbox.
func (gm *GPUManager) ReleaseGPUs(sandboxID string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	if alloc, ok := gm.allocs[sandboxID]; ok {
		klog.Infof("Released GPU devices %v for sandbox %s", alloc.DeviceIDs, sandboxID)
		delete(gm.allocs, sandboxID)
	}
}

// GetAvailableGPUs returns the number of available GPUs.
func (gm *GPUManager) GetAvailableGPUs() int {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	allocated := make(map[int]bool)
	for _, alloc := range gm.allocs {
		for _, id := range alloc.DeviceIDs {
			allocated[id] = true
		}
	}

	available := 0
	for i := range gm.devices {
		if !allocated[i] {
			available++
		}
	}
	return available
}

// GetGPUDevices returns all GPU devices.
func (gm *GPUManager) GetGPUDevices() []GPUDevice {
	gm.mu.RLock()
	defer gm.mu.RUnlock()
	return gm.devices
}

// GetCGPUMetrics returns GPU metrics for monitoring.
func (gm *GPUManager) GetGPUMetrics() map[int]GPUMetrics {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	metrics := make(map[int]GPUMetrics)
	for i, dev := range gm.devices {
		metrics[i] = GPUMetrics{
			DeviceIndex: i,
			Name:        dev.Name,
			MemoryTotal: dev.MemoryTotal,
			MemoryUsed:  dev.MemoryUsed,
		}
	}
	return metrics
}

// GPUMetrics contains GPU usage metrics.
type GPUMetrics struct {
	DeviceIndex int
	Name        string
	MemoryTotal uint64
	MemoryUsed  uint64
	Utilization float64
}

// discoverDevices discovers NVIDIA GPU devices on the node.
func (gm *GPUManager) discoverDevices() {
	// Check for nvidia-smi
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		klog.V(4).Info("nvidia-smi not found, no GPU devices available")
		return
	}

	// Use nvidia-smi to query devices
	output, err := exec.Command("nvidia-smi", "--query-gpu=index,uuid,name,memory.total,memory.used", "--format=csv,noheader,nounits").Output()
	if err != nil {
		klog.Warningf("Failed to query GPU devices: %v", err)
		return
	}

	// Parse output
	lines := parseCSVOutput(string(output))
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		var index int
		var memTotal, memUsed uint64
		fmt.Sscanf(line[0], "%d", &index)
		fmt.Sscanf(line[3], "%d", &memTotal)
		fmt.Sscanf(line[4], "%d", &memUsed)

		gm.devices = append(gm.devices, GPUDevice{
			Index:       index,
			UUID:        line[1],
			Name:        line[2],
			MemoryTotal: memTotal * 1024 * 1024, // MiB to bytes
			MemoryUsed:  memUsed * 1024 * 1024,
			Path:        filepath.Join("/dev", fmt.Sprintf("nvidia%d", index)),
		})
	}

	klog.Infof("Discovered %d GPU devices", len(gm.devices))
}

func parseGPUCount(gpu string) int {
	var count int
	fmt.Sscanf(gpu, "%d", &count)
	return count
}

func parseCSVOutput(output string) [][]string {
	var result [][]string
	lines := splitLines(output)
	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := splitCSV(line)
		result = append(result, fields)
	}
	return result
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			lines = append(lines, line)
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func splitCSV(line string) []string {
	var fields []string
	start := 0
	for i := 0; i < len(line); i++ {
		if line[i] == ',' {
			fields = append(fields, trimSpace(line[start:i]))
			start = i + 1
		}
	}
	fields = append(fields, trimSpace(line[start:]))
	return fields
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
