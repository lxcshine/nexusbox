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
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"

	sandboxv1alpha1 "github.com/nexusbox/nexusbox/pkg/apis/sandbox/v1alpha1"
)

// Agent represents a sandbox agent that runs on each node.
// It manages sandboxes locally and reports status to the control plane.
//
// The agent is responsible for:
// - Managing local sandbox runtimes
// - Reporting node status via heartbeats
// - Executing runtime operations (create/start/stop/delete)
// - Collecting resource usage metrics
// - Handling health checks
type Agent struct {
	mu sync.RWMutex

	// config holds agent configuration.
	config *AgentConfig

	// nodeName is the name of this node.
	nodeName string

	// nodeIP is the IP address of this node.
	nodeIP string

	// sandboxes manages local sandboxes.
	sandboxes map[string]*LocalSandbox

	// client communicates with the control plane.
	client AgentControlPlaneClient

	// httpServer serves HTTP requests.
	httpServer *http.Server

	// stopCh is used to signal shutdown.
	stopCh chan struct{}

	// heartbeatInterval is how often to send heartbeats.
	heartbeatInterval time.Duration

	// lastHeartbeatTime is the time of the last successful heartbeat.
	lastHeartbeatTime time.Time

	// resourceMonitor monitors local resources.
	resourceMonitor *ResourceMonitor
}

// AgentConfig holds configuration for the agent.
type AgentConfig struct {
	// NodeName is the name of this node.
	NodeName string
	// NodeIP is the IP address of this node.
	NodeIP string
	// ControlPlaneURL is the URL of the control plane API.
	ControlPlaneURL string
	// ListenPort is the port to listen on for incoming requests.
	ListenPort int
	// HeartbeatInterval is how often to send heartbeats.
	HeartbeatInterval time.Duration
	// MetricsInterval is how often to collect metrics.
	MetricsInterval time.Duration
	// MaxSandboxes is the maximum number of sandboxes per node.
	MaxSandboxes int32
	// SupportedRuntimes are the runtimes supported by this node.
	SupportedRuntimes []sandboxv1alpha1.SandboxRuntimeType
}

// DefaultAgentConfig returns default agent configuration.
func DefaultAgentConfig() *AgentConfig {
	return &AgentConfig{
		NodeName:          getHostname(),
		NodeIP:            getHostIP(),
		ControlPlaneURL:   "http://localhost:8080",
		ListenPort:        9090,
		HeartbeatInterval: 10 * time.Second,
		MetricsInterval:   15 * time.Second,
		MaxSandboxes:      100,
		SupportedRuntimes: []sandboxv1alpha1.SandboxRuntimeType{
			sandboxv1alpha1.RuntimeKataContainers,
			sandboxv1alpha1.RuntimeGVisor,
			sandboxv1alpha1.RuntimeRunc,
		},
	}
}

// LocalSandbox represents a sandbox managed by the agent.
type LocalSandbox struct {
	mu sync.RWMutex

	// Key is the namespace/name identifier.
	Key string
	// Sandbox is the sandbox CRD object.
	Sandbox *sandboxv1alpha1.Sandbox
	// RuntimeHandle is the handle to the runtime.
	RuntimeHandle interface{}
	// State is the current state of the sandbox.
	State LocalSandboxState
	// CreatedAt is when the sandbox was created.
	CreatedAt time.Time
	// StartedAt is when the sandbox was started.
	StartedAt time.Time
	// ResourceUsage tracks current resource usage.
	ResourceUsage *LocalResourceUsage
}

// LocalSandboxState represents the state of a local sandbox.
type LocalSandboxState string

const (
	LocalStatePending  LocalSandboxState = "pending"
	LocalStateCreating LocalSandboxState = "creating"
	LocalStateRunning  LocalSandboxState = "running"
	LocalStatePaused   LocalSandboxState = "paused"
	LocalStateStopping LocalSandboxState = "stopping"
	LocalStateStopped  LocalSandboxState = "stopped"
	LocalStateDeleting LocalSandboxState = "deleting"
	LocalStateDeleted  LocalSandboxState = "deleted"
	LocalStateError    LocalSandboxState = "error"
)

