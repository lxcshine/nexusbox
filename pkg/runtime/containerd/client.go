/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package containerd

import (
	"context"
	"fmt"
	"io"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/security/rootless"
	spec "github.com/opencontainers/runtime-spec/specs-go"
	"k8s.io/klog/v2"
)

// Client wraps a containerd client for sandbox runtime operations.
type Client struct {
	client    *containerd.Client
	namespace string

	// rootlessManager handles user namespace uid/gid mapping.
	// nil if rootless mode is not configured.
	rootlessManager *rootless.Manager
}

// ClientConfig holds configuration for the containerd client.
type ClientConfig struct {
	Address   string
	Namespace string
	Timeout   time.Duration
}

// DefaultClientConfig returns a default client configuration.
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		Address:   "/run/containerd/containerd.sock",
		Namespace: "nexusbox",
		Timeout:   10 * time.Second,
	}
}

// NewClient creates a new containerd client.
func NewClient(config *ClientConfig) (*Client, error) {
	if config == nil {
		config = DefaultClientConfig()
	}
	client, err := containerd.New(config.Address,
		containerd.WithTimeout(config.Timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to containerd at %s: %w", config.Address, err)
	}
	klog.Infof("Connected to containerd at %s (namespace: %s)", config.Address, config.Namespace)

	// Initialize rootless manager (no-op if /etc/subuid not configured)
	rootlessMgr, err := rootless.NewManager()
	if err != nil {
		klog.Warningf("Rootless manager initialization failed (continuing in rootful mode): %v", err)
		rootlessMgr = nil
	}

	return &Client{
		client:          client,
		namespace:       config.Namespace,
		rootlessManager: rootlessMgr,
	}, nil
}

// Close closes the containerd client.
func (c *Client) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// WithNamespace returns a context with the containerd namespace.
func (c *Client) WithNamespace(ctx context.Context) context.Context {
	return namespaces.WithNamespace(ctx, c.namespace)
}

// CreateSandbox creates a new sandbox container in containerd.
func (c *Client) CreateSandbox(ctx context.Context, sb *sandboxv1alpha1.Sandbox, runtimeType sandboxv1alpha1.SandboxRuntimeType) (string, error) {
	ctx = c.WithNamespace(ctx)
	sandboxID := fmt.Sprintf("%s-%s", sb.Namespace, sb.Name)

	image, err := c.client.GetImage(ctx, sb.Spec.Image)
	if err != nil {
		klog.Infof("Image %s not found locally, pulling...", sb.Spec.Image)
		image, err = c.client.Pull(ctx, sb.Spec.Image, containerd.WithPullUnpack)
		if err != nil {
			return "", fmt.Errorf("failed to pull image %s: %w", sb.Spec.Image, err)
		}
	}

	specOpts := c.buildSpecOptions(sb, runtimeType)

	container, err := c.client.NewContainer(
		ctx, sandboxID,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(sandboxID+"-snapshot", image),
		containerd.WithSpec(nil, specOpts...),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create container %s: %w", sandboxID, err)
	}

	task, err := container.NewTask(ctx, cio.LogFile(c.logPath(sandboxID)))
	if err != nil {
		container.Delete(ctx, containerd.WithSnapshotCleanup)
		return "", fmt.Errorf("failed to create task for %s: %w", sandboxID, err)
	}

	if err := task.Start(ctx); err != nil {
		task.Delete(ctx)
		container.Delete(ctx, containerd.WithSnapshotCleanup)
		return "", fmt.Errorf("failed to start task for %s: %w", sandboxID, err)
	}

	klog.Infof("Created sandbox %s (runtime: %s, PID: %d)", sandboxID, runtimeType, task.Pid())
	return sandboxID, nil
}

// StopSandbox stops a sandbox container gracefully.
func (c *Client) StopSandbox(ctx context.Context, sandboxID string, timeout time.Duration) error {
	ctx = c.WithNamespace(ctx)
	container, err := c.client.LoadContainer(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", sandboxID, err)
	}
	task, err := container.Task(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get task for %s: %w", sandboxID, err)
	}

	if err := task.Kill(ctx, syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to %s: %w", sandboxID, err)
	}

	exitCh, err := task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for task %s: %w", sandboxID, err)
	}

	select {
	case <-exitCh:
		klog.Infof("Sandbox %s exited gracefully", sandboxID)
	case <-time.After(timeout):
		klog.Warningf("Sandbox %s did not exit within %v, sending SIGKILL", sandboxID, timeout)
		if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
			return fmt.Errorf("failed to kill %s: %w", sandboxID, err)
		}
	}

	_, err = task.Delete(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete task %s: %w", sandboxID, err)
	}
	return container.Delete(ctx, containerd.WithSnapshotCleanup)
}

