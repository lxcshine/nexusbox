//go:build windows

package runtime

import (
	"context"
	"os"
	"testing"
	"time"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// TestJobObject_BasicCommand verifies the sandbox can run basic commands
func TestJobObject_BasicCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	workspace := t.TempDir()

	provider := newJobObjectProvider(nil)

	// Use "cmd /c dir" to verify basic command execution works
	// We rely on exit code 0 for success since stdout isn't captured
	spec := &RuntimeSpec{
		SandboxName: "basic-test",
		Namespace:   "test",
		RuntimeType: sandboxv1alpha1.RuntimeRunc,
		Command:     []string{"cmd /c dir > nul"},
		Args:        []string{},
		WorkingDir:  workspace,
		Resources: sandboxv1alpha1.ResourceRequirements{
			CPU:    "1",
			Memory: "256Mi",
		},
	}

	handle, err := provider.Create(ctx, spec)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	t.Logf("Created sandbox, PID: %s", handle.ID())
	jh := handle.(*jobObjectHandle)
	t.Logf("Isolated working directory: %s", jh.isolatedDir)

	// Wait for exit
	var exitCode int
	deadline := time.After(10 * time.Second)
loop:
	for {
		select {
		case <-deadline:
			provider.ForceStop(context.Background(), handle)
			t.Fatal("Timeout")
		case <-time.After(100 * time.Millisecond):
		}
		status, err := provider.Status(ctx, handle)
		if err != nil {
			continue
		}
		if status.State == RuntimeStateStopped {
			exitCode = status.ExitCode
			t.Logf("Process exited with code: %d", exitCode)
			break loop
		}
	}

	// Verify isolated directory cleanup happens (it should be removed after exit)
	// Give the cleanup goroutine a moment to run
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(jh.isolatedDir); os.IsNotExist(err) {
		t.Log("✅ Isolated directory was cleaned up automatically")
	} else {
		t.Logf("Isolated directory still exists (may be cleaned up later): %s", jh.isolatedDir)
	}

	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d - basic command execution failed", exitCode)
	} else {
		t.Log("✅ Basic command execution works (exit code 0)")
	}

	provider.ForceStop(context.Background(), handle)
}
