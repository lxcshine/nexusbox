package devtool

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"k8s.io/klog/v2"
)

// Launcher starts dev tool processes with security hardening.
// All tools are bound to 127.0.0.1 so they are only reachable via the
// Gateway reverse proxy, never directly exposed.
type Launcher struct {
	jupyterPath    string
	codeServerPath string
}

// NewLauncher detects dev tool binary paths from PATH or known locations.
// Missing binaries are not an error — the corresponding tool will simply
// fail at launch time with a clear message.
func NewLauncher() *Launcher {
	l := &Launcher{
		jupyterPath:    findBinary("jupyter", "jupyter.exe"),
		codeServerPath: findBinary("code-server", "code-server.exe"),
	}
	if l.jupyterPath != "" {
		klog.Infof("DevTool launcher: jupyter found at %s", l.jupyterPath)
	} else {
		klog.Warning("DevTool launcher: jupyter binary not found in PATH; JupyterLab tool will be unavailable")
	}
	if l.codeServerPath != "" {
		klog.Infof("DevTool launcher: code-server found at %s", l.codeServerPath)
	} else {
		klog.Warning("DevTool launcher: code-server binary not found in PATH; code-server tool will be unavailable")
	}
	return l
}

// JupyterPath returns the detected jupyter binary path.
func (l *Launcher) JupyterPath() string { return l.jupyterPath }

// CodeServerPath returns the detected code-server binary path.
func (l *Launcher) CodeServerPath() string { return l.codeServerPath }

