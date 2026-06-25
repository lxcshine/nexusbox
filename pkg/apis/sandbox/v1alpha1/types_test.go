package v1alpha1

import (
	"testing"
)

func TestSandboxPhaseIsTerminal(t *testing.T) {
	tests := []struct {
		phase     SandboxPhase
		terminal  bool
	}{
		{SandboxPending, false},
		{SandboxScheduling, false},
		{SandboxCreating, false},
		{SandboxRunning, false},
		{SandboxPaused, false},
		{SandboxStopped, false},
		{SandboxFailed, true},
		{SandboxEvicted, true},
	}

	for _, tt := range tests {
		got := tt.phase.IsTerminal()
		if got != tt.terminal {
			t.Errorf("SandboxPhase(%s).IsTerminal() = %v, want %v", tt.phase, got, tt.terminal)
		}
	}
}

func TestTenantPhaseIsAvailable(t *testing.T) {
	tests := []struct {
		phase     TenantPhase
		available bool
	}{
		{TenantPending, false},
		{TenantActive, true},
		{TenantSuspended, false},
		{TenantTerminating, false},
	}

	for _, tt := range tests {
		got := tt.phase.IsAvailable()
		if got != tt.available {
			t.Errorf("TenantPhase(%s).IsAvailable() = %v, want %v", tt.phase, got, tt.available)
		}
	}
}

func TestSandboxRuntimeTypeConstants(t *testing.T) {
	if RuntimeKataContainers != "kata-containers" {
		t.Errorf("RuntimeKataContainers = %q, want %q", RuntimeKataContainers, "kata-containers")
	}
	if RuntimeGVisor != "gvisor" {
		t.Errorf("RuntimeGVisor = %q, want %q", RuntimeGVisor, "gvisor")
	}
	if RuntimeRunc != "runc" {
		t.Errorf("RuntimeRunc = %q, want %q", RuntimeRunc, "runc")
	}
}

func TestSandboxPriorityConstants(t *testing.T) {
	if PriorityLow != 0 {
		t.Errorf("PriorityLow = %d, want 0", PriorityLow)
	}
	if PriorityNormal != 50 {
		t.Errorf("PriorityNormal = %d, want 50", PriorityNormal)
	}
	if PriorityHigh != 75 {
		t.Errorf("PriorityHigh = %d, want 75", PriorityHigh)
	}
	if PriorityCritical != 100 {
		t.Errorf("PriorityCritical = %d, want 100", PriorityCritical)
	}
}

func TestSandboxSpecDefaults(t *testing.T) {
	sb := &Sandbox{}
	if sb.Spec.Runtime != "" {
		t.Errorf("default Runtime should be empty, got %q", sb.Spec.Runtime)
	}
	if sb.Spec.Priority != 0 {
		t.Errorf("default Priority should be 0, got %d", sb.Spec.Priority)
	}
}

func TestSandboxStatusConditions(t *testing.T) {
	conditions := []SandboxCondition{
		{
			Type:   SandboxConditionScheduled,
			Status: ConditionTrue,
		},
		{
			Type:   SandboxConditionReady,
			Status: ConditionFalse,
		},
	}

	status := SandboxStatus{
		Phase:      SandboxRunning,
		Conditions: conditions,
	}

	if len(status.Conditions) != 2 {
		t.Errorf("expected 2 conditions, got %d", len(status.Conditions))
	}
	if status.Conditions[0].Type != SandboxConditionScheduled {
		t.Errorf("condition[0].Type = %q, want %q", status.Conditions[0].Type, SandboxConditionScheduled)
	}
	if status.Conditions[1].Status != ConditionFalse {
		t.Errorf("condition[1].Status = %q, want %q", status.Conditions[1].Status, ConditionFalse)
	}
}

func TestResourceRequirements(t *testing.T) {
	res := ResourceRequirements{
		CPU:    "2",
		Memory: "4Gi",
		GPU:    "1",
	}
	if res.CPU != "2" {
		t.Errorf("CPU = %q, want %q", res.CPU, "2")
	}
	if res.Memory != "4Gi" {
		t.Errorf("Memory = %q, want %q", res.Memory, "4Gi")
	}
}

func TestNetworkModeConstants(t *testing.T) {
	if NetworkModeBridge != "Bridge" {
		t.Errorf("NetworkModeBridge = %q, want %q", NetworkModeBridge, "Bridge")
	}
	if NetworkModeHost != "Host" {
		t.Errorf("NetworkModeHost = %q, want %q", NetworkModeHost, "Host")
	}
	if NetworkModeNone != "None" {
		t.Errorf("NetworkModeNone = %q, want %q", NetworkModeNone, "None")
	}
}

func TestIsolationLevelConstants(t *testing.T) {
	if IsolationLevelStandard != "Standard" {
		t.Errorf("IsolationLevelStandard = %q, want %q", IsolationLevelStandard, "Standard")
	}
	if IsolationLevelEnhanced != "Enhanced" {
		t.Errorf("IsolationLevelEnhanced = %q, want %q", IsolationLevelEnhanced, "Enhanced")
	}
	if IsolationLevelMaximum != "Maximum" {
		t.Errorf("IsolationLevelMaximum = %q, want %q", IsolationLevelMaximum, "Maximum")
	}
}

func TestTenantResourceQuota(t *testing.T) {
	quota := TenantResourceQuota{
		CPU:         "8",
		Memory:      "16Gi",
		MaxInstances: 100,
	}
	if quota.MaxInstances != 100 {
		t.Errorf("MaxInstances = %d, want 100", quota.MaxInstances)
	}
}

func TestSandboxSecuritySpec(t *testing.T) {
	uid := int64(1000)
	gid := int64(1000)
	security := &SandboxSecuritySpec{
		RunAsUser:             &uid,
		RunAsGroup:            &gid,
		ReadOnlyRootFilesystem: true,
	}
	if *security.RunAsUser != 1000 {
		t.Errorf("RunAsUser = %d, want 1000", *security.RunAsUser)
	}
	if !security.ReadOnlyRootFilesystem {
		t.Error("ReadOnlyRootFilesystem should be true")
	}
}

func TestBatchSchedulingInfo(t *testing.T) {
	batch := &BatchSchedulingInfo{
		BatchID:        "batch-001",
		BatchSize:      10,
		GangScheduling: true,
		MinAvailable:   8,
	}
	if batch.BatchID != "batch-001" {
		t.Errorf("BatchID = %q, want %q", batch.BatchID, "batch-001")
	}
	if !batch.GangScheduling {
		t.Error("GangScheduling should be true")
	}
}
