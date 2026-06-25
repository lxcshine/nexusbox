package egress

import (
	"context"
	"testing"
)

func TestNewAuditLog(t *testing.T) {
	a := NewAuditLog(100)
	if a == nil {
		t.Fatal("NewAuditLog returned nil")
	}
	if cap(a.entries) != 100 {
		t.Errorf("expected capacity 100, got %d", cap(a.entries))
	}
}

func TestNewAuditLog_DefaultSize(t *testing.T) {
	a := NewAuditLog(0)
	if a.maxSize != 10000 {
		t.Errorf("expected default maxSize 10000, got %d", a.maxSize)
	}
}

func TestAuditLog_AppendAndEntries(t *testing.T) {
	a := NewAuditLog(10)

	for i := 0; i < 5; i++ {
		a.Append(AuditEntry{
			SandboxID: "sb-1",
			StatusCode: 200,
		})
	}

	entries := a.Entries(3)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// Should return the latest 3
	if entries[0].StatusCode != 200 {
		t.Errorf("expected status 200, got %d", entries[0].StatusCode)
	}
}

func TestAuditLog_AllWhenNExceedsSize(t *testing.T) {
	a := NewAuditLog(10)
	a.Append(AuditEntry{SandboxID: "sb-1"})

	entries := a.Entries(100)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestAuditLog_DropOldestWhenFull(t *testing.T) {
	a := NewAuditLog(5)

	for i := 0; i < 10; i++ {
		a.Append(AuditEntry{SandboxID: "sb-" + string(rune('0'+i))})
	}

	entries := a.Entries(0)
	if len(entries) >= 10 {
		t.Errorf("expected entries to be dropped, got %d", len(entries))
	}
}

func TestNewGateway(t *testing.T) {
	g := NewGateway(&GatewayConfig{
		ListenAddr:   ":0",
		AuditLogSize: 100,
	})
	if g == nil {
		t.Fatal("NewGateway returned nil")
	}
	if g.auditLog == nil {
		t.Error("expected auditLog to be initialized")
	}
}

func TestGateway_SetPolicy(t *testing.T) {
	g := NewGateway(&GatewayConfig{ListenAddr: ":0"})

	policy := &Policy{
		SandboxID:       "sb-1",
		AllowedDomains:  []string{"api.openai.com"},
		BlockPrivateIPs: true,
	}
	g.SetPolicy("sb-1", policy)

	got := g.GetPolicy("sb-1")
	if got == nil {
		t.Fatal("expected policy to be set")
	}
	if len(got.AllowedDomains) != 1 {
		t.Errorf("expected 1 allowed domain, got %d", len(got.AllowedDomains))
	}
}

func TestGateway_RemovePolicy(t *testing.T) {
	g := NewGateway(&GatewayConfig{ListenAddr: ":0"})

	g.SetPolicy("sb-1", &Policy{SandboxID: "sb-1"})
	g.RemovePolicy("sb-1")

	if g.GetPolicy("sb-1") != nil {
		t.Error("expected nil after remove")
	}
}

func TestGateway_GetPolicy_Missing(t *testing.T) {
	g := NewGateway(&GatewayConfig{ListenAddr: ":0"})
	if g.GetPolicy("missing") != nil {
		t.Error("expected nil for missing policy")
	}
}

func TestGateway_isDomainAllowed(t *testing.T) {
	g := NewGateway(&GatewayConfig{ListenAddr: ":0"})

	cases := []struct {
		name     string
		policy   *Policy
		host     string
		expected bool
	}{
		{
			name:     "empty allowlist allows all",
			policy:   &Policy{},
			host:     "example.com",
			expected: true,
		},
		{
			name:     "exact match in allowlist",
			policy:   &Policy{AllowedDomains: []string{"api.openai.com"}},
			host:     "api.openai.com",
			expected: true,
		},
		{
			name:     "no match in allowlist",
			policy:   &Policy{AllowedDomains: []string{"api.openai.com"}},
			host:     "evil.com",
			expected: false,
		},
		{
			name:     "denied domain blocked",
			policy:   &Policy{DeniedDomains: []string{"evil.com"}},
			host:     "evil.com",
			expected: false,
		},
		{
			name:     "wildcard subdomain match",
			policy:   &Policy{AllowedDomains: []string{"*.openai.com"}},
			host:     "api.openai.com",
			expected: true,
		},
		{
			name:     "wildcard subdomain no match for parent",
			policy:   &Policy{AllowedDomains: []string{"*.openai.com"}},
			host:     "openai.com",
			expected: false,
		},
		{
			name:     "wildcard star matches all",
			policy:   &Policy{AllowedDomains: []string{"*"}},
			host:     "anything.com",
			expected: true,
		},
		{
			name:     "deny takes precedence over allow",
			policy:   &Policy{AllowedDomains: []string{"*"}, DeniedDomains: []string{"evil.com"}},
			host:     "evil.com",
			expected: false,
		},
		{
			name:     "host with port stripped",
			policy:   &Policy{AllowedDomains: []string{"api.openai.com"}},
			host:     "api.openai.com:443",
			expected: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := g.isDomainAllowed(c.policy, c.host)
			if got != c.expected {
				t.Errorf("expected %v, got %v", c.expected, got)
			}
		})
	}
}

