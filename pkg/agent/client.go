package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"k8s.io/klog/v2"
)

// ControlPlaneClient communicates with the NexusBox control plane.
// It handles heartbeat reporting, sandbox status updates, and
// resource reporting.
type ControlPlaneClient interface {
	// SendHeartbeat sends a heartbeat to the control plane.
	SendHeartbeat(ctx context.Context, heartbeat *Heartbeat) error

	// ReportSandboxStatus reports sandbox status to the control plane.
	ReportSandboxStatus(ctx context.Context, status *SandboxStatusReport) error

	// ReportNodeResources reports node resource usage to the control plane.
	ReportNodeResources(ctx context.Context, resources *NodeResourceReport) error

	// GetSandboxSpec retrieves the sandbox spec from the control plane.
	GetSandboxSpec(ctx context.Context, namespace, name string) (*SandboxSpecResponse, error)

	// ReportEvent reports an event to the control plane.
	ReportEvent(ctx context.Context, event *AgentEvent) error
}

// HTTPControlPlaneClient implements ControlPlaneClient using HTTP.
type HTTPControlPlaneClient struct {
	// baseURL is the base URL of the control plane API.
	baseURL string

	// httpClient is the HTTP client.
	httpClient *http.Client

	// nodeName is the name of this node.
	nodeName string

	// retryCount is the number of retries for failed requests.
	retryCount int

	// retryInterval is the interval between retries.
	retryInterval time.Duration
}

