package devtool

import (
	"context"

	"github.com/lxcshine/nexusbox/pkg/sandbox/runtime"
)

// RuntimeAdapter adapts DevToolManager to the runtime.DevToolManager interface.
// This bridges the type difference between pkg/devtool.DevToolConfig and
// runtime.DevToolConfig without creating a circular import.
type RuntimeAdapter struct {
	manager *DevToolManager
}

// NewRuntimeAdapter creates an adapter that satisfies runtime.DevToolManager.
func NewRuntimeAdapter(m *DevToolManager) runtime.DevToolManager {
	return &RuntimeAdapter{manager: m}
}

// Start launches a dev tool for the given sandbox, returning instance ID and port.
func (a *RuntimeAdapter) Start(ctx context.Context, sandboxID string, config runtime.DevToolConfig, workingDir string) (string, int, error) {
	// Map runtime.DevToolConfig -> devtool.DevToolConfig
	dtConfig := DevToolConfig{
		Type:    DevToolType(config.Type),
		Enabled: config.Enabled,
		Port:    config.Port,
		Auth: DevToolAuthConfig{
			Token:     config.AuthToken,
			AllowNone: config.AllowNoneAuth,
		},
	}

	inst, err := a.manager.Start(ctx, sandboxID, dtConfig, workingDir)
	if err != nil {
		return "", 0, err
	}
	return inst.ID, inst.Port, nil
}

// StopAll stops all dev tools for a sandbox.
func (a *RuntimeAdapter) StopAll(ctx context.Context, sandboxID string) error {
	return a.manager.StopAll(ctx, sandboxID)
}