// DeleteSandbox forcefully removes a sandbox container.
func (c *Client) DeleteSandbox(ctx context.Context, sandboxID string) error {
	ctx = c.WithNamespace(ctx)
	container, err := c.client.LoadContainer(ctx, sandboxID)
	if err != nil {
		return nil
	}
	task, err := container.Task(ctx, nil)
	if err == nil {
		task.Kill(ctx, syscall.SIGKILL)
		task.Delete(ctx)
	}
	return container.Delete(ctx, containerd.WithSnapshotCleanup)
}

// PauseSandbox pauses a sandbox container (cgroups freezer).
func (c *Client) PauseSandbox(ctx context.Context, sandboxID string) error {
	ctx = c.WithNamespace(ctx)
	container, err := c.client.LoadContainer(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", sandboxID, err)
	}
	task, err := container.Task(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get task for %s: %w", sandboxID, err)
	}
	return task.Pause(ctx)
}

// ResumeSandbox resumes a paused sandbox container.
func (c *Client) ResumeSandbox(ctx context.Context, sandboxID string) error {
	ctx = c.WithNamespace(ctx)
	container, err := c.client.LoadContainer(ctx, sandboxID)
	if err != nil {
		return fmt.Errorf("failed to load container %s: %w", sandboxID, err)
	}
	task, err := container.Task(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to get task for %s: %w", sandboxID, err)
	}
	return task.Resume(ctx)
}

// ExecInSandbox executes a command inside a running sandbox.
func (c *Client) ExecInSandbox(ctx context.Context, sandboxID string, cmd []string, stdin io.Reader, stdout, stderr io.Writer) (uint32, error) {
	ctx = c.WithNamespace(ctx)
	container, err := c.client.LoadContainer(ctx, sandboxID)
	if err != nil {
		return 0, fmt.Errorf("failed to load container %s: %w", sandboxID, err)
	}
	task, err := container.Task(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to get task for %s: %w", sandboxID, err)
	}

	process, err := task.Exec(ctx, sandboxID+"-exec", &spec.Process{
		Args: cmd,
		Cwd:  "/",
	}, cio.NewCreator(cio.WithStreams(stdin, stdout, stderr)))
	if err != nil {
		return 0, fmt.Errorf("failed to exec in %s: %w", sandboxID, err)
	}
	defer process.Delete(ctx)

	exitCh, err := process.Wait(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to wait for exec in %s: %w", sandboxID, err)
	}
	if err := process.Start(ctx); err != nil {
		return 0, fmt.Errorf("failed to start exec in %s: %w", sandboxID, err)
	}

	status := <-exitCh
	return status.ExitCode(), nil
}

