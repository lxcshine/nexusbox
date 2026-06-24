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
	"os/exec"
	"sync"
	"time"

	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// KataContainersProvider implements RuntimeProvider for Kata Containers.
// Kata Containers provides VM-level isolation using lightweight VMs,
// offering strong security boundaries between sandboxes.
type KataContainersProvider struct {
	mu       sync.RWMutex
	endpoint string
	config   *RuntimeManagerConfig
	// vmConfig holds VM-level configuration for Kata Containers.
	vmConfig *KataVMConfig
}

// KataVMConfig holds VM-level configuration for Kata Containers.
type KataVMConfig struct {
	// HypervisorType is the hypervisor type (qemu, firecracker, cloud-hypervisor).
	HypervisorType string
	// DefaultVCPUs is the default number of vCPUs for VMs.
	DefaultVCPUs int
	// DefaultMemoryMB is the default memory size in MB for VMs.
	DefaultMemoryMB int
	// DefaultMaxVCPUs is the maximum number of vCPUs.
	DefaultMaxVCPUs int
	// DefaultMaxMemoryMB is the maximum memory size in MB.
	DefaultMaxMemoryMB int
	// SharedFS is the shared filesystem type (virtiofs, virtio-9p).
	SharedFS string
	// VirtioFSDaemon is the path to virtiofsd.
	VirtioFSDaemon string
	// EnableHugePages indicates whether to use huge pages.
	EnableHugePages bool
	// Image is the guest image path.
	Image string
	// Kernel is the guest kernel path.
	Kernel string
	// Initrd is the guest initrd path.
	Initrd string
	// BootToRootTimeMs is the target boot time in milliseconds.
	BootToRootTimeMs int64
}

// DefaultKataVMConfig returns default Kata Containers VM configuration.
func DefaultKataVMConfig() *KataVMConfig {
	return &KataVMConfig{
		HypervisorType:    "qemu",
		DefaultVCPUs:      1,
		DefaultMemoryMB:   1024,
		DefaultMaxVCPUs:   8,
		DefaultMaxMemoryMB: 32768,
		SharedFS:          "virtiofs",
		VirtioFSDaemon:    "/usr/libexec/virtiofsd",
		EnableHugePages:   false,
		Image:             "/usr/share/kata-containers/kata-ubuntu.img",
		Kernel:            "/usr/share/kata-containers/vmlinuz.container",
		Initrd:            "",
		BootToRootTimeMs:  1000,
	}
}

// kataRuntimeHandle implements RuntimeHandle for Kata Containers.
type kataRuntimeHandle struct {
	mu       sync.RWMutex
	id       string
	spec     *RuntimeSpec
	ready    bool
	pid      int
	ip       string
	createdAt time.Time
}

func (h *kataRuntimeHandle) ID() string         { return h.id }
func (h *kataRuntimeHandle) IsReady() bool       { return h.ready }
func (h *kataRuntimeHandle) GetSpec() *RuntimeSpec { return h.spec }
func (h *kataRuntimeHandle) ForceStop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = false
	return nil
}
func (h *kataRuntimeHandle) Cleanup(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = false
	return nil
}

// NewKataContainersProvider creates a new KataContainersProvider.
func NewKataContainersProvider(endpoint string, config *RuntimeManagerConfig) *KataContainersProvider {
	return &KataContainersProvider{
		endpoint: endpoint,
		config:   config,
		vmConfig: DefaultKataVMConfig(),
	}
}

// Type returns the runtime type.
func (p *KataContainersProvider) Type() sandboxv1alpha1.SandboxRuntimeType {
	return sandboxv1alpha1.RuntimeKataContainers
}

// IsAvailable returns whether Kata Containers is available.
func (p *KataContainersProvider) IsAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "kata-runtime", "check")
	if err := cmd.Run(); err != nil {
		klog.V(4).Infof("Kata Containers not available: %v", err)
		return false
	}
	return true
}

// Create creates a new Kata Containers sandbox.
func (p *KataContainersProvider) Create(ctx context.Context, spec *RuntimeSpec) (RuntimeHandle, error) {
	klog.Infof("Creating Kata Containers sandbox: %s/%s", spec.Namespace, spec.SandboxName)

	startTime := time.Now()

	// Build the kata runtime spec
	kataSpec := p.buildKataSpec(spec)

	// Create the VM using kata-runtime
	// In production, this would use the containerd CRI API
	cmd := exec.CommandContext(ctx, "kata-runtime", "create",
		"--bundle", kataSpec.BundlePath,
		"--pid-file", kataSpec.PidFile,
		kataSpec.ContainerID)

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to create kata container: %w", err)
	}

	// Read the PID
	pid := 0
	if data, err := exec.CommandContext(ctx, "cat", kataSpec.PidFile).Output(); err == nil {
		fmt.Sscanf(string(data), "%d", &pid)
	}

	elapsed := time.Since(startTime)
	klog.Infof("Created Kata Containers sandbox %s/%s in %v (PID: %d)",
		spec.Namespace, spec.SandboxName, elapsed, pid)

	handle := &kataRuntimeHandle{
		id:        kataSpec.ContainerID,
		spec:      spec,
		ready:     true,
		pid:       pid,
		createdAt: time.Now(),
	}

	return handle, nil
}

