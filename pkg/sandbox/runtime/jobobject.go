//go:build windows

/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
*/

package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"golang.org/x/sys/windows"
	"k8s.io/klog/v2"
)

// JobObject limits constants (Windows API).
const (
	jobObjectLimitKillOnJobClose = 0x2000
	jobObjectLimitProcessMemory  = 0x100
	jobObjectLimitActiveProcess  = 0x0008
	jobObjectLimitBreakawayOk    = 0x0040
)

// CPU rate control flags (Windows API, ControlFlags field).
const (
	cpuRateControlEnable      = 0x1 // JOB_OBJECT_CPU_RATE_CONTROL_ENABLE
	cpuRateControlWeightBased = 0x2 // JOB_OBJECT_CPU_RATE_CONTROL_WEIGHT_BASED
	cpuRateControlHardCap     = 0x4 // JOB_OBJECT_CPU_RATE_CONTROL_HARD_CAP
)

// JobObjectInformation classes for SetInformationJobObject.
const (
	jobObjectExtendedLimitInformation  = 9
	jobObjectCpuRateControlInformation = 15
	jobObjectBasicLimitInformation     = 2
)

// jobObjectProvider implements RuntimeProvider using Windows Job Objects.
type jobObjectProvider struct {
	mu      sync.RWMutex
	config  *RuntimeManagerConfig
	handles map[string]*jobObjectHandle
}

// jobObjectHandle implements RuntimeHandle for a Windows Job Object sandbox.
type jobObjectHandle struct {
	mu        sync.RWMutex
	id        string
	spec      *RuntimeSpec
	ready     bool
	pid       int
	jobHandle windows.Handle
	cmd       *exec.Cmd
	createdAt time.Time
	exitCh    chan int
}

// newJobObjectProvider creates a new Windows Job Object provider.
func newJobObjectProvider(config *RuntimeManagerConfig) *jobObjectProvider {
	if config == nil {
		config = DefaultRuntimeManagerConfig()
	}
	return &jobObjectProvider{
		config:  config,
		handles: make(map[string]*jobObjectHandle),
	}
}

// Type returns the runtime type.
func (p *jobObjectProvider) Type() sandboxv1alpha1.SandboxRuntimeType {
	return sandboxv1alpha1.RuntimeRunc
}

// IsAvailable returns true on Windows (job objects are a core OS feature).
func (p *jobObjectProvider) IsAvailable(ctx context.Context) bool {
	return true
}