// LocalResourceUsage tracks resource usage for a local sandbox.
type LocalResourceUsage struct {
	CPUUsage    float64 // CPU usage in cores
	MemoryUsage uint64  // Memory usage in bytes
	DiskUsage   uint64  // Disk usage in bytes
	NetworkRx   uint64  // Network received bytes
	NetworkTx   uint64  // Network transmitted bytes
	Timestamp   time.Time
}

// AgentControlPlaneClient communicates with the control plane.
type AgentControlPlaneClient interface {
	SendHeartbeat(ctx context.Context, heartbeat *NodeHeartbeat) error
	GetSandbox(ctx context.Context, key string) (*sandboxv1alpha1.Sandbox, error)
	UpdateSandboxStatus(ctx context.Context, key string, status *sandboxv1alpha1.SandboxStatus) error
	GetNodeStatus(ctx context.Context, nodeName string) (*NodeStatus, error)
}

// NodeHeartbeat contains information sent in each heartbeat.
type NodeHeartbeat struct {
	// Timestamp is when the heartbeat was sent.
	Timestamp metav1.Time
	// NodeName is the name of the node.
	NodeName string
	// NodeIP is the IP address of the node.
	NodeIP string
	// Status is the current status of the node.
	Status *NodeStatus
	// Sandboxes is the list of sandboxes running on this node.
	Sandboxes []LocalSandboxInfo
}

// NodeStatus contains detailed node status information.
type NodeStatus struct {
	// Capacity is the total capacity of the node.
	Capacity *NodeResources
	// Allocatable is the allocatable resources.
	Allocatable *NodeResources
	// Allocated is the currently allocated resources.
	Allocated *NodeResources
	// Available is the available resources.
	Available *NodeResources
	// Conditions are the current conditions of the node.
	Conditions []NodeCondition
	// Version is the agent version.
	Version string
	// Uptime is how long the node has been up.
	Uptime time.Duration
}

// NodeResources represents node resources.
type NodeResources struct {
	CPU               int64 // CPU in millicores
	Memory            int64 // Memory in bytes
	GPU               int64 // GPU count
	EphemeralStorage  int64 // Ephemeral storage in bytes
	PersistentStorage int64 // Persistent storage in bytes
	Pods              int32 // Number of pods/sandboxes
}

// NodeCondition represents a condition of the node.
type NodeCondition struct {
	Type    NodeConditionType
	Status  ConditionStatus
	Reason  string
	Message string
}

// NodeConditionType is the type of a node condition.
type NodeConditionType string

const (
	NodeReady              NodeConditionType = "Ready"
	NodeMemoryPressure     NodeConditionType = "MemoryPressure"
	NodeDiskPressure       NodeConditionType = "DiskPressure"
	NodePIDPressure        NodeConditionType = "PIDPressure"
	NodeNetworkUnavailable NodeConditionType = "NetworkUnavailable"
)

// ConditionStatus is the status of a condition.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// LocalSandboxInfo contains summary info about a local sandbox.
type LocalSandboxInfo struct {
	Key           string
	State         LocalSandboxState
	ResourceUsage *LocalResourceUsage
}

// NewAgent creates a new Agent.
func NewAgent(config *AgentConfig) (*Agent, error) {
	if config == nil {
		config = DefaultAgentConfig()
	}

	a := &Agent{
		config:            config,
		nodeName:          config.NodeName,
		nodeIP:            config.NodeIP,
		sandboxes:         make(map[string]*LocalSandbox),
		stopCh:            make(chan struct{}),
		heartbeatInterval: config.HeartbeatInterval,
	}

	// Initialize resource monitor
	a.resourceMonitor = NewResourceMonitor(config.MetricsInterval)

	// Initialize HTTP server
	a.initHTTPServer()

	klog.Infof("Initialized agent for node %s (%s)", a.nodeName, a.nodeIP)
	return a, nil
}

