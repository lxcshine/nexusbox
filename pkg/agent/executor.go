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

package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// SandboxExecutor executes sandbox operations on the local node.
// It wraps the container runtime CLI tools (kata-runtime, runsc, runc)
// to perform sandbox lifecycle operations.
type SandboxExecutor struct {
	mu sync.RWMutex

	// runtimes maps runtime type to its configuration.
	runtimes map[string]*RuntimeConfig

	// activeOps tracks currently running operations.
	activeOps map[string]*Operation

	// maxConcurrentOps is the maximum number of concurrent operations.
	maxConcurrentOps int

	// bundlesDir is the directory for OCI bundles.
	bundlesDir string

	// pidsDir is the directory for PID files.
	pidsDir string
}

// RuntimeConfig holds configuration for a specific runtime.
type RuntimeConfig struct {
	// Type is the runtime type.
	Type string

	// Path is the path to the runtime binary.
	Path string

	// Endpoint is the containerd endpoint for this runtime.
	Endpoint string

	// DefaultVCPUs is the default number of vCPUs (for Kata).
	DefaultVCPUs int

	// DefaultMemoryMB is the default memory in MB (for Kata).
	DefaultMemoryMB int
}

// Operation represents a running sandbox operation.
type Operation struct {
	// ID is the operation ID.
	ID string

	// SandboxName is the sandbox name.
	SandboxName string

	// Type is the operation type (create, start, stop, etc.).
	Type string

	// StartedAt is when the operation started.
	StartedAt time.Time

	// Context is the operation context.
	Ctx context.Context

	// Cancel cancels the operation.
	Cancel context.CancelFunc
}

// NewSandboxExecutor creates a new SandboxExecutor.
func NewSandboxExecutor(maxConcurrentOps int) *SandboxExecutor {
	bundlesDir := "/run/nexusbox/bundles"
	pidsDir := "/run/nexusbox/pids"

	// Ensure directories exist
	os.MkdirAll(bundlesDir, 0755)
	os.MkdirAll(pidsDir, 0755)

	return &SandboxExecutor{
		runtimes: map[string]*RuntimeConfig{
			"kata-containers": {
				Type:           "kata-containers",
				Path:           "kata-runtime",
				Endpoint:       "/run/kata-containers/containerd/kata.sock",
				DefaultVCPUs:   1,
				DefaultMemoryMB: 1024,
			},
			"gvisor": {
				Type:     "gvisor",
				Path:     "runsc",
				Endpoint: "/run/containerd/runsc.sock",
			},
			"runc": {
				Type:     "runc",
				Path:     "runc",
				Endpoint: "/run/containerd/containerd.sock",
			},
		},
		activeOps:        make(map[string]*Operation),
		maxConcurrentOps: maxConcurrentOps,
		bundlesDir:       bundlesDir,
		pidsDir:          pidsDir,
	}
}

// CreateSandbox creates a new sandbox on the local node.
func (se *SandboxExecutor) CreateSandbox(ctx context.Context, spec *SandboxCreateSpec) (*SandboxCreateResult, error) {
	se.mu.Lock()
	if len(se.activeOps) >= se.maxConcurrentOps {
		se.mu.Unlock()
		return nil, fmt.Errorf("too many concurrent operations (%d/%d)", len(se.activeOps), se.maxConcurrentOps)
	}

	opID := fmt.Sprintf("create-%s-%d", spec.Name, time.Now().UnixNano())
	opCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	op := &Operation{
		ID:          opID,
		SandboxName: spec.Name,
		Type:        "create",
		StartedAt:   time.Now(),
		Ctx:         opCtx,
		Cancel:      cancel,
	}
	se.activeOps[opID] = op
	se.mu.Unlock()

	defer func() {
		se.mu.Lock()
		delete(se.activeOps, opID)
		se.mu.Unlock()
	}()

	runtimeConfig, exists := se.runtimes[spec.RuntimeType]
	if !exists {
		cancel()
		return nil, fmt.Errorf("unsupported runtime type: %s", spec.RuntimeType)
	}

	startTime := time.Now()
	containerID := fmt.Sprintf("%s-%s", spec.Namespace, spec.Name)
	bundlePath := filepath.Join(se.bundlesDir, containerID)
	pidFilePath := filepath.Join(se.pidsDir, containerID+".pid")

	// Create bundle directory
	if err := os.MkdirAll(bundlePath, 0755); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create bundle directory: %w", err)
	}

	// Generate OCI spec
	if err := se.generateOCISpec(bundlePath, spec); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to generate OCI spec: %w", err)
	}

	// Create the container using the appropriate runtime
	var cmd *exec.Cmd
	switch spec.RuntimeType {
	case "kata-containers":
		cmd = exec.CommandContext(opCtx, runtimeConfig.Path, "create",
			"--bundle", bundlePath,
			"--pid-file", pidFilePath,
			containerID)
	case "gvisor":
		cmd = exec.CommandContext(opCtx, runtimeConfig.Path, "create",
			"--bundle", bundlePath,
			"--pid-file", pidFilePath,
			containerID)
	case "runc":
		cmd = exec.CommandContext(opCtx, runtimeConfig.Path, "create",
			"--bundle", bundlePath,
			"--pid-file", pidFilePath,
			containerID)
	default:
		cancel()
		return nil, fmt.Errorf("unsupported runtime type: %s", spec.RuntimeType)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create sandbox: %w (output: %s)", err, string(output))
	}

	// Read PID
	pid := 0
	if pidData, err := os.ReadFile(pidFilePath); err == nil {
		fmt.Sscanf(string(pidData), "%d", &pid)
	}

	elapsed := time.Since(startTime)
	klog.Infof("Created sandbox %s/%s with runtime %s in %v (PID: %d)",
		spec.Namespace, spec.Name, spec.RuntimeType, elapsed, pid)

	return &SandboxCreateResult{
		ContainerID: containerID,
		PID:         pid,
		BundlePath:  bundlePath,
		PidFilePath: pidFilePath,
		Duration:    elapsed,
	}, nil
}