// LaunchJupyter starts a JupyterLab instance.
//
// Security hardening:
//   - --ip=127.0.0.1: only listen on loopback (proxied by Gateway)
//   - --NotebookApp.token=<random>: always require token unless AllowNone
//   - --NotebookApp.allow_origin=”: restrict CORS
//   - --no-browser: headless mode
//   - Working directory = sandbox's isolated working dir
func (l *Launcher) LaunchJupyter(config DevToolConfig, workingDir string, port int) (*DevToolInstance, error) {
	if l.jupyterPath == "" {
		return nil, fmt.Errorf("jupyter binary not found; install JupyterLab to use this tool")
	}
	if port == 0 {
		return nil, fmt.Errorf("port must be allocated before launch")
	}

	// Determine auth token
	token := config.Auth.Token
	if token == "" && !config.Auth.AllowNone {
		token = generateToken(24)
	}

	// Build command line — all flags cross-platform
	// Use ServerApp.* flags (NotebookApp.* is deprecated but still works)
	args := []string{
		"lab",
		"--ip=127.0.0.1",
		"--port=" + strconv.Itoa(port),
		"--no-browser",
		"--ServerApp.token=" + token,
		"--ServerApp.password=",
		"--ServerApp.allow_origin=",
		"--ServerApp.trust_xheaders=True",
		"--ServerApp.base_url=/",
		"--ServerApp.allow_remote_access=False",
	}
	if config.Auth.AllowNone {
		// Override: disable all auth
		args = []string{
			"lab",
			"--ip=127.0.0.1",
			"--port=" + strconv.Itoa(port),
			"--no-browser",
			"--ServerApp.token=",
			"--ServerApp.password=",
			"--ServerApp.allow_origin=",
			"--ServerApp.trust_xheaders=True",
			"--ServerApp.base_url=/",
			"--ServerApp.allow_remote_access=False",
		}
		token = ""
	}

	// Ensure working directory exists
	if workingDir == "" {
		return nil, fmt.Errorf("workingDir must not be empty")
	}

	cmd := exec.Command(l.jupyterPath, args...)
	cmd.Dir = workingDir

	// Redirect stdout/stderr to a log file so the process doesn't die
	// when writing output (critical on Windows where no inherited pipe = crash)
	logFile, err := os.OpenFile(
		filepath.Join(workingDir, ".devtool-jupyter.log"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create jupyter log file: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Set environment: always redirect Jupyter runtime/data dirs to the
	// working directory to avoid permission issues with system paths.
	// This also isolates Jupyter state per-sandbox.
	runtimeDir := filepath.Join(workingDir, ".jupyter-runtime")
	dataDir := filepath.Join(workingDir, ".jupyter-data")
	os.MkdirAll(runtimeDir, 0755)
	os.MkdirAll(dataDir, 0755)

	env := os.Environ()
	env = append(env, "JUPYTER_RUNTIME_DIR="+runtimeDir)
	env = append(env, "JUPYTER_DATA_DIR="+dataDir)
	env = append(env, "JUPYTER_CONFIG_DIR="+filepath.Join(workingDir, ".jupyter-config"))
	for k, v := range config.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start jupyter: %w", err)
	}

	inst := &DevToolInstance{
		ID:         generateInstanceID("jupyter"),
		Type:       DevToolJupyterLab,
		Port:       port,
		PID:        cmd.Process.Pid,
		WorkingDir: workingDir,
		Status:     DevToolStatusPending,
		StartedAt:  time.Now(),
		Token:      token,
		cmd:        cmd,
	}

	klog.Infof("Launched JupyterLab: pid=%d port=%d workdir=%s", inst.PID, port, workingDir)
	return inst, nil
}

// LaunchCodeServer starts a code-server instance.
//
// Security hardening:
//   - --bind-addr 127.0.0.1:<port>: loopback only
//   - --auth password: always require password unless AllowNone
//   - --password <random>: auto-generated if not provided
//   - Working directory = sandbox's isolated working dir
func (l *Launcher) LaunchCodeServer(config DevToolConfig, workingDir string, port int) (*DevToolInstance, error) {
	if l.codeServerPath == "" {
		return nil, fmt.Errorf("code-server binary not found; install code-server to use this tool")
	}
	if port == 0 {
		return nil, fmt.Errorf("port must be allocated before launch")
	}

	// Determine auth password
	password := config.Auth.Password
	if password == "" && !config.Auth.AllowNone {
		password = generateToken(20)
	}

	args := []string{
		"--bind-addr", "127.0.0.1:" + strconv.Itoa(port),
		"--auth", "password",
		"--password", password,
		"--disable-telemetry",
		"--disable-update-check",
		"--user-data-dir", filepath.Join(workingDir, ".code-server-data"),
		"--extensions-dir", filepath.Join(workingDir, ".code-server-extensions"),
	}

	if config.Auth.AllowNone {
		args = []string{
			"--bind-addr", "127.0.0.1:" + strconv.Itoa(port),
			"--auth", "none",
			"--disable-telemetry",
			"--disable-update-check",
			"--user-data-dir", filepath.Join(workingDir, ".code-server-data"),
			"--extensions-dir", filepath.Join(workingDir, ".code-server-extensions"),
		}
		password = ""
	}

	if workingDir == "" {
		return nil, fmt.Errorf("workingDir must not be empty")
	}

	cmd := exec.Command(l.codeServerPath, args...)
	cmd.Dir = workingDir

	// Redirect stdout/stderr to a log file so the process doesn't die
	// when writing output (critical on Windows where no inherited pipe = crash)
	csLogFile, err := os.OpenFile(
		filepath.Join(workingDir, ".devtool-code-server.log"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create code-server log file: %w", err)
	}
	cmd.Stdout = csLogFile
	cmd.Stderr = csLogFile

	if len(config.Env) > 0 {
		env := cmd.Env
		if env == nil {
			env = append(env, "PATH=/usr/local/bin:/usr/bin:/bin")
		}
		for k, v := range config.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start code-server: %w", err)
	}

	inst := &DevToolInstance{
		ID:         generateInstanceID("code-server"),
		Type:       DevToolCodeServer,
		Port:       port,
		PID:        cmd.Process.Pid,
		WorkingDir: workingDir,
		Status:     DevToolStatusPending,
		StartedAt:  time.Now(),
		Token:      password,
		cmd:        cmd,
	}

	klog.Infof("Launched code-server: pid=%d port=%d workdir=%s", inst.PID, port, workingDir)
	return inst, nil
}

// Stop terminates the dev tool process.
func (l *Launcher) Stop(inst *DevToolInstance) error {
	if inst == nil || inst.cmd == nil || inst.cmd.Process == nil {
		return nil
	}
	if err := inst.cmd.Process.Kill(); err != nil {
		klog.Warningf("Failed to kill dev tool %s (pid=%d): %v", inst.ID, inst.PID, err)
		return err
	}
	inst.Status = DevToolStatusStopped
	klog.Infof("Stopped dev tool %s (pid=%d)", inst.ID, inst.PID)
	return nil
}

// Wait waits for the dev tool process to exit and updates status.
func (l *Launcher) Wait(inst *DevToolInstance) error {
	if inst == nil || inst.cmd == nil {
		return nil
	}
	err := inst.cmd.Wait()
	if inst.Status == DevToolStatusRunning || inst.Status == DevToolStatusPending {
		if err != nil {
			inst.Status = DevToolStatusFailed
		} else {
			inst.Status = DevToolStatusStopped
		}
	}
	return err
}

// findBinary searches for a binary in PATH, trying platform-specific names.
func findBinary(names ...string) string {
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err == nil {
			return path
		}
	}
	// Check common install locations
	if runtime.GOOS == "windows" {
		// code-server may be installed in %USERPROFILE%\.local\bin
		for _, name := range names {
			candidates := []string{
				filepath.Join(getEnv("USERPROFILE"), ".local", "bin", name),
				filepath.Join(getEnv("LOCALAPPDATA"), "Programs", name),
				filepath.Join("C:\\Program Files", name),
			}
			for _, c := range candidates {
				if c != "" && fileExists(c) {
					return c
				}
			}
		}
	}
	return ""
}

// generateToken creates a cryptographically random hex token.
func generateToken(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based token if crypto/rand fails
		return fmt.Sprintf("nexusbox-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// generateInstanceID creates a unique instance ID.
func generateInstanceID(toolType string) string {
	b := make([]byte, 6)
	rand.Read(b)
	return fmt.Sprintf("dt-%s-%s", toolType, hex.EncodeToString(b))
}

func getEnv(key string) string { return getEnvOS(key) }

func fileExists(path string) bool { return fileExistsOS(path) }