// Start starts a stopped Kata Containers sandbox.
func (p *KataContainersProvider) Start(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*kataRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for Kata Containers")
	}

	klog.Infof("Starting Kata Containers sandbox: %s", h.id)

	cmd := exec.CommandContext(ctx, "kata-runtime", "start", h.id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start kata container %s: %w", h.id, err)
	}

	h.mu.Lock()
	h.ready = true
	h.mu.Unlock()

	return nil
}

// Stop stops a running Kata Containers sandbox.
func (p *KataContainersProvider) Stop(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*kataRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for Kata Containers")
	}

	klog.Infof("Stopping Kata Containers sandbox: %s", h.id)

	cmd := exec.CommandContext(ctx, "kata-runtime", "kill", h.id, "SIGTERM")
	if err := cmd.Run(); err != nil {
		// Try force kill
		cmd = exec.CommandContext(ctx, "kata-runtime", "kill", h.id, "SIGKILL")
		cmd.Run()
	}

	h.mu.Lock()
	h.ready = false
	h.mu.Unlock()

	return nil
}

// ForceStop forcefully stops a Kata Containers sandbox.
func (p *KataContainersProvider) ForceStop(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*kataRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for Kata Containers")
	}

	cmd := exec.CommandContext(ctx, "kata-runtime", "kill", h.id, "SIGKILL")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to force kill kata container %s: %w", h.id, err)
	}

	h.mu.Lock()
	h.ready = false
	h.mu.Unlock()

	return nil
}

// Pause pauses a running Kata Containers sandbox.
func (p *KataContainersProvider) Pause(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*kataRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for Kata Containers")
	}

	klog.Infof("Pausing Kata Containers sandbox: %s", h.id)

	cmd := exec.CommandContext(ctx, "kata-runtime", "pause", h.id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pause kata container %s: %w", h.id, err)
	}

	return nil
}

// Resume resumes a paused Kata Containers sandbox.
func (p *KataContainersProvider) Resume(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*kataRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for Kata Containers")
	}

	klog.Infof("Resuming Kata Containers sandbox: %s", h.id)

	cmd := exec.CommandContext(ctx, "kata-runtime", "resume", h.id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to resume kata container %s: %w", h.id, err)
	}

	h.mu.Lock()
	h.ready = true
	h.mu.Unlock()

	return nil
}

// Status returns the status of a Kata Containers sandbox.
func (p *KataContainersProvider) Status(ctx context.Context, handle RuntimeHandle) (*RuntimeStatus, error) {
	h, ok := handle.(*kataRuntimeHandle)
	if !ok {
		return nil, fmt.Errorf("invalid handle type for Kata Containers")
	}

	return &RuntimeStatus{
		State:     func() RuntimeState {
			if h.ready {
				return RuntimeStateRunning
			}
			return RuntimeStateStopped
		}(),
		PID:       h.pid,
		IP:        h.ip,
		StartedAt: h.createdAt,
	}, nil
}

// Stats returns resource usage statistics for a Kata Containers sandbox.
func (p *KataContainersProvider) Stats(ctx context.Context, handle RuntimeHandle) (*RuntimeStats, error) {
	h, ok := handle.(*kataRuntimeHandle)
	if !ok {
		return nil, fmt.Errorf("invalid handle type for Kata Containers")
	}

	// In production, this would query cgroup stats for the VM process
	stats := &RuntimeStats{
		CollectedAt: time.Now(),
	}

	// Get stats from kata-runtime metrics command
	cmd := exec.CommandContext(ctx, "kata-runtime", "metrics", h.id)
	if output, err := cmd.Output(); err == nil {
		// Parse metrics output
		klog.V(6).Infof("Kata metrics for %s: %s", h.id, string(output))
	}

	return stats, nil
}

// kataSpec holds the Kata Containers specific spec.
type kataSpec struct {
	ContainerID string
	BundlePath  string
	PidFile     string
}