// NewHTTPControlPlaneClient creates a new HTTPControlPlaneClient.
func NewHTTPControlPlaneClient(baseURL, nodeName string) *HTTPControlPlaneClient {
	return &HTTPControlPlaneClient{
		baseURL:   baseURL,
		nodeName:  nodeName,
		retryCount: 3,
		retryInterval: 5 * time.Second,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SendHeartbeat sends a heartbeat to the control plane.
func (c *HTTPControlPlaneClient) SendHeartbeat(ctx context.Context, heartbeat *Heartbeat) error {
	return c.doRequestWithRetry(ctx, "POST", "/api/v1/agents/heartbeat", heartbeat, nil)
}

// ReportSandboxStatus reports sandbox status to the control plane.
func (c *HTTPControlPlaneClient) ReportSandboxStatus(ctx context.Context, status *SandboxStatusReport) error {
	return c.doRequestWithRetry(ctx, "POST", "/api/v1/agents/sandbox-status", status, nil)
}

// ReportNodeResources reports node resource usage to the control plane.
func (c *HTTPControlPlaneClient) ReportNodeResources(ctx context.Context, resources *NodeResourceReport) error {
	return c.doRequestWithRetry(ctx, "POST", "/api/v1/agents/node-resources", resources, nil)
}

// GetSandboxSpec retrieves the sandbox spec from the control plane.
func (c *HTTPControlPlaneClient) GetSandboxSpec(ctx context.Context, namespace, name string) (*SandboxSpecResponse, error) {
	path := fmt.Sprintf("/api/v1/sandboxes/%s/%s", namespace, name)
	var resp SandboxSpecResponse
	if err := c.doRequestWithRetry(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ReportEvent reports an event to the control plane.
func (c *HTTPControlPlaneClient) ReportEvent(ctx context.Context, event *AgentEvent) error {
	return c.doRequestWithRetry(ctx, "POST", "/api/v1/agents/events", event, nil)
}

// doRequestWithRetry performs an HTTP request with retries.
func (c *HTTPControlPlaneClient) doRequestWithRetry(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	var lastErr error

	for i := 0; i < c.retryCount; i++ {
		if i > 0 {
			select {
			case <-time.After(c.retryInterval):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		lastErr = c.doRequest(ctx, method, path, body, result)
		if lastErr == nil {
			return nil
		}

		klog.V(4).Infof("Request to %s %s failed (attempt %d/%d): %v",
			method, path, i+1, c.retryCount, lastErr)
	}

	return fmt.Errorf("request to %s %s failed after %d attempts: %w",
		method, path, c.retryCount, lastErr)
}

// doRequest performs a single HTTP request.
func (c *HTTPControlPlaneClient) doRequest(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	url := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-NexusBox-Node", c.nodeName)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// Heartbeat represents a heartbeat message from an agent.
type Heartbeat struct {
	// NodeName is the name of the node.
	NodeName string `json:"nodeName"`

	// NodeIP is the IP address of the node.
	NodeIP string `json:"nodeIP"`

	// Timestamp is the heartbeat timestamp.
	Timestamp time.Time `json:"timestamp"`

	// Status is the node status.
	Status string `json:"status"`

	// Sandboxes are the sandboxes on this node.
	Sandboxes []LocalSandboxStatus `json:"sandboxes"`

	// Resources is the node resource information.
	Resources *NodeResourceReport `json:"resources"`

	// SupportedRuntimes are the runtimes supported by this node.
	SupportedRuntimes []string `json:"supportedRuntimes"`

	// Version is the agent version.
	Version string `json:"version"`
}

// LocalSandboxStatus represents the status of a sandbox on the local node.
type LocalSandboxStatus struct {
	// Name is the sandbox name.
	Name string `json:"name"`

	// Namespace is the sandbox namespace.
	Namespace string `json:"namespace"`

	// Phase is the sandbox phase.
	Phase string `json:"phase"`

	// RuntimeType is the runtime type.
	RuntimeType string `json:"runtimeType"`

	// PID is the process ID.
	PID int `json:"pid"`

	// StartedAt is when the sandbox started.
	StartedAt *time.Time `json:"startedAt"`

	// CPUUsage is the CPU usage in millicores.
	CPUUsage int64 `json:"cpuUsage"`

	// MemoryUsage is the memory usage in bytes.
	MemoryUsage int64 `json:"memoryUsage"`
}

// SandboxStatusReport represents a sandbox status report.
type SandboxStatusReport struct {
	// NodeName is the name of the reporting node.
	NodeName string `json:"nodeName"`

	// SandboxName is the sandbox name.
	SandboxName string `json:"sandboxName"`

	// Namespace is the sandbox namespace.
	Namespace string `json:"namespace"`

	// Phase is the current phase.
	Phase string `json:"phase"`

	// Message is a status message.
	Message string `json:"message"`

	// Timestamp is the report timestamp.
	Timestamp time.Time `json:"timestamp"`
}

// NodeResourceReport represents a node resource report.
type NodeResourceReport struct {
	// NodeName is the name of the node.
	NodeName string `json:"nodeName"`

	// Timestamp is the report timestamp.
	Timestamp time.Time `json:"timestamp"`

	// CPUCapacity is the total CPU capacity in millicores.
	CPUCapacity int64 `json:"cpuCapacity"`

	// CPUUsed is the used CPU in millicores.
	CPUUsed int64 `json:"cpuUsed"`

	// MemoryCapacity is the total memory capacity in bytes.
	MemoryCapacity int64 `json:"memoryCapacity"`

	// MemoryUsed is the used memory in bytes.
	MemoryUsed int64 `json:"memoryUsed"`

	// GPUCapacity is the total GPU count.
	GPUCapacity int64 `json:"gpuCapacity"`

	// GPUUsed is the used GPU count.
	GPUUsed int64 `json:"gpuUsed"`

	// DiskCapacity is the total disk capacity in bytes.
	DiskCapacity int64 `json:"diskCapacity"`

	// DiskUsed is the used disk in bytes.
	DiskUsed int64 `json:"diskUsed"`

	// SandboxCount is the number of sandboxes.
	SandboxCount int `json:"sandboxCount"`

	// MaxSandboxes is the maximum number of sandboxes.
	MaxSandboxes int `json:"maxSandboxes"`
}

// SandboxSpecResponse is the response for a sandbox spec request.
type SandboxSpecResponse struct {
	// Name is the sandbox name.
	Name string `json:"name"`

	// Namespace is the sandbox namespace.
	Namespace string `json:"namespace"`

	// Spec is the sandbox spec.
	Spec json.RawMessage `json:"spec"`
}

// AgentEvent represents an event from an agent.
type AgentEvent struct {
	// NodeName is the name of the node.
	NodeName string `json:"nodeName"`

	// Type is the event type.
	Type string `json:"type"`

	// Reason is the event reason.
	Reason string `json:"reason"`

	// Message is the event message.
	Message string `json:"message"`

	// SandboxName is the sandbox name (if applicable).
	SandboxName string `json:"sandboxName,omitempty"`

	// Namespace is the sandbox namespace (if applicable).
	Namespace string `json:"namespace,omitempty"`

	// Timestamp is the event timestamp.
	Timestamp time.Time `json:"timestamp"`
}

// MockControlPlaneClient is a mock implementation for testing.
type MockControlPlaneClient struct {
	Heartbeats        []*Heartbeat
	StatusReports     []*SandboxStatusReport
	ResourceReports   []*NodeResourceReport
	Events            []*AgentEvent
	FailHeartbeat     bool
	FailStatusReport  bool
}

// NewMockControlPlaneClient creates a new MockControlPlaneClient.
func NewMockControlPlaneClient() *MockControlPlaneClient {
	return &MockControlPlaneClient{
		Heartbeats:      make([]*Heartbeat, 0),
		StatusReports:   make([]*SandboxStatusReport, 0),
		ResourceReports: make([]*NodeResourceReport, 0),
		Events:          make([]*AgentEvent, 0),
	}
}

// SendHeartbeat sends a heartbeat (mock).
func (m *MockControlPlaneClient) SendHeartbeat(ctx context.Context, heartbeat *Heartbeat) error {
	if m.FailHeartbeat {
		return fmt.Errorf("mock heartbeat failure")
	}
	m.Heartbeats = append(m.Heartbeats, heartbeat)
	return nil
}

// ReportSandboxStatus reports sandbox status (mock).
func (m *MockControlPlaneClient) ReportSandboxStatus(ctx context.Context, status *SandboxStatusReport) error {
	if m.FailStatusReport {
		return fmt.Errorf("mock status report failure")
	}
	m.StatusReports = append(m.StatusReports, status)
	return nil
}

// ReportNodeResources reports node resources (mock).
func (m *MockControlPlaneClient) ReportNodeResources(ctx context.Context, resources *NodeResourceReport) error {
	m.ResourceReports = append(m.ResourceReports, resources)
	return nil
}

// GetSandboxSpec retrieves a sandbox spec (mock).
func (m *MockControlPlaneClient) GetSandboxSpec(ctx context.Context, namespace, name string) (*SandboxSpecResponse, error) {
	return &SandboxSpecResponse{
		Name:      name,
		Namespace: namespace,
		Spec:      json.RawMessage(`{"runtime": "runc"}`),
	}, nil
}

// ReportEvent reports an event (mock).
func (m *MockControlPlaneClient) ReportEvent(ctx context.Context, event *AgentEvent) error {
	m.Events = append(m.Events, event)
	return nil
}
