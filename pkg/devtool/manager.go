
package devtool

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// portRange defines the dynamic port allocation range for dev tools.
var portRange = struct {
	min, max int
}{49152, 65535}

// DevToolManager manages per-sandbox dev tool instances.
// It handles:
//   - Port allocation (dynamic, from a pool)
//   - Process lifecycle (start/stop/restart)
//   - Health checking
//   - Auto-cleanup on sandbox destruction
//   - Reverse proxy routing
type DevToolManager struct {
	mu         sync.RWMutex
	instances  map[string]*DevToolInstance  // instanceID -> instance
	sandboxMap map[string]map[string]bool   // sandboxID -> set of instanceIDs
	usedPorts  map[int]bool                 // track allocated ports
	nextPort   int
	launcher   *Launcher
	proxy      *DevToolProxy
	stopCh     chan struct{}
}

// NewDevToolManager creates a new manager with a launcher that auto-detects
// installed dev tool binaries.
func NewDevToolManager() *DevToolManager {
	m := &DevToolManager{
		instances:  make(map[string]*DevToolInstance),
		sandboxMap: make(map[string]map[string]bool),
		usedPorts:  make(map[int]bool),
		nextPort:   portRange.min,
		launcher:   NewLauncher(),
		stopCh:     make(chan struct{}),
	}
	m.proxy = NewDevToolProxy(m)
	return m
}

// Launcher returns the underlying launcher (for binary path inspection).
func (m *DevToolManager) Launcher() *Launcher { return m.launcher }

// Proxy returns the HTTP reverse proxy handler.
func (m *DevToolManager) Proxy() *DevToolProxy { return m.proxy }

// Start launches a dev tool for the given sandbox.
// The tool runs as a child process and should be placed under the sandbox's
// Job Object / container for security restriction inheritance.
func (m *DevToolManager) Start(ctx context.Context, sandboxID string, config DevToolConfig, workingDir string) (*DevToolInstance, error) {
	if !config.Enabled {
		return nil, fmt.Errorf("dev tool %s is not enabled", config.Type)
	}

	m.mu.Lock()
	port := config.Port
	if port == 0 {
		var err error
		port, err = m.allocatePort()
		if err != nil {
			m.mu.Unlock()
			return nil, fmt.Errorf("failed to allocate port: %w", err)
		}
	} else {
		if m.usedPorts[port] {
			m.mu.Unlock()
			return nil, fmt.Errorf("port %d already in use", port)
		}
		m.usedPorts[port] = true
	}
	m.mu.Unlock()

	var inst *DevToolInstance
	var err error
	switch config.Type {
	case DevToolJupyterLab:
		inst, err = m.launcher.LaunchJupyter(config, workingDir, port)
	case DevToolCodeServer:
		inst, err = m.launcher.LaunchCodeServer(config, workingDir, port)
	default:
		m.releasePort(port)
		return nil, fmt.Errorf("unknown dev tool type: %s", config.Type)
	}

	if err != nil {
		m.releasePort(port)
		return nil, err
	}

	inst.SandboxID = sandboxID

	m.mu.Lock()
	m.instances[inst.ID] = inst
	if m.sandboxMap[sandboxID] == nil {
		m.sandboxMap[sandboxID] = make(map[string]bool)
	}
	m.sandboxMap[sandboxID][inst.ID] = true
	m.mu.Unlock()

	// Start background health checker and wait goroutine
	go m.monitorInstance(inst)

	klog.Infof("DevTool %s started for sandbox %s on port %d", inst.Type, sandboxID, port)
	return inst, nil
}

// Stop stops a specific dev tool instance by ID.
func (m *DevToolManager) Stop(ctx context.Context, instanceID string) error {
	m.mu.Lock()
	inst, ok := m.instances[instanceID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("dev tool instance %s not found", instanceID)
	}
	sandboxID := inst.SandboxID
	port := inst.Port
	m.mu.Unlock()

	if err := m.launcher.Stop(inst); err != nil {
		klog.Warningf("Error stopping dev tool %s: %v", instanceID, err)
	}

	m.mu.Lock()
	inst.Status = DevToolStatusStopped
	m.releasePortLocked(port)
	delete(m.instances, instanceID)
	if m.sandboxMap[sandboxID] != nil {
		delete(m.sandboxMap[sandboxID], instanceID)
		if len(m.sandboxMap[sandboxID]) == 0 {
			delete(m.sandboxMap, sandboxID)
		}
	}
	m.mu.Unlock()

	klog.Infof("DevTool %s stopped", instanceID)
	return nil
}

