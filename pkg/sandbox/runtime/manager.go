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
	"sync"
	"time"

	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// RuntimeManager manages sandbox runtimes across the cluster.
// It provides a unified interface for creating, managing, and destroying
// sandbox runtimes, including Kata Containers for strong isolation.
type RuntimeManager struct {
	mu sync.RWMutex

	// providers maps runtime type to its provider.
	providers map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider

	// handles tracks active runtime handles.
	handles map[string]RuntimeHandle

	// poolManager manages pre-warmed sandbox pools.
	poolManager *PoolManager

	// config holds runtime configuration.
	config *RuntimeManagerConfig

	// stopCh is used to signal shutdown.
	stopCh chan struct{}
}

// RuntimeManagerConfig holds configuration for the RuntimeManager.
type RuntimeManagerConfig struct {
	// KataContainersEndpoint is the endpoint for Kata Containers runtime.
	KataContainersEndpoint string
	// GVisorEndpoint is the endpoint for gVisor runtime.
	GVisorEndpoint string
	// RuncEndpoint is the endpoint for runc runtime.
	RuncEndpoint string
	// PoolEnabled indicates whether sandbox pooling is enabled.
	PoolEnabled bool
	// PoolSize is the default pool size per runtime type.
	PoolSize map[sandboxv1alpha1.SandboxRuntimeType]int32
	// PoolRefreshInterval is how often pools are refreshed.
	PoolRefreshInterval time.Duration
	// CreateTimeout is the timeout for sandbox creation.
	CreateTimeout time.Duration
	// StartTimeout is the timeout for sandbox start.
	StartTimeout time.Duration
	// StopTimeout is the timeout for sandbox stop.
	StopTimeout time.Duration
	// PauseTimeout is the timeout for sandbox pause.
	PauseTimeout time.Duration
	// ResumeTimeout is the timeout for sandbox resume.
	ResumeTimeout time.Duration
	// MaxConcurrentOperations is the maximum number of concurrent runtime operations.
	MaxConcurrentOperations int
}

// DefaultRuntimeManagerConfig returns default runtime manager configuration.
func DefaultRuntimeManagerConfig() *RuntimeManagerConfig {
	return &RuntimeManagerConfig{
		KataContainersEndpoint:  "/run/kata-containers/containerd/kata.sock",
		GVisorEndpoint:          "/run/containerd/runsc.sock",
		RuncEndpoint:            "/run/containerd/containerd.sock",
		PoolEnabled:             true,
		PoolSize: map[sandboxv1alpha1.SandboxRuntimeType]int32{
			sandboxv1alpha1.RuntimeKataContainers: 5,
			sandboxv1alpha1.RuntimeGVisor:         10,
			sandboxv1alpha1.RuntimeRunc:           20,
		},
		PoolRefreshInterval:     30 * time.Second,
		CreateTimeout:           120 * time.Second,
		StartTimeout:            60 * time.Second,
		StopTimeout:             30 * time.Second,
		PauseTimeout:            30 * time.Second,
		ResumeTimeout:           60 * time.Second,
		MaxConcurrentOperations: 100,
	}
}

// RuntimeProvider provides sandbox runtime operations.
type RuntimeProvider interface {
	// Create creates a new sandbox runtime instance.
	Create(ctx context.Context, spec *RuntimeSpec) (RuntimeHandle, error)
	// Start starts a stopped sandbox runtime instance.
	Start(ctx context.Context, handle RuntimeHandle) error
	// Stop stops a running sandbox runtime instance.
	Stop(ctx context.Context, handle RuntimeHandle) error
	// ForceStop forcefully stops a sandbox runtime instance.
	ForceStop(ctx context.Context, handle RuntimeHandle) error
	// Pause pauses a running sandbox runtime instance.
	Pause(ctx context.Context, handle RuntimeHandle) error
	// Resume resumes a paused sandbox runtime instance.
	Resume(ctx context.Context, handle RuntimeHandle) error
	// Status returns the status of a sandbox runtime instance.
	Status(ctx context.Context, handle RuntimeHandle) (*RuntimeStatus, error)
	// Stats returns resource usage statistics.
	Stats(ctx context.Context, handle RuntimeHandle) (*RuntimeStats, error)
	// Type returns the runtime type.
	Type() sandboxv1alpha1.SandboxRuntimeType
	// IsAvailable returns whether the runtime is available on this node.
	IsAvailable(ctx context.Context) bool
}