// buildSpecOptions builds OCI spec options based on sandbox configuration.
func (c *Client) buildSpecOptions(sb *sandboxv1alpha1.Sandbox, runtimeType sandboxv1alpha1.SandboxRuntimeType) []oci.SpecOpts {
	opts := []oci.SpecOpts{
		oci.WithImageConfig(nil),
	}

	if len(sb.Spec.Command) > 0 {
		opts = append(opts, oci.WithProcessArgs(sb.Spec.Command...))
	}
	if len(sb.Spec.Env) > 0 {
		envVars := make([]string, 0, len(sb.Spec.Env))
		for _, env := range sb.Spec.Env {
			envVars = append(envVars, env.Name+"="+env.Value)
		}
		opts = append(opts, oci.WithEnv(envVars))
	}
	if sb.Spec.WorkingDir != "" {
		opts = append(opts, oci.WithProcessCwd(sb.Spec.WorkingDir))
	}

	opts = append(opts, c.resourceSpecOptions(&sb.Spec.Resources)...)
	opts = append(opts, c.securitySpecOptions(sb.Spec.Security, runtimeType)...)

	// Apply rootless user namespace mapping if available.
	// This must come after security options so the mappings are not overwritten.
	if c.rootlessManager != nil && c.rootlessManager.IsEnabled() {
		opts = append(opts, c.rootlessSpecOptions()...)
		klog.V(2).Infof("Applied rootless user namespace mapping for sandbox %s/%s",
			sb.Namespace, sb.Name)
	}

	return opts
}

// rootlessSpecOptions returns OCI spec options for user namespace uid/gid mapping.
func (c *Client) rootlessSpecOptions() []oci.SpecOpts {
	cfg := c.rootlessManager.Config()
	if !cfg.Enabled {
		return nil
	}

	return []oci.SpecOpts{
		withUserNamespace(cfg.UIDMappings, cfg.GIDMappings),
	}
}

// withUserNamespace configures the OCI spec to use a user namespace with the
// given uid/gid mappings. This is what enables rootless execution: the sandbox
// sees itself as root (uid 0) but the kernel maps it to an unprivileged host uid.
func withUserNamespace(uidMappings, gidMappings []rootless.IDMapping) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *spec.Spec) error {
		if s.Linux == nil {
			s.Linux = &spec.Linux{}
		}

		// Add user namespace to the namespaces list
		var hasUserNS bool
		for _, ns := range s.Linux.Namespaces {
			if ns.Type == spec.UserNamespace {
				hasUserNS = true
				break
			}
		}
		if !hasUserNS {
			s.Linux.Namespaces = append(s.Linux.Namespaces, spec.LinuxNamespace{
				Type: spec.UserNamespace,
			})
		}

		// Set uid mappings
		s.Linux.UIDMappings = make([]spec.LinuxIDMapping, 0, len(uidMappings))
		for _, m := range uidMappings {
			s.Linux.UIDMappings = append(s.Linux.UIDMappings, spec.LinuxIDMapping{
				ContainerID: m.ContainerID,
				HostID:      m.HostID,
				Size:        m.Size,
			})
		}

		// Set gid mappings
		s.Linux.GIDMappings = make([]spec.LinuxIDMapping, 0, len(gidMappings))
		for _, m := range gidMappings {
			s.Linux.GIDMappings = append(s.Linux.GIDMappings, spec.LinuxIDMapping{
				ContainerID: m.ContainerID,
				HostID:      m.HostID,
				Size:        m.Size,
			})
		}

		return nil
	}
}

// resourceSpecOptions returns OCI spec options for resource limits.
func (c *Client) resourceSpecOptions(resources *sandboxv1alpha1.ResourceRequirements) []oci.SpecOpts {
	var opts []oci.SpecOpts
	if resources == nil {
		return opts
	}
	if resources.CPU != "" {
		if cpu := parseMilliCPU(resources.CPU); cpu > 0 {
			opts = append(opts, oci.WithCPUShares(uint64(cpu)))
		}
	}
	if resources.Memory != "" {
		if mem := parseMemoryBytes(resources.Memory); mem > 0 {
			opts = append(opts, oci.WithMemoryLimit(uint64(mem)))
		}
	}
	return opts
}