func TestMatchDomain(t *testing.T) {
	cases := []struct {
		pattern string
		host    string
		want    bool
	}{
		{"*", "anything.com", true},
		{"api.openai.com", "api.openai.com", true},
		{"api.openai.com", "other.com", false},
		{"*.openai.com", "api.openai.com", true},
		{"*.openai.com", "sub.api.openai.com", true},
		{"*.openai.com", "openai.com", false},
		{"api.openai.com", "xapi.openai.com", false},
	}
	for _, c := range cases {
		t.Run(c.pattern+"_"+c.host, func(t *testing.T) {
			got := matchDomain(c.pattern, c.host)
			if got != c.want {
				t.Errorf("matchDomain(%q, %q) = %v, want %v", c.pattern, c.host, got, c.want)
			}
		})
	}
}

func TestGateway_isPrivateIP(t *testing.T) {
	g := NewGateway(&GatewayConfig{ListenAddr: ":0"})

	cases := []struct {
		host  string
		want  bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"192.168.1.1", true},
		{"172.16.0.1", true},
		{"169.254.1.1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"example.com", false},
		{"127.0.0.1:8080", true},
	}
	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			got := g.isPrivateIP(c.host)
			if got != c.want {
				t.Errorf("isPrivateIP(%q) = %v, want %v", c.host, got, c.want)
			}
		})
	}
}

func TestGateway_GetStats(t *testing.T) {
	g := NewGateway(&GatewayConfig{ListenAddr: ":0"})

	// Initially all stats should be 0
	stats := g.GetStats()
	if stats.TotalRequests != 0 {
		t.Errorf("expected 0 total, got %d", stats.TotalRequests)
	}
}

func TestGateway_GetAuditEntries(t *testing.T) {
	g := NewGateway(&GatewayConfig{ListenAddr: ":0", AuditLogSize: 100})

	// Add some entries via audit log directly
	g.auditLog.Append(AuditEntry{SandboxID: "sb-1", StatusCode: 200})
	g.auditLog.Append(AuditEntry{SandboxID: "sb-2", StatusCode: 403})

	entries := g.GetAuditEntries(10)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestStaticCredentialProvider(t *testing.T) {
	p := NewStaticCredentialProvider()
	ctx := context.Background()

	p.SetCredentials("sb-1", "api.openai.com", map[string]string{
		"Authorization": "Bearer sk-test",
	})

	creds, err := p.GetCredentials(ctx, "sb-1", "api.openai.com")
	if err != nil {
		t.Fatalf("GetCredentials failed: %v", err)
	}
	if creds["Authorization"] != "Bearer sk-test" {
		t.Errorf("expected Bearer sk-test, got %s", creds["Authorization"])
	}

	// Missing sandbox
	creds, err = p.GetCredentials(ctx, "missing", "api.openai.com")
	if err != nil {
		t.Errorf("expected no error for missing sandbox, got: %v", err)
	}
	if creds != nil {
		t.Errorf("expected nil creds for missing sandbox, got %v", creds)
	}

	// Missing domain
	creds, err = p.GetCredentials(ctx, "sb-1", "missing.com")
	if err != nil {
		t.Errorf("expected no error for missing domain, got: %v", err)
	}
	if creds != nil {
		t.Errorf("expected nil creds for missing domain, got %v", creds)
	}
}
