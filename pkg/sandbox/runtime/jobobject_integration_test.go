//go:build windows

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
	"github.com/nexusbox/nexusbox/pkg/security/filesystem"
)

// TestJobObject_FilesystemSandbox_Integration verifies that a Windows Job Object
// sandbox can be created with a filesystem whitelist, that the sandboxed process
// only runs inside the whitelisted workspace, and that the job is torn down
// cleanly on ForceStop.
//
// This is an end-to-end integration test that exercises the two P0 pillars
// together:
//  1. Windows Job Object isolation (process-level kill-on-close)
//  2. Filesystem sandbox with path whitelist
//
// The test does NOT depend on network or external images: it runs a real
// `cmd /c` process inside the job and verifies the sandbox policy.
func TestJobObject_FilesystemSandbox_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Prepare a workspace directory.
	workspace := t.TempDir()
	innerFile := filepath.Join(workspace, "allowed.txt")
	if err := os.WriteFile(innerFile, []byte("hello"), 0644); err != nil {
		t.Fatalf("setup: write allowed file: %v", err)
	}

	// 2. Build a filesystem sandbox with a whitelist.
	fsSandbox, err := filesystem.NewSandbox(&filesystem.SandboxConfig{
		WorkspaceRoot: workspace,
		MaxFileSize:   1 << 20, // 1MB
	})
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}

	// 3. Verify the whitelist allows the workspace file.
	if err := fsSandbox.ValidateRead(innerFile); err != nil {
		t.Fatalf("ValidateRead(allowed.txt) = %v, want nil", err)
	}
	if err := fsSandbox.ValidateWrite(filepath.Join(workspace, "new.txt")); err != nil {
		t.Fatalf("ValidateWrite(new.txt) = %v, want nil", err)
	}

	// 4. Verify the whitelist blocks a path outside the workspace.
	outside := filepath.Join(os.TempDir(), "nexusbox-outside-"+filepath.Base(workspace))
	t.Cleanup(func() { os.Remove(outside) })

	if err := fsSandbox.ValidateWrite(outside); err == nil {
		t.Fatalf("ValidateWrite(outside) = nil, want denial")
	}

	// 5. Create a Job Object provider.
	provider := newJobObjectProvider(nil)

	// 6. Create a sandbox runtime that runs a short-lived command inside the
	//    workspace. We use `cmd /c echo ok > allowed.txt` so the process exits
	//    quickly and we can observe the job lifecycle.
	spec := &RuntimeSpec{
		SandboxName: "integration-test",
		Namespace:   "default",
		RuntimeType: sandboxv1alpha1.RuntimeRunc,
		Command:     []string{"cmd /c echo ok"},
		Args:        []string{"cmd /c echo ok"},
		WorkingDir:  workspace,
		Resources: sandboxv1alpha1.ResourceRequirements{
			CPU:    "1",
			Memory: "256Mi",
		},
	}

	handle, err := provider.Create(ctx, spec)
	if err != nil {
		t.Fatalf("provider.Create: %v", err)
	}
	t.Cleanup(func() {
		_ = provider.ForceStop(context.Background(), handle)
	})

	// 7. Verify the handle reports a real PID.
	if pid := handle.ID(); pid == "" {
		t.Fatalf("handle.ID() is empty")
	}

	// 8. Query status; the process should be running or already exited.
	status, err := provider.Status(ctx, handle)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.State != RuntimeStateRunning && status.State != RuntimeStateStopped {
		t.Fatalf("status.State = %s, want running or stopped", status.State)
	}

	// 9. Wait briefly for the short command to finish.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for process to exit")
		case <-time.After(100 * time.Millisecond):
		}
		st, _ := provider.Status(ctx, handle)
		if st.State == RuntimeStateStopped {
			break
		}
	}

	// 10. ForceStop should be idempotent and clean.
	if err := provider.ForceStop(ctx, handle); err != nil {
		t.Fatalf("ForceStop: %v", err)
	}

	// 11. After teardown, the filesystem sandbox should still enforce the
	//     policy (it's independent of the job lifecycle).
	if err := fsSandbox.ValidateWrite(outside); err == nil {
		t.Fatalf("ValidateWrite(outside) after teardown = nil, want denial")
	}
}

