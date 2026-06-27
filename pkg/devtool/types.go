
package devtool

import (
	"os/exec"
	"time"
)

// DevToolType defines the type of development tool.
type DevToolType string

const (
	// DevToolJupyterLab is the JupyterLab notebook environment.
	DevToolJupyterLab DevToolType = "jupyter"
	// DevToolCodeServer is the code-server (VS Code in browser) environment.
	DevToolCodeServer DevToolType = "code-server"
)

// DevToolConfig specifies how a dev tool should be launched within a sandbox.
type DevToolConfig struct {
	// Type is the kind of dev tool to launch.
	Type DevToolType `json:"type"`
	// Enabled controls whether the tool is auto-started with the sandbox.
	Enabled bool `json:"enabled"`
	// Port is the port to listen on. If 0, a port is auto-allocated.
	Port int `json:"port,omitempty"`
	// Auth holds authentication settings for the tool.
	Auth DevToolAuthConfig `json:"auth,omitempty"`
	// Env are extra environment variables passed to the tool process.
	Env map[string]string `json:"env,omitempty"`
}

// DevToolAuthConfig holds authentication settings for a dev tool.
// In production, AlwaysRequireAuth should be true. The AllowNone flag
// is intended only for local development.
type DevToolAuthConfig struct {
	// Token is the Jupyter notebook token. If empty and AllowNone is false,
	// a random token is generated.
	Token string `json:"token,omitempty"`
	// Password is the code-server password. If empty and AllowNone is false,
	// a random password is generated.
	Password string `json:"password,omitempty"`
	// AllowNone disables authentication entirely. INSECURE — dev only.
	AllowNone bool `json:"allowNone,omitempty"`
}

// DevToolStatus represents the lifecycle status of a dev tool instance.
type DevToolStatus string

const (
	DevToolStatusPending DevToolStatus = "pending"
	DevToolStatusRunning DevToolStatus = "running"
	DevToolStatusStopped DevToolStatus = "stopped"
	DevToolStatusFailed  DevToolStatus = "failed"
)

// DevToolInstance represents a running dev tool instance.
type DevToolInstance struct {
	// ID is the unique identifier for this instance.
	ID string `json:"id"`
	// SandboxID is the sandbox this instance belongs to.
	SandboxID string `json:"sandboxId"`
	// Type is the kind of dev tool.
	Type DevToolType `json:"type"`
	// Port is the port the tool is listening on.
	Port int `json:"port"`
	// PID is the OS process ID.
	PID int `json:"pid"`
	// WorkingDir is the working directory of the tool process.
	WorkingDir string `json:"workingDir"`
	// Status is the current lifecycle status.
	Status DevToolStatus `json:"status"`
	// StartedAt is when the tool was started.
	StartedAt time.Time `json:"startedAt"`
	// Token holds the generated auth token (Jupyter) or password (code-server).
	// This is only populated when auto-generated, not when provided by config.
	Token string `json:"-"` // never serialize to JSON responses
	// cmd is the underlying process (internal use only).
	cmd *exec.Cmd
}