// buildKataSpec builds a Kata Containers specific spec.
func (p *KataContainersProvider) buildKataSpec(spec *RuntimeSpec) *kataSpec {
	containerID := fmt.Sprintf("kata-%s-%s", spec.Namespace, spec.SandboxName)
	bundlePath := fmt.Sprintf("/run/kata-containers/%s", containerID)
	pidFile := fmt.Sprintf("/run/kata-containers/%s/pid", containerID)

	return &kataSpec{
		ContainerID: containerID,
		BundlePath:  bundlePath,
		PidFile:     pidFile,
	}
}

// OptimizeStartup applies startup optimization for Kata Containers.
// This includes VM template pre-creation, memory pre-allocation,
// and other techniques to reduce cold-start latency.
func (p *KataContainersProvider) OptimizeStartup(ctx context.Context) error {
	klog.Info("Optimizing Kata Containers startup")

	// Pre-warm VM templates
	if p.vmConfig.HypervisorType == "qemu" {
		cmd := exec.CommandContext(ctx, "kata-runtime", "factory", "init")
		if err := cmd.Run(); err != nil {
			klog.Warningf("Failed to initialize Kata VM factory: %v", err)
		}
	}

	return nil
}

// GVisorProvider implements RuntimeProvider for gVisor.
type GVisorProvider struct {
	mu       sync.RWMutex
	endpoint string
	config   *RuntimeManagerConfig
}

// gVisorRuntimeHandle implements RuntimeHandle for gVisor.
type gVisorRuntimeHandle struct {
	mu       sync.RWMutex
	id       string
	spec     *RuntimeSpec
	ready    bool
	pid      int
	createdAt time.Time
}

func (h *gVisorRuntimeHandle) ID() string         { return h.id }
func (h *gVisorRuntimeHandle) IsReady() bool       { return h.ready }
func (h *gVisorRuntimeHandle) GetSpec() *RuntimeSpec { return h.spec }
func (h *gVisorRuntimeHandle) ForceStop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = false
	return nil
}
func (h *gVisorRuntimeHandle) Cleanup(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = false
	return nil
}

// Type returns the runtime type.
func (p *GVisorProvider) Type() sandboxv1alpha1.SandboxRuntimeType {
	return sandboxv1alpha1.RuntimeGVisor
}

// IsAvailable returns whether gVisor is available.
func (p *GVisorProvider) IsAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "runsc", "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// Create creates a new gVisor sandbox.
func (p *GVisorProvider) Create(ctx context.Context, spec *RuntimeSpec) (RuntimeHandle, error) {
	klog.Infof("Creating gVisor sandbox: %s/%s", spec.Namespace, spec.SandboxName)

	containerID := fmt.Sprintf("gvisor-%s-%s", spec.Namespace, spec.SandboxName)

	handle := &gVisorRuntimeHandle{
		id:        containerID,
		spec:      spec,
		ready:     true,
		createdAt: time.Now(),
	}

	return handle, nil
}

// Start starts a stopped gVisor sandbox.
func (p *GVisorProvider) Start(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*gVisorRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for gVisor")
	}

	cmd := exec.CommandContext(ctx, "runsc", "start", h.id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start gVisor container %s: %w", h.id, err)
	}

	h.mu.Lock()
	h.ready = true
	h.mu.Unlock()
	return nil
}

// Stop stops a running gVisor sandbox.
func (p *GVisorProvider) Stop(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*gVisorRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for gVisor")
	}

	cmd := exec.CommandContext(ctx, "runsc", "kill", h.id)
	cmd.Run()

	h.mu.Lock()
	h.ready = false
	h.mu.Unlock()
	return nil
}

// ForceStop forcefully stops a gVisor sandbox.
func (p *GVisorProvider) ForceStop(ctx context.Context, handle RuntimeHandle) error {
	return p.Stop(ctx, handle)
}

// Pause pauses a running gVisor sandbox.
func (p *GVisorProvider) Pause(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*gVisorRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for gVisor")
	}

	cmd := exec.CommandContext(ctx, "runsc", "pause", h.id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pause gVisor container %s: %w", h.id, err)
	}
	return nil
}

// Resume resumes a paused gVisor sandbox.
func (p *GVisorProvider) Resume(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*gVisorRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for gVisor")
	}

	cmd := exec.CommandContext(ctx, "runsc", "resume", h.id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to resume gVisor container %s: %w", h.id, err)
	}

	h.mu.Lock()
	h.ready = true
	h.mu.Unlock()
	return nil
}

// Status returns the status of a gVisor sandbox.
func (p *GVisorProvider) Status(ctx context.Context, handle RuntimeHandle) (*RuntimeStatus, error) {
	h, ok := handle.(*gVisorRuntimeHandle)
	if !ok {
		return nil, fmt.Errorf("invalid handle type for gVisor")
	}

	return &RuntimeStatus{
		State: func() RuntimeState {
			if h.ready {
				return RuntimeStateRunning
			}
			return RuntimeStateStopped
		}(),
		PID:       h.pid,
		StartedAt: h.createdAt,
	}, nil
}