// Create creates a new sandbox runtime backed by a Windows Job Object.
func (p *jobObjectProvider) Create(ctx context.Context, spec *RuntimeSpec) (RuntimeHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Create the Job Object with kill-on-close semantics.
	jobHandle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create job object: %w", err)
	}

	// Set basic limits: kill on job close.
	basicLimit := struct {
		PerProcessUserTimeLimit int64
		PerJobUserTimeLimit     int64
		LimitFlags              uint32
		MinimumWorkingSetSize   uintptr
		MaximumWorkingSetSize   uintptr
		ActiveProcessLimit      uint32
		Affinity                uintptr
		PriorityClass           uint32
		SchedulingClass         uint32
	}{
		LimitFlags:         jobObjectLimitKillOnJobClose,
		ActiveProcessLimit: 256,
		PriorityClass:      windows.NORMAL_PRIORITY_CLASS,
	}

	// Set extended limits (memory + kill-on-close).
	extLimit := struct {
		BasicLimitInformation struct {
			PerProcessUserTimeLimit int64
			PerJobUserTimeLimit     int64
			LimitFlags              uint32
			MinimumWorkingSetSize   uintptr
			MaximumWorkingSetSize   uintptr
			ActiveProcessLimit      uint32
			Affinity                uintptr
			PriorityClass           uint32
			SchedulingClass         uint32
		}
		IoInfo struct {
			ReadOperationCount  uint64
			WriteOperationCount uint64
			OtherOperationCount uint64
			ReadTransferCount   uint64
			WriteTransferCount  uint64
			OtherTransferCount  uint64
		}
		ProcessMemoryLimit    uintptr
		JobMemoryLimit        uintptr
		PeakProcessMemoryUsed uintptr
		PeakJobMemoryUsed     uintptr
	}{}

	extLimit.BasicLimitInformation = basicLimit
	extLimit.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose | jobObjectLimitActiveProcess

	// Apply memory limit if specified.
	if spec.Resources.Memory != "" {
		memBytes := parseMemoryToBytes(spec.Resources.Memory)
		if memBytes > 0 {
			extLimit.BasicLimitInformation.LimitFlags |= jobObjectLimitProcessMemory
			extLimit.ProcessMemoryLimit = uintptr(memBytes)
			extLimit.JobMemoryLimit = uintptr(memBytes)
		}
	}

	// Apply CPU rate limit if specified. CPU rate control is enabled solely
	// by calling SetInformationJobObject with JobObjectCpuRateControlInformation
	// and the JOB_OBJECT_CPU_RATE_CONTROL_ENABLE flag; it does NOT require (and
	// must not use) the JOB_OBJECT_LIMIT_CPU_RATE_CONTROL bit in the extended
	// limits' LimitFlags. That bit shares the same value (0x10) as
	// JOB_OBJECT_LIMIT_AFFINITY, so setting it makes the kernel validate the
	// Affinity field (which is 0) and reject the call with ERROR_INVALID_PARAMETER.
	if spec.Resources.CPU != "" {
		if err := setJobCpuRate(jobHandle, spec.Resources.CPU); err != nil {
			windows.CloseHandle(jobHandle)
			return nil, fmt.Errorf("failed to set CPU rate: %w", err)
		}
	}

	if err := setJobExtendedLimits(jobHandle, &extLimit); err != nil {
		windows.CloseHandle(jobHandle)
		return nil, fmt.Errorf("failed to set job limits: %w", err)
	}

	// Build the command to run inside the sandbox.
	cmd := exec.CommandContext(ctx, "cmd", "/c", buildJobCommand(spec))
	cmd.Dir = spec.WorkingDir
	if cmd.Dir == "" {
		cmd.Dir, _ = os.Getwd()
	}
	cmd.Env = buildJobEnv(spec.Env)

	// Create the process suspended so we can assign it to the job before it runs.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_SUSPENDED,
	}

	if err := cmd.Start(); err != nil {
		windows.CloseHandle(jobHandle)
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	pid := cmd.Process.Pid

	// Assign the process to the job object.
	procHandle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.PROCESS_SET_QUOTA|windows.PROCESS_SET_INFORMATION, false, uint32(pid))
	if err != nil {
		_ = cmd.Process.Kill()
		windows.CloseHandle(jobHandle)
		return nil, fmt.Errorf("failed to open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(procHandle)

	if err := windows.AssignProcessToJobObject(jobHandle, procHandle); err != nil {
		_ = cmd.Process.Kill()
		windows.CloseHandle(jobHandle)
		return nil, fmt.Errorf("failed to assign process %d to job: %w", pid, err)
	}

	// Resume the main thread.
	if err := resumeProcessMainThread(pid); err != nil {
		klog.Warningf("Failed to resume process %d: %v", pid, err)
	}

	handle := &jobObjectHandle{
		id:        fmt.Sprintf("%s/%s", spec.SandboxName, spec.Namespace),
		spec:      spec,
		ready:     true,
		pid:       pid,
		jobHandle: jobHandle,
		cmd:       cmd,
		createdAt: time.Now(),
		exitCh:    make(chan int, 1),
	}

	p.handles[handle.id] = handle

	// Wait for the process in a goroutine.
	go func() {
		err := cmd.Wait()
		exitCode := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		handle.exitCh <- exitCode
		close(handle.exitCh)
		handle.mu.Lock()
		handle.ready = false
		handle.mu.Unlock()
	}()

	klog.Infof("Created Windows Job Object sandbox %s (pid=%d)", handle.id, pid)
	return handle, nil
}

// Start is not supported (job objects don't support restart).
func (p *jobObjectProvider) Start(ctx context.Context, handle RuntimeHandle) error {
	return fmt.Errorf("Start is not supported for Windows Job Object sandboxes")
}

// Stop terminates all processes in the job.
func (p *jobObjectProvider) Stop(ctx context.Context, handle RuntimeHandle) error {
	return p.ForceStop(ctx, handle)
}

// ForceStop forcefully terminates all processes in the job.
func (p *jobObjectProvider) ForceStop(ctx context.Context, handle RuntimeHandle) error {
	jh, ok := handle.(*jobObjectHandle)
	if !ok {
		return fmt.Errorf("invalid handle type for jobObjectProvider")
	}

	jh.mu.Lock()
	defer jh.mu.Unlock()

	if !jh.ready {
		return nil
	}

	// Terminate all processes in the job with exit code 1.
	if err := windows.TerminateJobObject(jh.jobHandle, 1); err != nil {
		klog.Warningf("Failed to terminate job for %s: %v", jh.id, err)
	}

	jh.ready = false
	klog.Infof("Stopped Windows Job Object sandbox %s", jh.id)
	return nil
}

// Pause is not supported on Windows Job Objects.
func (p *jobObjectProvider) Pause(ctx context.Context, handle RuntimeHandle) error {
	return fmt.Errorf("Pause is not supported for Windows Job Object sandboxes")
}

// Resume is not supported on Windows Job Objects.
func (p *jobObjectProvider) Resume(ctx context.Context, handle RuntimeHandle) error {
	return fmt.Errorf("Resume is not supported for Windows Job Object sandboxes")
}

// Status returns the status of a sandbox runtime.
func (p *jobObjectProvider) Status(ctx context.Context, handle RuntimeHandle) (*RuntimeStatus, error) {
	jh, ok := handle.(*jobObjectHandle)
	if !ok {
		return nil, fmt.Errorf("invalid handle type for jobObjectProvider")
	}

	jh.mu.RLock()
	defer jh.mu.RUnlock()

	state := RuntimeStateRunning
	if !jh.ready {
		state = RuntimeStateStopped
	}

	return &RuntimeStatus{
		State:     state,
		PID:       jh.pid,
		StartedAt: jh.createdAt,
	}, nil
}

// Stats returns resource usage statistics.
func (p *jobObjectProvider) Stats(ctx context.Context, handle RuntimeHandle) (*RuntimeStats, error) {
	jh, ok := handle.(*jobObjectHandle)
	if !ok {
		return nil, fmt.Errorf("invalid handle type for jobObjectProvider")
	}

	// Query basic accounting info from the job object.
	var accounting struct {
		TotalUserTime   windows.Filetime
		TotalKernelTime windows.Filetime
		TotalPageFaults uint32
		TotalProcesses  uint32
		TotalTerminated uint32
	}

	err := queryJobBasicAccounting(jh.jobHandle, &accounting)
	if err != nil {
		return nil, fmt.Errorf("failed to query job accounting: %w", err)
	}

	userTime := accounting.TotalUserTime.Nanoseconds()
	kernelTime := accounting.TotalKernelTime.Nanoseconds()

	return &RuntimeStats{
		CPUUsageNanoCores: uint64(userTime + kernelTime),
		CollectedAt:       time.Now(),
	}, nil
}

// ID returns the runtime identifier.
func (h *jobObjectHandle) ID() string { return h.id }

// IsReady returns whether the runtime is ready.
func (h *jobObjectHandle) IsReady() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.ready
}