// StopAll stops all dev tools for a sandbox (called on sandbox destruction).
func (m *DevToolManager) StopAll(ctx context.Context, sandboxID string) error {
	m.mu.RLock()
	instanceIDs := make([]string, 0, len(m.sandboxMap[sandboxID]))
	for id := range m.sandboxMap[sandboxID] {
		instanceIDs = append(instanceIDs, id)
	}
	m.mu.RUnlock()

	if len(instanceIDs) == 0 {
		return nil
	}

	klog.Infof("Stopping %d dev tools for sandbox %s", len(instanceIDs), sandboxID)
	var lastErr error
	for _, id := range instanceIDs {
		if err := m.Stop(ctx, id); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Get returns a dev tool instance by ID.
func (m *DevToolManager) Get(instanceID string) (*DevToolInstance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inst, ok := m.instances[instanceID]
	return inst, ok
}

// GetBySandboxAndType returns the dev tool instance for a sandbox by type.
func (m *DevToolManager) GetBySandboxAndType(sandboxID string, toolType DevToolType) (*DevToolInstance, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id := range m.sandboxMap[sandboxID] {
		inst := m.instances[id]
		if inst != nil && inst.Type == toolType {
			return inst, true
		}
	}
	return nil, false
}

// ListBySandbox returns all dev tool instances for a sandbox.
func (m *DevToolManager) ListBySandbox(sandboxID string) []*DevToolInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*DevToolInstance, 0, len(m.sandboxMap[sandboxID]))
	for id := range m.sandboxMap[sandboxID] {
		if inst := m.instances[id]; inst != nil {
			result = append(result, inst)
		}
	}
	return result
}

// List returns all dev tool instances.
func (m *DevToolManager) List() []*DevToolInstance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*DevToolInstance, 0, len(m.instances))
	for _, inst := range m.instances {
		result = append(result, inst)
	}
	return result
}

// HealthCheck checks if a dev tool is healthy by attempting a TCP connection.
func (m *DevToolManager) HealthCheck(ctx context.Context, instanceID string) (bool, error) {
	m.mu.RLock()
	inst, ok := m.instances[instanceID]
	m.mu.RUnlock()
	if !ok {
		return false, fmt.Errorf("instance %s not found", instanceID)
	}

	if inst.Status == DevToolStatusStopped || inst.Status == DevToolStatusFailed {
		return false, nil
	}

	// TCP probe to 127.0.0.1:port
	dialer := net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", inst.Port))
	if err != nil {
		return false, nil
	}
	conn.Close()
	return true, nil
}

// monitorInstance watches the process and updates status when it becomes reachable.
func (m *DevToolManager) monitorInstance(inst *DevToolInstance) {
	// Health probe loop: wait for the tool to be ready, then mark as running
	deadline := time.Now().Add(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			if time.Now().After(deadline) {
				m.mu.Lock()
				if inst.Status == DevToolStatusPending {
					inst.Status = DevToolStatusFailed
					klog.Warningf("DevTool %s failed to become ready within 30s", inst.ID)
				}
				m.mu.Unlock()
				return
			}

			// Check if process is still alive
			if inst.cmd == nil || inst.cmd.Process == nil {
				m.mu.Lock()
				inst.Status = DevToolStatusFailed
				m.mu.Unlock()
				return
			}

			// TCP probe
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", inst.Port), 1*time.Second)
			if err == nil {
				conn.Close()
				m.mu.Lock()
				if inst.Status == DevToolStatusPending {
					inst.Status = DevToolStatusRunning
					klog.Infof("DevTool %s is now ready on port %d", inst.ID, inst.Port)
				}
				m.mu.Unlock()
				// Wait for process exit in background
				go func() {
					_ = m.launcher.Wait(inst)
					m.mu.Lock()
					m.releasePortLocked(inst.Port)
					m.mu.Unlock()
					klog.Infof("DevTool %s process exited", inst.ID)
				}()
				return
			}
		}
	}
}

// allocatePort finds a free port in the dynamic range.
// Caller must hold m.mu.
func (m *DevToolManager) allocatePort() (int, error) {
	for port := m.nextPort; port < portRange.max; port++ {
		if !m.usedPorts[port] {
			// Verify the port is actually free
			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err == nil {
				ln.Close()
				m.usedPorts[port] = true
				m.nextPort = port + 1
				return port, nil
			}
		}
	}
	// Wrap around
	for port := portRange.min; port < m.nextPort; port++ {
		if !m.usedPorts[port] {
			ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
			if err == nil {
				ln.Close()
				m.usedPorts[port] = true
				return port, nil
			}
		}
	}
	return 0, fmt.Errorf("no free ports available in range %d-%d", portRange.min, portRange.max)
}

func (m *DevToolManager) releasePort(port int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releasePortLocked(port)
}

func (m *DevToolManager) releasePortLocked(port int) {
	delete(m.usedPorts, port)
}