// Start starts the agent.
func (a *Agent) Start(ctx context.Context) error {
	klog.Info("Starting sandbox agent")

	agentStartTime = time.Now()

	// Start HTTP server
	go func() {
		if err := a.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// Start heartbeat loop
	go wait.Until(a.sendHeartbeat, a.heartbeatInterval, a.stopCh)

	// Start resource monitor
	if a.resourceMonitor != nil {
		a.resourceMonitor.Start(ctx)
	}

	// Start sandbox cleanup loop
	go wait.Until(a.cleanupStaleSandboxes, 30*time.Second, a.stopCh)

	klog.Infof("Sandbox agent started on node %s", a.nodeName)
	return nil
}

// Stop stops the agent.
func (a *Agent) Stop() error {
	klog.Info("Stopping sandbox agent")
	close(a.stopCh)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.httpServer.Shutdown(ctx)
}

// sendHeartbeat sends a heartbeat to the control plane.
func (a *Agent) sendHeartbeat() {
	ctx := context.Background()
	startTime := time.Now()

	heartbeat := a.buildHeartbeat()

	if err := a.client.SendHeartbeat(ctx, heartbeat); err != nil {
		klog.Warningf("Failed to send heartbeat: %v", err)
		return
	}

	a.mu.Lock()
	a.lastHeartbeatTime = startTime
	a.mu.Unlock()

	klog.V(4).Infof("Sent heartbeat for node %s (sandboxes: %d)", a.nodeName, len(heartbeat.Sandboxes))
}

// buildHeartbeat builds the heartbeat message.
func (a *Agent) buildHeartbeat() *NodeHeartbeat {
	a.mu.RLock()
	defer a.mu.RUnlock()

	heartbeat := &NodeHeartbeat{
		Timestamp: metav1.Now(),
		NodeName:  a.nodeName,
		NodeIP:    a.nodeIP,
		Status:    a.buildNodeStatus(),
		Sandboxes: a.getLocalSandboxInfos(),
	}

	return heartbeat
}

// buildNodeStatus builds the node status for the heartbeat.
func (a *Agent) buildNodeStatus() *NodeStatus {
	status := &NodeStatus{
		Version: "1.0.0",
		Uptime:  time.Since(agentStartTime),
	}

	// Get resource information from monitor
	if a.resourceMonitor != nil {
		resources := a.resourceMonitor.GetNodeResources()
		status.Capacity = resources.Capacity
		status.Allocatable = resources.Allocatable
		status.Allocated = resources.Allocated
		status.Available = resources.Available
	} else {
		// Default values
		status.Capacity = &NodeResources{
			CPU:               4000,
			Memory:            16 * 1024 * 1024 * 1024,
			GPU:               0,
			EphemeralStorage:  100 * 1024 * 1024 * 1024,
			PersistentStorage: 500 * 1024 * 1024 * 1024,
			Pods:              a.config.MaxSandboxes,
		}
		status.Allocatable = status.Capacity
		status.Allocated = &NodeResources{Pods: int32(len(a.sandboxes))}
		status.Available = &NodeResources{Pods: a.config.MaxSandboxes - int32(len(a.sandboxes))}
	}

	// Build conditions
	status.Conditions = a.getNodeConditions()

	return status
}

// getNodeConditions returns the current node conditions.
func (a *Agent) getNodeConditions() []NodeCondition {
	conditions := []NodeCondition{
		{
			Type:    NodeReady,
			Status:  ConditionTrue,
			Reason:  "AgentRunning",
			Message: fmt.Sprintf("Agent is running on node %s", a.nodeName),
		},
	}

	// Check memory pressure
	if a.resourceMonitor != nil {
		memStats := a.resourceMonitor.GetMemoryStats()
		if memStats.UsagePercent > 85 {
			conditions = append(conditions, NodeCondition{
				Type:    NodeMemoryPressure,
				Status:  ConditionTrue,
				Reason:  "HighMemoryUsage",
				Message: fmt.Sprintf("Memory usage is %.2f%%", memStats.UsagePercent),
			})
		}
	}

	// Check disk pressure
	if a.resourceMonitor != nil {
		diskStats := a.resourceMonitor.GetDiskStats()
		if diskStats.UsagePercent > 80 {
			conditions = append(conditions, NodeCondition{
				Type:    NodeDiskPressure,
				Status:  ConditionTrue,
				Reason:  "HighDiskUsage",
				Message: fmt.Sprintf("Disk usage is %.2f%%", diskStats.UsagePercent),
			})
		}
	}

	return conditions
}

// getLocalSandboxInfos returns info about all local sandboxes.
func (a *Agent) getLocalSandboxInfos() []LocalSandboxInfo {
	infos := make([]LocalSandboxInfo, 0, len(a.sandboxes))

	for _, sb := range a.sandboxes {
		sb.mu.RLock()
		info := LocalSandboxInfo{
			Key:           sb.Key,
			State:         sb.State,
			ResourceUsage: sb.ResourceUsage,
		}
		sb.mu.RUnlock()
		infos = append(infos, info)
	}

	return infos
}

// CreateSandbox creates a new local sandbox.
func (a *Agent) CreateSandbox(ctx context.Context, sb *sandboxv1alpha1.Sandbox) error {
	key := cacheMetaObjectToName(sb)

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.sandboxes[key]; exists {
		return fmt.Errorf("sandbox %s already exists on this node", key)
	}

	localSB := &LocalSandbox{
		Key:       key,
		Sandbox:   sb,
		State:     LocalStateCreating,
		CreatedAt: time.Now(),
	}

	a.sandboxes[key] = localSB

	klog.Infof("Creating sandbox %s on node %s", key, a.nodeName)

	// In production, create the actual container/runtime here
	// For now, transition to Running after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		localSB.mu.Lock()
		localSB.State = LocalStateRunning
		localSB.StartedAt = time.Now()
		localSB.mu.Unlock()
	}()

	return nil
}