// GetSpec returns the runtime specification.
func (h *jobObjectHandle) GetSpec() *RuntimeSpec { return h.spec }

// ForceStop forcefully stops the runtime.
func (h *jobObjectHandle) ForceStop(ctx context.Context) error {
	if h.jobHandle != 0 {
		_ = windows.TerminateJobObject(h.jobHandle, 1)
	}
	h.mu.Lock()
	h.ready = false
	h.mu.Unlock()
	return nil
}

// Cleanup cleans up runtime resources.
func (h *jobObjectHandle) Cleanup(ctx context.Context) error {
	if h.jobHandle != 0 {
		_ = windows.CloseHandle(h.jobHandle)
		h.jobHandle = 0
	}
	return nil
}

// setJobExtendedLimits sets extended limits on a job object.
func setJobExtendedLimits(job windows.Handle, limits *struct {
	BasicLimitInformation struct {
		PerProcessUserTimeLimit int64
		PerJobUserTimeLimit     int64
		LimitFlags              uint32
		MinimumWorkingSetSize   uintptr
		MaximumWorkingSetSize   uintptr
		ActiveProcessLimit      uint32
		Affinity                uintptr
		PriorityClass           uint32
		SchedulingClass         uint32
	}
	IoInfo struct {
		ReadOperationCount  uint64
		WriteOperationCount uint64
		OtherOperationCount uint64
		ReadTransferCount   uint64
		WriteTransferCount  uint64
		OtherTransferCount  uint64
	}
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}) error {
	_, _, err := windows.NewLazyDLL("kernel32.dll").NewProc("SetInformationJobObject").Call(
		uintptr(job),
		uintptr(jobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(limits)),
		unsafe.Sizeof(*limits),
	)
	if err != windows.Errno(0) {
		return err
	}
	return nil
}