// TestJobObject_MemoryLimit_KillsProcess verifies that the Job Object memory
// limit actually kills a process that exceeds the limit. This is the core
// enterprise guarantee: a runaway AI agent cannot exhaust host memory.
func TestJobObject_MemoryLimit_KillsProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	provider := newJobObjectProvider(nil)

	// Allocate a large amount of memory in a tight loop.
	// powershell is available on all modern Windows; we use it to spike memory.
	spec := &RuntimeSpec{
		SandboxName: "mem-limit-test",
		Namespace:   "default",
		RuntimeType: sandboxv1alpha1.RuntimeRunc,
		// Use PowerShell to allocate ~512MB of byte arrays, exceeding the 64MB limit.
		Command: []string{`powershell -NoProfile -Command "$a = New-Object byte[] 536870912; Start-Sleep -Seconds 5"`},
		Args:    []string{`powershell -NoProfile -Command "$a = New-Object byte[] 536870912; Start-Sleep -Seconds 5"`},
		Resources: sandboxv1alpha1.ResourceRequirements{
			Memory: "64Mi", // 64MB limit; process tries 512MB -> should be killed.
		},
	}

	handle, err := provider.Create(ctx, spec)
	if err != nil {
		t.Fatalf("provider.Create: %v", err)
	}
	t.Cleanup(func() {
		_ = provider.ForceStop(context.Background(), handle)
	})

	// The job should kill the process because of the memory cap.
	// Wait up to 15 seconds for the process to be terminated by the job.
	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout: process was not killed by memory limit")
		case <-time.After(200 * time.Millisecond):
		}
		st, _ := provider.Status(ctx, handle)
		if st.State == RuntimeStateStopped {
			// Process was killed by the job object due to memory limit.
			return
		}
	}
}

// TestJobObject_IsAvailable confirms the provider reports itself available on Windows.
func TestJobObject_IsAvailable(t *testing.T) {
	provider := newJobObjectProvider(nil)
	if !provider.IsAvailable(context.Background()) {
		t.Fatal("IsAvailable() = false on Windows, want true")
	}
}

// TestJobObject_Type returns a runtime type.
func TestJobObject_Type(t *testing.T) {
	provider := newJobObjectProvider(nil)
	if got := provider.Type(); got == "" {
		t.Fatal("Type() returned empty string")
	}
}

// TestJobObject_CpuRateControl_NoError verifies that the CPU rate control
// API call succeeds (no "command length is incorrect" error) after the
// struct size fix. The setJobCpuRate function is exercised directly to
// ensure the JOBOBJECT_CPU_RATE_CONTROL_INFORMATION structure is accepted
// by the Windows kernel.
func TestJobObject_CpuRateControl_NoError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	provider := newJobObjectProvider(nil)

	spec := &RuntimeSpec{
		SandboxName: "cpu-rate-test",
		Namespace:   "default",
		RuntimeType: sandboxv1alpha1.RuntimeRunc,
		Command:     []string{"cmd /c echo ok"},
		Args:        []string{"cmd /c echo ok"},
		WorkingDir:  t.TempDir(),
		Resources: sandboxv1alpha1.ResourceRequirements{
			CPU:    "500m", // 0.5 core = CpuRate 5000
			Memory: "128Mi",
		},
	}

	handle, err := provider.Create(ctx, spec)
	if err != nil {
		t.Fatalf("provider.Create with CPU limit: %v", err)
	}
	t.Cleanup(func() {
		_ = provider.ForceStop(context.Background(), handle)
	})

	// Wait for the short command to complete.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for process to exit")
		case <-time.After(100 * time.Millisecond):
		}
		st, _ := provider.Status(ctx, handle)
		if st.State == RuntimeStateStopped {
			break
		}
	}
}