// StopSandbox stops a local sandbox.
func (a *Agent) StopSandbox(ctx context.Context, key string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	sb, exists := a.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found on this node", key)
	}

	sb.mu.Lock()
	sb.State = LocalStateStopping
	sb.mu.Unlock()

	// In production, stop the actual container/runtime here
	go func() {
		time.Sleep(50 * time.Millisecond)
		sb.mu.Lock()
		sb.State = LocalStateStopped
		sb.mu.Unlock()
	}()

	return nil
}

// DeleteSandbox deletes a local sandbox.
func (a *Agent) DeleteSandbox(ctx context.Context, key string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	sb, exists := a.sandboxes[key]
	if !exists {
		return fmt.Errorf("sandbox %s not found on this node", key)
	}

	sb.mu.Lock()
	sb.State = LocalStateDeleting
	sb.mu.Unlock()

	// In production, delete the actual container/runtime here
	go func() {
		time.Sleep(50 * time.Millisecond)
		a.mu.Lock()
		delete(a.sandboxes, key)
		a.mu.Unlock()
		klog.Infof("Deleted sandbox %s from node %s", key, a.nodeName)
	}()

	return nil
}

// GetSandbox returns a local sandbox.
func (a *Agent) GetSandbox(key string) (*LocalSandbox, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	sb, exists := a.sandboxes[key]
	if !exists {
		return nil, false
	}

	copy := *sb
	return &copy, true
}

// ListSandboxes lists all local sandboxes.
func (a *Agent) ListSandboxes() []*LocalSandbox {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make([]*LocalSandbox, 0, len(a.sandboxes))
	for _, sb := range a.sandboxes {
		copy := *sb
		result = append(result, &copy)
	}
	return result
}

// cleanupStaleSandboxes cleans up stale sandboxes.
func (a *Agent) cleanupStaleSandboxes() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()

	for key, sb := range a.sandboxes {
		sb.mu.RLock()
		state := sb.State
		createdAt := sb.CreatedAt
		sb.mu.RUnlock()

		// Clean up sandboxes that have been in creating/stopping/deleting too long
		switch state {
		case LocalStateCreating:
			if now.Sub(createdAt) > 5*time.Minute {
				klog.Warningf("Cleaning up stuck sandbox %s (state: %s)", key, state)
				delete(a.sandboxes, key)
			}
		case LocalStateStopping:
			if now.Sub(createdAt) > 2*time.Minute {
				klog.Warningf("Cleaning up stuck sandbox %s (state: %s)", key, state)
				delete(a.sandboxes, key)
			}
		case LocalStateDeleting:
			if now.Sub(createdAt) > 1*time.Minute {
				klog.Warningf("Cleaning up stuck sandbox %s (state: %s)", key, state)
				delete(a.sandboxes, key)
			}
		}
	}
}