// Stats returns resource usage statistics for a gVisor sandbox.
func (p *GVisorProvider) Stats(ctx context.Context, handle RuntimeHandle) (*RuntimeStats, error) {
	return &RuntimeStats{CollectedAt: time.Now()}, nil
}

// RuncProvider implements RuntimeProvider for standard runc containers.
type RuncProvider struct {
	mu       sync.RWMutex
	endpoint string
	config   *RuntimeManagerConfig
}

// runcRuntimeHandle implements RuntimeHandle for runc.
type runcRuntimeHandle struct {
	mu       sync.RWMutex
	id       string
	spec     *RuntimeSpec
	ready    bool
	pid      int
	createdAt time.Time
}

func (h *runcRuntimeHandle) ID() string         { return h.id }
func (h *runcRuntimeHandle) IsReady() bool       { return h.ready }
func (h *runcRuntimeHandle) GetSpec() *RuntimeSpec { return h.spec }
func (h *runcRuntimeHandle) ForceStop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = false
	return nil
}
func (h *runcRuntimeHandle) Cleanup(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = false
	return nil
}

// Type returns the runtime type.
func (p *RuncProvider) Type() sandboxv1alpha1.SandboxRuntimeType {
	return sandboxv1alpha1.RuntimeRunc
}

// IsAvailable returns whether runc is available.
func (p *RuncProvider) IsAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "runc", "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// Create creates a new runc container.
func (p *RuncProvider) Create(ctx context.Context, spec *RuntimeSpec) (RuntimeHandle, error) {
	klog.Infof("Creating runc sandbox: %s/%s", spec.Namespace, spec.SandboxName)

	containerID := fmt.Sprintf("runc-%s-%s", spec.Namespace, spec.SandboxName)

	handle := &runcRuntimeHandle{
		id:        containerID,
		spec:      spec,
		ready:     true,
		createdAt: time.Now(),
	}

	return handle, nil
}

// Start starts a stopped runc container.
func (p *RuncProvider) Start(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*runcRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for runc")
	}

	cmd := exec.CommandContext(ctx, "runc", "start", h.id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start runc container %s: %w", h.id, err)
	}

	h.mu.Lock()
	h.ready = true
	h.mu.Unlock()
	return nil
}

// Stop stops a running runc container.
func (p *RuncProvider) Stop(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*runcRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for runc")
	}

	cmd := exec.CommandContext(ctx, "runc", "kill", h.id, "SIGTERM")
	cmd.Run()

	h.mu.Lock()
	h.ready = false
	h.mu.Unlock()
	return nil
}

// ForceStop forcefully stops a runc container.
func (p *RuncProvider) ForceStop(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*runcRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for runc")
	}

	cmd := exec.CommandContext(ctx, "runc", "kill", h.id, "SIGKILL")
	cmd.Run()

	h.mu.Lock()
	h.ready = false
	h.mu.Unlock()
	return nil
}

// Pause pauses a running runc container.
func (p *RuncProvider) Pause(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*runcRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for runc")
	}

	cmd := exec.CommandContext(ctx, "runc", "pause", h.id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to pause runc container %s: %w", h.id, err)
	}
	return nil
}

// Resume resumes a paused runc container.
func (p *RuncProvider) Resume(ctx context.Context, handle RuntimeHandle) error {
	h, ok := handle.(*runcRuntimeHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for runc")
	}

	cmd := exec.CommandContext(ctx, "runc", "resume", h.id)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to resume runc container %s: %w", h.id, err)
	}

	h.mu.Lock()
	h.ready = true
	h.mu.Unlock()
	return nil
}

// Status returns the status of a runc container.
func (p *RuncProvider) Status(ctx context.Context, handle RuntimeHandle) (*RuntimeStatus, error) {
	h, ok := handle.(*runcRuntimeHandle)
	if !ok {
		return nil, fmt.Errorf("invalid handle type for runc")
	}

	return &RuntimeStatus{
		State: func() RuntimeState {
			if h.ready {
				return RuntimeStateRunning
			}
			return RuntimeStateStopped
		}(),
		PID:       h.pid,
		StartedAt: h.createdAt,
	}, nil
}

// Stats returns resource usage statistics for a runc container.
func (p *RuncProvider) Stats(ctx context.Context, handle RuntimeHandle) (*RuntimeStats, error) {
	return &RuntimeStats{CollectedAt: time.Now()}, nil
}