// defaultDroppedCapabilities is the set of high-risk Linux capabilities that
// are ALWAYS dropped from sandbox containers, regardless of user configuration.
// These match the Kubernetes/Docker default drop list plus additional
// hardening for sandbox escape prevention.
//
// Reference: capabilities(7), Kubernetes Pod Security Standards (restricted).
var defaultDroppedCapabilities = []string{
	"CAP_AUDIT_CONTROL",      // Manipulate audit subsystem
	"CAP_AUDIT_WRITE",        // Write to audit log
	"CAP_BLOCK_SUSPEND",      // Block system suspend (DoS vector)
	"CAP_DAC_READ_SEARCH",    // Bypass file DAC read/search checks
	"CAP_IPC_LOCK",           // Lock memory (DoS via mlock)
	"CAP_MAC_ADMIN",          // Mandatory Access Control admin
	"CAP_MAC_OVERRIDE",       // Bypass MAC
	"CAP_SYS_ADMIN",          // THE big one: broad system admin (mount, namespaces, etc.)
	"CAP_SYS_BOOT",           // Reboot the system
	"CAP_SYS_MODULE",         // Load/unload kernel modules
	"CAP_SYS_NICE",           // Bypass nice limits (CPU starvation)
	"CAP_SYS_PACCT",          // Process accounting
	"CAP_SYS_PTRACE",         // ptrace any process (sandbox escape!)
	"CAP_SYS_RAWIO",          // Raw I/O (kernel exploit vector)
	"CAP_SYS_RESOURCE",       // Bypass resource limits
	"CAP_SYS_TIME",           // Set system clock (time attacks)
	"CAP_SYS_TTY_CONFIG",     // TTY config
	"CAP_SYSLOG",             // Syslog manipulation
	"CAP_WAKE_ALARM",         // Wake alarm (DoS)
	"CAP_LINUX_IMMUTABLE",    // Make files immutable
	"CAP_NET_BROADCAST",      // Network broadcast (ARP spoofing)
	"CAP_NET_RAW",            // Raw sockets (network attack vector) - dropped unless explicitly allowed
	"CAP_PERFMON",            // Performance monitoring (side-channel)
	"CAP_BPF",                // Load BPF programs (kernel attack surface)
	"CAP_CHECKPOINT_RESTORE", // Checkpoint/restore (sandbox escape)
}

// securitySpecOptions returns OCI spec options for security configuration.
func (c *Client) securitySpecOptions(security *sandboxv1alpha1.SandboxSecuritySpec, runtimeType sandboxv1alpha1.SandboxRuntimeType) []oci.SpecOpts {
	var opts []oci.SpecOpts

	// Default: no new privileges - prevents setuid escalation
	opts = append(opts, oci.WithNoNewPrivileges)

	// Always drop high-risk capabilities (defense in depth)
	opts = append(opts, withDroppedCapabilities(defaultDroppedCapabilities))

	if security == nil {
		return opts
	}

	if security.ReadOnlyRootFilesystem {
		opts = append(opts, oci.WithRootFSReadonly())
	}

	if security.RunAsUser != nil {
		opts = append(opts, oci.WithUserID(uint32(*security.RunAsUser)))
	}
	if security.RunAsGroup != nil {
		opts = append(opts, withGID(uint32(*security.RunAsGroup)))
	}

	// Handle user-specified capabilities.
	// - Add: only grant if not in the default drop list (refuse to re-grant dropped caps)
	// - Drop: append to the always-drop list
	if security.Capabilities != nil {
		// Filter Add list to prevent re-granting dropped caps
		var safeAdd []string
		dropped := make(map[string]struct{}, len(defaultDroppedCapabilities))
		for _, cap := range defaultDroppedCapabilities {
			dropped[cap] = struct{}{}
		}
		for _, cap := range security.Capabilities.Add {
			if _, blocked := dropped[cap]; blocked {
				klog.Warningf("Refusing to re-grant dropped capability %s for sandbox", cap)
				continue
			}
			safeAdd = append(safeAdd, cap)
		}
		if len(safeAdd) > 0 {
			opts = append(opts, oci.WithCapabilities(safeAdd))
		}
		if len(security.Capabilities.Drop) > 0 {
			opts = append(opts, withDroppedCapabilities(security.Capabilities.Drop))
		}
	}

	if security.SeccompProfile != nil && security.SeccompProfile.Type == sandboxv1alpha1.SeccompProfileTypeRuntimeDefault {
		opts = append(opts, oci.WithDefaultUnixDevices)
	}

	if security.AppArmorProfile != nil && security.AppArmorProfile.Type == sandboxv1alpha1.AppArmorProfileTypeLocalhost {
		opts = append(opts, withAppArmorProfile(security.AppArmorProfile.LocalhostProfile))
	}

	return opts
}