// setJobCpuRate configures CPU rate control on the job object.
//
// The Windows JOBOBJECT_CPU_RATE_CONTROL_INFORMATION structure is defined in
// the SDK headers as 16 bytes (ControlFlags + a union of CpuRate/Weight and
// MinRate/MaxRate), but the kernel on Windows 8 through Windows 11 only
// accepts the first 8 bytes (ControlFlags + CpuRate). Passing 16 bytes
// triggers ERROR_BAD_LENGTH ("The program issued a command but the command
// length is incorrect") because the kernel's expected struct size is 8.
//
// ControlFlags must include JOB_OBJECT_CPU_RATE_CONTROL_ENABLE (0x1). For a
// hard cap (recommended for sandbox resource limits) also set
// JOB_OBJECT_CPU_RATE_CONTROL_HARD_CAP (0x4).
func setJobCpuRate(job windows.Handle, cpu string) error {
	milliCPU := parseCPUMilli(cpu)
	if milliCPU <= 0 {
		return nil
	}

	// Windows CPU rate is in units of 1/10000 of a core per scheduling period.
	// e.g., 10000 = 1 full core, 5000 = 0.5 core.
	// For milliCPU: rate = milliCPU * 10000 / 1000 = milliCPU * 10
	rate := uint32(milliCPU * 10)
	if rate < 1 {
		rate = 1
	}
	if rate > 10000 {
		rate = 10000
	}

	// JOBOBJECT_CPU_RATE_CONTROL_INFORMATION as accepted by the kernel (8 bytes).
	type cpuRateControlInfo struct {
		ControlFlags uint32
		CpuRate      uint32
	}

	info := cpuRateControlInfo{
		// Enable rate-based control with a hard cap so processes cannot
		// exceed the quota even when the CPU would otherwise be idle.
		ControlFlags: cpuRateControlEnable | cpuRateControlHardCap,
		CpuRate:      rate,
	}

	ret, err := windows.SetInformationJobObject(
		job,
		jobObjectCpuRateControlInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		return fmt.Errorf("SetInformationJobObject(CpuRateControl): %w", err)
	}
	if ret == 0 {
		return fmt.Errorf("SetInformationJobObject(CpuRateControl) returned 0")
	}
	return nil
}

// queryJobBasicAccounting queries basic accounting info from the job object.
func queryJobBasicAccounting(job windows.Handle, out interface{}) error {
	_, _, err := windows.NewLazyDLL("kernel32.dll").NewProc("QueryInformationJobObject").Call(
		uintptr(job),
		uintptr(1), // JobObjectBasicAccountingInformation = 1
		uintptr(unsafe.Pointer(&out)),
		unsafe.Sizeof(out),
		0,
	)
	if err != windows.Errno(0) {
		return err
	}
	return nil
}

// resumeProcessMainThread resumes the main thread of a process.
func resumeProcessMainThread(pid int) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("CreateToolhelp32Snapshot failed: %w", err)
	}
	defer windows.CloseHandle(snap)

	var entry windows.ThreadEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	err = windows.Thread32First(snap, &entry)
	if err != nil {
		return fmt.Errorf("Thread32First failed: %w", err)
	}

	for {
		if entry.OwnerProcessID == uint32(pid) {
			th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err == nil {
				_, err = windows.ResumeThread(th)
				windows.CloseHandle(th)
				return err
			}
		}
		err = windows.Thread32Next(snap, &entry)
		if err != nil {
			break
		}
	}
	return nil
}

// buildJobCommand constructs the shell command string from the spec.
func buildJobCommand(spec *RuntimeSpec) string {
	if len(spec.Args) > 0 {
		return spec.Args[0]
	}
	if len(spec.Command) > 0 {
		return spec.Command[0]
	}
	// Default keep-alive command.
	return "ping -t 127.0.0.1 > nul"
}

// buildJobEnv constructs the environment for the child process.
func buildJobEnv(userEnv map[string]string) []string {
	env := os.Environ()
	for k, v := range userEnv {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}

// parseCPUMilli parses a Kubernetes-style CPU quantity into milliCPU.
func parseCPUMilli(cpu string) int64 {
	if cpu == "" {
		return 0
	}
	if cpu[len(cpu)-1] == 'm' {
		var val int64
		fmt.Sscanf(cpu[:len(cpu)-1], "%d", &val)
		return val
	}
	var val int64
	fmt.Sscanf(cpu, "%d", &val)
	return val * 1000
}

// parseMemoryToBytes parses a Kubernetes-style memory quantity into bytes.
func parseMemoryToBytes(mem string) int64 {
	if mem == "" {
		return 0
	}
	suffixes := []struct {
		suffix     string
		multiplier int64
	}{
		{"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10},
		{"G", 1e9}, {"M", 1e6}, {"K", 1e3},
	}
	for _, su := range suffixes {
		if len(mem) > len(su.suffix) && mem[len(mem)-len(su.suffix):] == su.suffix {
			var val int64
			fmt.Sscanf(mem[:len(mem)-len(su.suffix)], "%d", &val)
			return val * su.multiplier
		}
	}
	var val int64
	fmt.Sscanf(mem, "%d", &val)
	return val
}

// Ensure jobObjectProvider satisfies the RuntimeProvider interface.
var _ RuntimeProvider = (*jobObjectProvider)(nil)

// RegisterJobObjectProvider is the public constructor used by the runtime manager
// to register the Windows Job Object backend.
func RegisterJobObjectProvider(rm *RuntimeManager) error {
	provider := newJobObjectProvider(rm.config)
	rm.RegisterProvider(provider)
	klog.Info("Registered Windows Job Object runtime provider")
	return nil
}