// RuntimeHandle represents a handle to a sandbox runtime instance.
type RuntimeHandle interface {
	// ID returns the runtime-specific identifier.
	ID() string
	// IsReady returns whether the runtime is ready.
	IsReady() bool
	// GetSpec returns the runtime specification.
	GetSpec() *RuntimeSpec
	// ForceStop forcefully stops the runtime.
	ForceStop(ctx context.Context) error
	// Cleanup cleans up runtime resources.
	Cleanup(ctx context.Context) error
}

// RuntimeSpec defines the specification for a sandbox runtime.
type RuntimeSpec struct {
	// SandboxName is the name of the sandbox.
	SandboxName string
	// Namespace is the namespace of the sandbox.
	Namespace string
	// TenantName is the tenant that owns the sandbox.
	TenantName string
	// RuntimeType is the type of runtime.
	RuntimeType sandboxv1alpha1.SandboxRuntimeType
	// Image is the container image to run.
	Image string
	// Command is the command to execute.
	Command []string
	// Args are the arguments to the command.
	Args []string
	// Env are environment variables.
	Env map[string]string
	// WorkingDir is the working directory.
	WorkingDir string
	// Resources are the resource requirements.
	Resources sandboxv1alpha1.ResourceRequirements
	// NetworkConfig is the network configuration.
	NetworkConfig *sandboxv1alpha1.SandboxNetworkSpec
	// StorageConfig is the storage configuration.
	StorageConfig *sandboxv1alpha1.SandboxStorageSpec
	// SecurityConfig is the security configuration.
	SecurityConfig *sandboxv1alpha1.SandboxSecuritySpec
	// NodeName is the target node.
	NodeName string
	// Annotations are runtime-specific annotations.
	Annotations map[string]string
}

// RuntimeStatus represents the status of a sandbox runtime.
type RuntimeStatus struct {
	// State is the current runtime state.
	State RuntimeState
	// PID is the process ID (if applicable).
	PID int
	// IP is the IP address assigned to the sandbox.
	IP string
	// StartedAt is the time the sandbox started.
	StartedAt time.Time
	// FinishedAt is the time the sandbox finished.
	FinishedAt time.Time
	// ExitCode is the exit code (if finished).
	ExitCode int
	// Error is any error message.
	Error string
}

// RuntimeState represents the state of a sandbox runtime.
type RuntimeState string

const (
	// RuntimeStateCreated indicates the runtime has been created.
	RuntimeStateCreated RuntimeState = "created"
	// RuntimeStateRunning indicates the runtime is running.
	RuntimeStateRunning RuntimeState = "running"
	// RuntimeStatePaused indicates the runtime is paused.
	RuntimeStatePaused RuntimeState = "paused"
	// RuntimeStateStopped indicates the runtime is stopped.
	RuntimeStateStopped RuntimeState = "stopped"
	// RuntimeStateError indicates the runtime is in an error state.
	RuntimeStateError RuntimeState = "error"
)

// RuntimeStats represents resource usage statistics for a sandbox.
type RuntimeStats struct {
	// CPUUsageNanoCores is the CPU usage in nano-cores.
	CPUUsageNanoCores uint64
	// MemoryUsageBytes is the memory usage in bytes.
	MemoryUsageBytes uint64
	// MemoryWorkingSetBytes is the working set memory in bytes.
	MemoryWorkingSetBytes uint64
	// StorageUsageBytes is the storage usage in bytes.
	StorageUsageBytes uint64
	// NetworkRxBytes is the total received bytes.
	NetworkRxBytes uint64
	// NetworkTxBytes is the total transmitted bytes.
	NetworkTxBytes uint64
	// NetworkRxErrors is the total receive errors.
	NetworkRxErrors uint64
	// NetworkTxErrors is the total transmit errors.
	NetworkTxErrors uint64
	// GPUMemoryUsageBytes is the GPU memory usage.
	GPUMemoryUsageBytes uint64
	// CollectedAt is the time the stats were collected.
	CollectedAt time.Time
}