// withDroppedCapabilities removes the given capabilities from the bounding,
// effective, permitted, and inheritable sets of the OCI spec process.
// This is the correct way to drop capabilities (oci.WithCapabilities only ADDS).
func withDroppedCapabilities(caps []string) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *spec.Spec) error {
		if s.Process == nil {
			s.Process = &spec.Process{}
		}
		if s.Process.Capabilities == nil {
			s.Process.Capabilities = &spec.LinuxCapabilities{}
		}
		// Build a set of caps to drop for O(1) lookup
		dropSet := make(map[string]struct{}, len(caps))
		for _, c := range caps {
			dropSet[c] = struct{}{}
		}
		// Filter each capability set
		s.Process.Capabilities.Bounding = filterCaps(s.Process.Capabilities.Bounding, dropSet)
		s.Process.Capabilities.Effective = filterCaps(s.Process.Capabilities.Effective, dropSet)
		s.Process.Capabilities.Permitted = filterCaps(s.Process.Capabilities.Permitted, dropSet)
		s.Process.Capabilities.Inheritable = filterCaps(s.Process.Capabilities.Inheritable, dropSet)
		s.Process.Capabilities.Ambient = filterCaps(s.Process.Capabilities.Ambient, dropSet)
		return nil
	}
}

// filterCaps returns caps from src that are NOT in dropSet.
func filterCaps(src []string, dropSet map[string]struct{}) []string {
	if len(src) == 0 {
		return src
	}
	out := src[:0:0]
	for _, c := range src {
		if _, drop := dropSet[c]; !drop {
			out = append(out, c)
		}
	}
	return out
}

func withGID(gid uint32) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, c *containers.Container, s *spec.Spec) error {
		if s.Process == nil {
			s.Process = &spec.Process{}
		}
		s.Process.User.GID = gid
		return nil
	}
}

func withAppArmorProfile(profile string) oci.SpecOpts {
	return oci.WithApparmorProfile(profile)
}

func (c *Client) logPath(sandboxID string) string {
	return fmt.Sprintf("/var/log/nexusbox/sandboxes/%s.log", sandboxID)
}

// parseMilliCPU parses a CPU quantity string to milliCPU.
func parseMilliCPU(s string) int64 {
	// Handles formats like "1", "1.5", "1000m"
	var val float64
	if len(s) > 0 && s[len(s)-1] == 'm' {
		fmt.Sscanf(s[:len(s)-1], "%f", &val)
		return int64(val)
	}
	fmt.Sscanf(s, "%f", &val)
	return int64(val * 1000)
}

// parseMemoryBytes parses a memory quantity string to bytes.
func parseMemoryBytes(s string) int64 {
	var val float64
	suffixes := []struct {
		suffix     string
		multiplier int64
	}{
		{"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10},
		{"G", 1e9}, {"M", 1e6}, {"K", 1e3},
	}
	for _, su := range suffixes {
		if len(s) > len(su.suffix) && s[len(s)-len(su.suffix):] == su.suffix {
			fmt.Sscanf(s[:len(s)-len(su.suffix)], "%f", &val)
			return int64(val * float64(su.multiplier))
		}
	}
	fmt.Sscanf(s, "%f", &val)
	return int64(val)
}