// initHTTPServer initializes the HTTP server.
func (a *Agent) initHTTPServer() {
	mux := http.NewServeMux()

	// Health check endpoint
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// Sandbox management endpoints
	mux.HandleFunc("/api/v1/sandboxes", a.handleSandboxRequest)
	mux.HandleFunc("/api/v1/sandboxes/", a.handleSandboxDetailRequest)

	// Status endpoint
	mux.HandleFunc("/api/v1/status", a.handleStatusRequest)

	// Metrics endpoint
	mux.HandleFunc("/metrics", a.handleMetricsRequest)

	addr := fmt.Sprintf("%s:%d", a.nodeIP, a.config.ListenPort)
	a.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
}

// handleSandboxRequest handles sandbox CRUD requests.
func (a *Agent) handleSandboxRequest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listSandboxesHandler(w, r)
	case http.MethodPost:
		a.createSandboxHandler(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleSandboxDetailRequest handles individual sandbox requests.
func (a *Agent) handleSandboxDetailRequest(w http.ResponseWriter, r *http.Request) {
	// Extract key from path
	key := r.URL.Path[len("/api/v1/sandboxes/"):]

	switch r.Method {
	case http.MethodGet:
		a.getSandboxHandler(w, r, key)
	case http.MethodDelete:
		a.deleteSandboxHandler(w, r, key)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// listSandboxesHandler handles listing sandboxes.
func (a *Agent) listSandboxesHandler(w http.ResponseWriter, r *http.Request) {
	sandboxes := a.ListSandboxes()

	resp := ListSandboxesResponse{
		Sandboxes: sandboxes,
		Total:     len(sandboxes),
	}

	writeJSON(w, http.StatusOK, resp)
}

// createSandboxHandler handles creating a sandbox.
func (a *Agent) createSandboxHandler(w http.ResponseWriter, r *http.Request) {
	var req CreateSandboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := a.CreateSandbox(context.Background(), req.Sandbox); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, CreateSandboxResponse{Key: req.Key})
}

// getSandboxHandler handles getting a sandbox.
func (a *Agent) getSandboxHandler(w http.ResponseWriter, r *http.Request, key string) {
	sb, exists := a.GetSandbox(key)
	if !exists {
		writeJSONError(w, http.StatusNotFound, "sandbox not found")
		return
	}

	writeJSON(w, http.StatusOK, sb)
}

// deleteSandboxHandler handles deleting a sandbox.
func (a *Agent) deleteSandboxHandler(w http.ResponseWriter, r *http.Request, key string) {
	if err := a.DeleteSandbox(context.Background(), key); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleStatusRequest handles status requests.
func (a *Agent) handleStatusRequest(w http.ResponseWriter, r *http.Request) {
	status := a.buildNodeStatus()
	writeJSON(w, http.StatusOK, status)
}

// handleMetricsRequest handles metrics requests.
func (a *Agent) handleMetricsRequest(w http.ResponseWriter, r *http.Request) {
	if a.resourceMonitor == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{})
		return
	}

	metrics := a.resourceMonitor.GetMetrics()
	writeJSON(w, http.StatusOK, metrics)
}

// Response types for HTTP handlers.

type ListSandboxesResponse struct {
	Sandboxes []*LocalSandbox
	Total     int
}

type CreateSandboxRequest struct {
	Key     string
	Sandbox *sandboxv1alpha1.Sandbox
}

type CreateSandboxResponse struct {
	Key string
}

// writeJSON writes JSON response.
func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

// writeJSONError writes JSON error response.
func writeJSONError(w http.ResponseWriter, code int, message string) {
	type ErrorResponse struct {
		Error string `json:"error"`
	}
	writeJSON(w, code, ErrorResponse{Error: message})
}

var agentStartTime time.Time

func getHostname() string {
	name, _ := os.Hostname()
	return name
}

func getHostIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return "127.0.0.1"
}

func cacheMetaObjectToName(obj metav1.Object) string {
	return obj.GetNamespace() + "/" + obj.GetName()
}