// NewRuntimeManager creates a new RuntimeManager.
func NewRuntimeManager(config *RuntimeManagerConfig) *RuntimeManager {
	if config == nil {
		config = DefaultRuntimeManagerConfig()
	}

	rm := &RuntimeManager{
		providers: make(map[sandboxv1alpha1.SandboxRuntimeType]RuntimeProvider),
		handles:   make(map[string]RuntimeHandle),
		config:    config,
		stopCh:    make(chan struct{}),
	}

	// Register default providers
	rm.RegisterProvider(&KataContainersProvider{
		endpoint: config.KataContainersEndpoint,
		config:   config,
	})
	rm.RegisterProvider(&GVisorProvider{
		endpoint: config.GVisorEndpoint,
		config:   config,
	})
	rm.RegisterProvider(&RuncProvider{
		endpoint: config.RuncEndpoint,
		config:   config,
	})

	// Initialize pool manager
	if config.PoolEnabled {
		rm.poolManager = NewPoolManager(rm, config)
	}

	return rm
}

// Start starts the runtime manager.
func (rm *RuntimeManager) Start(ctx context.Context) {
	klog.Info("Starting runtime manager")

	// Start pool manager if enabled
	if rm.poolManager != nil {
		rm.poolManager.Start(ctx)
	}

	klog.Info("Runtime manager started")
}

// Stop stops the runtime manager.
func (rm *RuntimeManager) Stop() {
	klog.Info("Stopping runtime manager")
	close(rm.stopCh)

	if rm.poolManager != nil {
		rm.poolManager.Stop()
	}
}

// RegisterProvider registers a runtime provider.
func (rm *RuntimeManager) RegisterProvider(provider RuntimeProvider) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.providers[provider.Type()] = provider
	klog.Infof("Registered runtime provider: %s", provider.Type())
}

// CreateRuntime creates a new sandbox runtime.
func (rm *RuntimeManager) CreateRuntime(ctx context.Context, spec *RuntimeSpec) (RuntimeHandle, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	provider, exists := rm.providers[spec.RuntimeType]
	if !exists {
		return nil, fmt.Errorf("no provider registered for runtime type %s", spec.RuntimeType)
	}

	// Check if provider is available
	if !provider.IsAvailable(ctx) {
		return nil, fmt.Errorf("runtime provider %s is not available", spec.RuntimeType)
	}

	// Try to get from pool first
	if rm.poolManager != nil {
		if handle := rm.poolManager.GetFromPool(spec.RuntimeType, spec); handle != nil {
			key := spec.SandboxName + "/" + spec.Namespace
			rm.handles[key] = handle
			klog.Infof("Got sandbox runtime from pool for %s (type: %s)", key, spec.RuntimeType)
			return handle, nil
		}
	}

	// Create new runtime
	ctx, cancel := context.WithTimeout(ctx, rm.config.CreateTimeout)
	defer cancel()

	handle, err := provider.Create(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox runtime: %w", err)
	}

	key := spec.SandboxName + "/" + spec.Namespace
	rm.handles[key] = handle

	klog.Infof("Created sandbox runtime for %s (type: %s, id: %s)", key, spec.RuntimeType, handle.ID())
	return handle, nil
}

// StartRuntime starts a sandbox runtime.
func (rm *RuntimeManager) StartRuntime(ctx context.Context, key string) error {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	handle, exists := rm.handles[key]
	if !exists {
		return fmt.Errorf("runtime handle not found for %s", key)
	}

	spec := handle.GetSpec()
	provider, exists := rm.providers[spec.RuntimeType]
	if !exists {
		return fmt.Errorf("no provider for runtime type %s", spec.RuntimeType)
	}

	ctx, cancel := context.WithTimeout(ctx, rm.config.StartTimeout)
	defer cancel()

	return provider.Start(ctx, handle)
}

