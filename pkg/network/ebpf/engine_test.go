package ebpf

import (
	"context"
	"testing"
)

func TestNewEngine_NoopBackend(t *testing.T) {
	e := NewEngine(&EngineConfig{BackendType: "noop"})
	if e == nil {
		t.Fatal("NewEngine returned nil")
	}
	if e.backend.Name() != "noop" {
		t.Errorf("expected backend=noop, got %s", e.backend.Name())
	}
}

func TestNewEngine_AutoDetect(t *testing.T) {
	// Auto-detect should always pick something
	e := NewEngine(&EngineConfig{})
	if e == nil {
		t.Fatal("NewEngine returned nil")
	}
	name := e.backend.Name()
	if name != "ebpf" && name != "iptables" && name != "noop" {
		t.Errorf("unexpected backend name: %s", name)
	}
}

func TestEngine_Init(t *testing.T) {
	e := NewEngine(&EngineConfig{BackendType: "noop"})
	if err := e.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if e.stats.Backend != "noop" {
		t.Errorf("expected stats.Backend=noop, got %s", e.stats.Backend)
	}
}

func TestEngine_SetPolicy_Validation(t *testing.T) {
	e := NewEngine(&EngineConfig{BackendType: "noop"})
	if err := e.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	ctx := context.Background()

	cases := []struct {
		name   string
		policy *NetworkPolicy
		err    bool
	}{
		{
			name:   "missing sandboxID",
			policy: &NetworkPolicy{SandboxIP: "10.0.0.1"},
			err:    true,
		},
		{
			name:   "missing sandboxIP",
			policy: &NetworkPolicy{SandboxID: "sb-1"},
			err:    true,
		},
		{
			name: "invalid ingress CIDR",
			policy: &NetworkPolicy{
				SandboxID: "sb-1",
				SandboxIP: "10.0.0.1",
				Ingress:   []IngressRule{{FromCIDR: "not-a-cidr"}},
			},
			err: true,
		},
		{
			name: "invalid egress CIDR",
			policy: &NetworkPolicy{
				SandboxID: "sb-1",
				SandboxIP: "10.0.0.1",
				Egress:    []EgressRule{{ToCIDR: "999.999.999.999/32"}},
			},
			err: true,
		},
		{
			name: "valid policy",
			policy: &NetworkPolicy{
				SandboxID: "sb-1",
				SandboxIP: "10.0.0.1",
				Ingress: []IngressRule{
					{
						FromCIDR:  "10.0.0.0/8",
						Ports:     []PortRange{{Start: 80, End: 80}},
						Protocols: []string{"tcp"},
					},
				},
				Egress: []EgressRule{
					{
						ToCIDR:    "0.0.0.0/0",
						Protocols: []string{"tcp"},
					},
				},
				DefaultDeny: true,
			},
			err: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := e.SetPolicy(ctx, c.policy)
			if c.err && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !c.err && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

func TestEngine_GetPolicy(t *testing.T) {
	e := NewEngine(&EngineConfig{BackendType: "noop"})
	if err := e.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	ctx := context.Background()

	policy := &NetworkPolicy{
		SandboxID:   "sb-get",
		SandboxIP:   "10.0.0.5",
		DefaultDeny: true,
	}
	if err := e.SetPolicy(ctx, policy); err != nil {
		t.Fatalf("SetPolicy failed: %v", err)
	}

	got := e.GetPolicy("sb-get")
	if got == nil {
		t.Fatal("expected policy, got nil")
	}
	if got.SandboxIP != "10.0.0.5" {
		t.Errorf("expected IP=10.0.0.5, got %s", got.SandboxIP)
	}

	if e.GetPolicy("missing") != nil {
		t.Error("expected nil for missing policy")
	}
}

func TestEngine_RemovePolicy(t *testing.T) {
	e := NewEngine(&EngineConfig{BackendType: "noop"})
	if err := e.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	ctx := context.Background()

	policy := &NetworkPolicy{
		SandboxID: "sb-rm",
		SandboxIP: "10.0.0.6",
	}
	if err := e.SetPolicy(ctx, policy); err != nil {
		t.Fatalf("SetPolicy failed: %v", err)
	}

	if err := e.RemovePolicy(ctx, "sb-rm"); err != nil {
		t.Fatalf("RemovePolicy failed: %v", err)
	}

	if e.GetPolicy("sb-rm") != nil {
		t.Error("expected nil after remove")
	}

	// Removing again should be no-op
	if err := e.RemovePolicy(ctx, "sb-rm"); err != nil {
		t.Errorf("second remove should be no-op, got: %v", err)
	}
}

func TestEngine_ListPolicies(t *testing.T) {
	e := NewEngine(&EngineConfig{BackendType: "noop"})
	if err := e.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	ctx := context.Background()

	for i, id := range []string{"sb-1", "sb-2", "sb-3"} {
		if err := e.SetPolicy(ctx, &NetworkPolicy{
			SandboxID: id,
			SandboxIP: "10.0.0." + string(rune('0'+i)),
		}); err != nil {
			t.Fatalf("SetPolicy %s failed: %v", id, err)
		}
	}

	list := e.ListPolicies()
	if len(list) != 3 {
		t.Fatalf("expected 3 policies, got %d", len(list))
	}
}

func TestEngine_GetStats(t *testing.T) {
	e := NewEngine(&EngineConfig{BackendType: "noop"})
	if err := e.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	ctx := context.Background()

	_ = e.SetPolicy(ctx, &NetworkPolicy{
		SandboxID: "sb-stats",
		SandboxIP: "10.0.0.7",
	})

	stats := e.GetStats()
	if stats.TotalPolicies != 1 {
		t.Errorf("expected TotalPolicies=1, got %d", stats.TotalPolicies)
	}
	if stats.EnforcedPolicies != 1 {
		t.Errorf("expected EnforcedPolicies=1, got %d", stats.EnforcedPolicies)
	}
	if stats.Backend != "noop" {
		t.Errorf("expected Backend=noop, got %s", stats.Backend)
	}
}

func TestNoopBackend_AllOperations(t *testing.T) {
	b := NewNoopBackend()
	if b.Name() != "noop" {
		t.Errorf("expected name=noop, got %s", b.Name())
	}
	if err := b.Init(); err != nil {
		t.Errorf("Init failed: %v", err)
	}
	ctx := context.Background()
	if err := b.ApplyPolicy(ctx, &NetworkPolicy{SandboxID: "x"}); err != nil {
		t.Errorf("ApplyPolicy failed: %v", err)
	}
	if err := b.RemovePolicy(ctx, "x"); err != nil {
		t.Errorf("RemovePolicy failed: %v", err)
	}
	if _, err := b.GetStats(); err != nil {
		t.Errorf("GetStats failed: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestIPTablesBackend_Name(t *testing.T) {
	b := NewIPTablesBackend()
	if b.Name() != "iptables" {
		t.Errorf("expected name=iptables, got %s", b.Name())
	}
}

func TestEBPFBackend_Name(t *testing.T) {
	b := NewEBPFBackend()
	if b.Name() != "ebpf" {
		t.Errorf("expected name=ebpf, got %s", b.Name())
	}
}

func TestSelectBackend(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"noop", "noop"},
		{"iptables", "iptables"},
		{"iptables-legacy", "iptables"},
		{"ebpf", "ebpf"},
		{"bpf", "ebpf"},
		{"unknown", "noop"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			b := selectBackend(c.input)
			if b.Name() != c.want {
				t.Errorf("input=%s: expected %s, got %s", c.input, c.want, b.Name())
			}
		})
	}
}