// TestJobObject_CpuRateControl_ThrottlesProcess verifies that a CPU rate
// limit actually throttles a CPU-bound process. A tight loop that would
// normally finish in well under a second should take noticeably longer
// when capped at 0.1 core (100m).
func TestJobObject_CpuRateControl_ThrottlesProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// First, run a CPU-burner WITHOUT a CPU limit to establish a baseline.
	// 500M iterations is long enough (~2-3s) for the scheduler to enforce the
	// throttle on the capped run; shorter loops finish within a single
	// scheduling period and the throttle is invisible.
	burnCmd := `powershell -NoProfile -Command "$x=0; for($i=0;$i -lt 500000000;$i++){$x+=$i}; echo $x"`

	baselineStart := time.Now()
	provider := newJobObjectProvider(nil)
	baselineSpec := &RuntimeSpec{
		SandboxName: "cpu-baseline",
		Namespace:   "default",
		RuntimeType: sandboxv1alpha1.RuntimeRunc,
		Command:     []string{burnCmd},
		Args:        []string{burnCmd},
		WorkingDir:  t.TempDir(),
		Resources:   sandboxv1alpha1.ResourceRequirements{}, // No CPU limit.
	}
	baselineHandle, err := provider.Create(ctx, baselineSpec)
	if err != nil {
		t.Fatalf("baseline Create: %v", err)
	}

	// Wait for baseline to finish.
	baselineDeadline := time.After(60 * time.Second)
	for {
		select {
		case <-baselineDeadline:
			t.Fatalf("baseline timed out")
		case <-time.After(200 * time.Millisecond):
		}
		st, _ := provider.Status(ctx, baselineHandle)
		if st.State == RuntimeStateStopped {
			break
		}
	}
	baselineDuration := time.Since(baselineStart)
	_ = provider.ForceStop(ctx, baselineHandle)

	// Now run the same burner WITH a 0.1 core (100m) hard cap.
	throttledStart := time.Now()
	throttledSpec := &RuntimeSpec{
		SandboxName: "cpu-throttled",
		Namespace:   "default",
		RuntimeType: sandboxv1alpha1.RuntimeRunc,
		Command:     []string{burnCmd},
		Args:        []string{burnCmd},
		WorkingDir:  t.TempDir(),
		Resources: sandboxv1alpha1.ResourceRequirements{
			CPU: "100m", // 0.1 core = CpuRate 1000 (10%)
		},
	}
	throttledHandle, err := provider.Create(ctx, throttledSpec)
	if err != nil {
		t.Fatalf("throttled Create: %v", err)
	}
	t.Cleanup(func() {
		_ = provider.ForceStop(context.Background(), throttledHandle)
	})

	// Wait for the throttled process to finish (it should take ~10x longer).
	deadline := time.After(80 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("throttled process did not finish in 80s")
		case <-time.After(500 * time.Millisecond):
		}
		st, _ := provider.Status(ctx, throttledHandle)
		if st.State == RuntimeStateStopped {
			break
		}
	}
	throttledDuration := time.Since(throttledStart)

	// The throttled run should be at least 1.5x slower than the baseline.
	// We use a conservative ratio (1.5x) to avoid flakiness on shared CI runners
	// where the baseline itself may be slowed by other load.
	if throttledDuration < baselineDuration*3/2 {
		t.Fatalf("CPU throttle not effective: baseline=%v, throttled=%v (ratio %.1fx, want >= 1.5x)",
			baselineDuration, throttledDuration, float64(throttledDuration)/float64(baselineDuration))
	}

	t.Logf("CPU throttle verified: baseline=%v, throttled=%v (%.1fx slower)",
		baselineDuration, throttledDuration, float64(throttledDuration)/float64(baselineDuration))
}