// StopRuntime stops a sandbox runtime.
func (rm *RuntimeManager) StopRuntime(ctx context.Context, key string) error {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	handle, exists := rm.handles[key]
	if !exists {
		return fmt.Errorf("runtime handle not found for %s", key)
	}

	spec := handle.GetSpec()
	provider, exists := rm.providers[spec.RuntimeType]
	if !exists {
		return fmt.Errorf("no provider for runtime type %s", spec.RuntimeType)
	}

	ctx, cancel := context.WithTimeout(ctx, rm.config.StopTimeout)
	defer cancel()

	return provider.Stop(ctx, handle)
}

// PauseRuntime pauses a sandbox runtime.
func (rm *RuntimeManager) PauseRuntime(ctx context.Context, key string) error {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	handle, exists := rm.handles[key]
	if !exists {
		return fmt.Errorf("runtime handle not found for %s", key)
	}

	spec := handle.GetSpec()
	provider, exists := rm.providers[spec.RuntimeType]
	if !exists {
		return fmt.Errorf("no provider for runtime type %s", spec.RuntimeType)
	}

	ctx, cancel := context.WithTimeout(ctx, rm.config.PauseTimeout)
	defer cancel()

	return provider.Pause(ctx, handle)
}

// ResumeRuntime resumes a paused sandbox runtime.
func (rm *RuntimeManager) ResumeRuntime(ctx context.Context, key string) error {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	handle, exists := rm.handles[key]
	if !exists {
		return fmt.Errorf("runtime handle not found for %s", key)
	}

	spec := handle.GetSpec()
	provider, exists := rm.providers[spec.RuntimeType]
	if !exists {
		return fmt.Errorf("no provider for runtime type %s", spec.RuntimeType)
	}

	ctx, cancel := context.WithTimeout(ctx, rm.config.ResumeTimeout)
	defer cancel()

	return provider.Resume(ctx, handle)
}

// GetRuntimeStatus returns the status of a sandbox runtime.
func (rm *RuntimeManager) GetRuntimeStatus(ctx context.Context, key string) (*RuntimeStatus, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	handle, exists := rm.handles[key]
	if !exists {
		return nil, fmt.Errorf("runtime handle not found for %s", key)
	}

	spec := handle.GetSpec()
	provider, exists := rm.providers[spec.RuntimeType]
	if !exists {
		return nil, fmt.Errorf("no provider for runtime type %s", spec.RuntimeType)
	}

	return provider.Status(ctx, handle)
}

// GetRuntimeStats returns resource usage statistics for a sandbox.
func (rm *RuntimeManager) GetRuntimeStats(ctx context.Context, key string) (*RuntimeStats, error) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	handle, exists := rm.handles[key]
	if !exists {
		return nil, fmt.Errorf("runtime handle not found for %s", key)
	}

	spec := handle.GetSpec()
	provider, exists := rm.providers[spec.RuntimeType]
	if !exists {
		return nil, fmt.Errorf("no provider for runtime type %s", spec.RuntimeType)
	}

	return provider.Stats(ctx, handle)
}

// RemoveRuntime removes a sandbox runtime handle.
func (rm *RuntimeManager) RemoveRuntime(key string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	delete(rm.handles, key)
}

// GetProvider returns the runtime provider for a given type.
func (rm *RuntimeManager) GetProvider(runtimeType sandboxv1alpha1.SandboxRuntimeType) (RuntimeProvider, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	provider, exists := rm.providers[runtimeType]
	return provider, exists
}

// ListActiveRuntimes returns all active runtime handles.
func (rm *RuntimeManager) ListActiveRuntimes() map[string]RuntimeHandle {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	result := make(map[string]RuntimeHandle, len(rm.handles))
	for key, handle := range rm.handles {
		result[key] = handle
	}
	return result
}