// StartSandbox starts a sandbox on the local node.
func (se *SandboxExecutor) StartSandbox(ctx context.Context, containerID, runtimeType string) error {
	runtimeConfig, exists := se.runtimes[runtimeType]
	if !exists {
		return fmt.Errorf("unsupported runtime type: %s", runtimeType)
	}

	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(startCtx, runtimeConfig.Path, "start", containerID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start sandbox %s: %w (output: %s)", containerID, err, string(output))
	}

	klog.Infof("Started sandbox %s with runtime %s", containerID, runtimeType)
	return nil
}

// StopSandbox stops a sandbox on the local node.
func (se *SandboxExecutor) StopSandbox(ctx context.Context, containerID, runtimeType string, timeout time.Duration) error {
	runtimeConfig, exists := se.runtimes[runtimeType]
	if !exists {
		return fmt.Errorf("unsupported runtime type: %s", runtimeType)
	}

	stopCtx, cancel := context.WithTimeout(ctx, timeout+10*time.Second)
	defer cancel()

	// Send SIGTERM
	cmd := exec.CommandContext(stopCtx, runtimeConfig.Path, "kill", containerID, "SIGTERM")
	if output, err := cmd.CombinedOutput(); err != nil {
		klog.Warningf("Failed to send SIGTERM to sandbox %s: %v (output: %s)", containerID, err, string(output))
	}

	// Wait for the sandbox to stop
	time.Sleep(min(timeout, 10*time.Second))

	// Force kill if still running
	cmd = exec.CommandContext(stopCtx, runtimeConfig.Path, "kill", containerID, "SIGKILL")
	cmd.CombinedOutput() // Ignore error, sandbox may have already stopped

	// Delete the container
	cmd = exec.CommandContext(stopCtx, runtimeConfig.Path, "delete", containerID)
	if output, err := cmd.CombinedOutput(); err != nil {
		klog.Warningf("Failed to delete sandbox %s: %v (output: %s)", containerID, err, string(output))
	}

	klog.Infof("Stopped sandbox %s with runtime %s", containerID, runtimeType)
	return nil
}

// PauseSandbox pauses a sandbox on the local node.
func (se *SandboxExecutor) PauseSandbox(ctx context.Context, containerID, runtimeType string) error {
	runtimeConfig, exists := se.runtimes[runtimeType]
	if !exists {
		return fmt.Errorf("unsupported runtime type: %s", runtimeType)
	}

	pauseCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(pauseCtx, runtimeConfig.Path, "pause", containerID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to pause sandbox %s: %w (output: %s)", containerID, err, string(output))
	}

	klog.Infof("Paused sandbox %s with runtime %s", containerID, runtimeType)
	return nil
}

// ResumeSandbox resumes a sandbox on the local node.
func (se *SandboxExecutor) ResumeSandbox(ctx context.Context, containerID, runtimeType string) error {
	runtimeConfig, exists := se.runtimes[runtimeType]
	if !exists {
		return fmt.Errorf("unsupported runtime type: %s", runtimeType)
	}

	resumeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(resumeCtx, runtimeConfig.Path, "resume", containerID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to resume sandbox %s: %w (output: %s)", containerID, err, string(output))
	}

	klog.Infof("Resumed sandbox %s with runtime %s", containerID, runtimeType)
	return nil
}

// SandboxCreateSpec holds the specification for creating a sandbox.
type SandboxCreateSpec struct {
	// Name is the sandbox name.
	Name string

	// Namespace is the sandbox namespace.
	Namespace string

	// RuntimeType is the runtime type.
	RuntimeType string

	// Image is the container image.
	Image string

	// Command is the command to run.
	Command []string

	// Args are the command arguments.
	Args []string

	// Env are the environment variables.
	Env map[string]string

	// CPU is the CPU request (e.g., "500m").
	CPU string

	// Memory is the memory request (e.g., "512Mi").
	Memory string

	// GPU is the GPU request.
	GPU string

	// EphemeralStorage is the ephemeral storage request.
	EphemeralStorage string

	// NetworkMode is the network mode.
	NetworkMode string

	// Ports are the port mappings.
	Ports []PortMapping

	// ReadOnlyRootFilesystem indicates whether the root filesystem is read-only.
	ReadOnlyRootFilesystem bool

	// RunAsNonRoot indicates whether to run as non-root.
	RunAsNonRoot bool
}

// PortMapping represents a port mapping.
type PortMapping struct {
	ContainerPort int
	HostPort      int
	Protocol      string
}

// SandboxCreateResult holds the result of creating a sandbox.
type SandboxCreateResult struct {
	// ContainerID is the container ID.
	ContainerID string

	// PID is the process ID.
	PID int

	// BundlePath is the path to the OCI bundle.
	BundlePath string

	// PidFilePath is the path to the PID file.
	PidFilePath string

	// Duration is how long the creation took.
	Duration time.Duration
}

// generateOCISpec generates an OCI spec for the sandbox.
func (se *SandboxExecutor) generateOCISpec(bundlePath string, spec *SandboxCreateSpec) error {
	// Generate a minimal OCI spec using runc spec command
	cmd := exec.Command("runc", "spec")
	cmd.Dir = bundlePath
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to generate OCI spec: %w", err)
	}

	// In production, we would modify the generated config.json
	// to set the image, command, resources, etc.
	// For now, the generated spec is a starting point.

	return nil
}

// min returns the minimum of two durations.
func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
