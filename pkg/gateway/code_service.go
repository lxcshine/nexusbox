package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CodeService provides code execution capabilities within sandboxes.
// Inspired by agent-infra/sandbox's code execution service, it supports:
// - Python code execution with timeout and resource limits
// - Node.js code execution with timeout and resource limits
// - Language info queries
// - Output truncation to prevent memory exhaustion
// - Process cleanup on timeout
type CodeService struct {
	pythonPath string
	nodePath   string

	// Execution limits
	maxExecutionTime time.Duration // max execution time
	maxOutputBytes   int           // max output size per stream
}

// CodeServiceConfig holds configuration for code execution limits.
type CodeServiceConfig struct {
	MaxExecutionTime time.Duration
	MaxOutputBytes   int
}

// NewCodeService creates a new CodeService with default limits.
func NewCodeService() *CodeService {
	return NewCodeServiceWithConfig(CodeServiceConfig{
		MaxExecutionTime: 30 * time.Second,
		MaxOutputBytes:   5 * 1024 * 1024, // 5MB
	})
}

// NewCodeServiceWithConfig creates a new CodeService with custom limits.
func NewCodeServiceWithConfig(config CodeServiceConfig) *CodeService {
	if config.MaxExecutionTime <= 0 {
		config.MaxExecutionTime = 30 * time.Second
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = 5 * 1024 * 1024
	}
	return &CodeService{
		pythonPath:       findExecutable("python3", "python"),
		nodePath:         findExecutable("node"),
		maxExecutionTime: config.MaxExecutionTime,
		maxOutputBytes:   config.MaxOutputBytes,
	}
}

// --- Request/Response types ---

// CodeExecuteRequest is the request for code execution.
type CodeExecuteRequest struct {
	Language string            `json:"language"` // "python" or "nodejs"
	Code     string            `json:"code"`
	Timeout  int               `json:"timeout,omitempty"` // seconds
	WorkDir  string            `json:"workDir,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

// CodeExecuteResponse is the response for code execution.
type CodeExecuteResponse struct {
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timedOut"`
	Runtime  string `json:"runtime"`
}

// CodeInfoResponse is the response for code info.
type CodeInfoResponse struct {
	Languages []LanguageInfo `json:"languages"`
}

// LanguageInfo contains information about a code language runtime.
type LanguageInfo struct {
	Language  string `json:"language"`
	Version   string `json:"version"`
	Path      string `json:"path"`
	Available bool   `json:"available"`
}

// --- Handlers ---

// Execute handles code execution requests.
func (c *CodeService) Execute(w http.ResponseWriter, r *http.Request) {
	var req CodeExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	switch strings.ToLower(req.Language) {
	case "python", "python3":
		c.executePython(w, &req)
	case "nodejs", "node", "javascript", "js":
		c.executeNodeJS(w, &req)
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported language: %s", req.Language))
	}
}

// Info returns information about available code runtimes.
func (c *CodeService) Info(w http.ResponseWriter, r *http.Request) {
	languages := []LanguageInfo{
		{
			Language:  "python",
			Version:   c.getPythonVersion(),
			Path:      c.pythonPath,
			Available: c.pythonPath != "",
		},
		{
			Language:  "nodejs",
			Version:   c.getNodeVersion(),
			Path:      c.nodePath,
			Available: c.nodePath != "",
		},
	}

	writeJSON(w, http.StatusOK, CodeInfoResponse{
		Languages: languages,
	})
}

// ExecuteCode executes code synchronously and returns the result.
// Used by the E2B compatibility layer and other internal callers.
func (c *CodeService) ExecuteCode(language, code string, timeoutSec int32) (stdout, stderr string, exitCode int, err error) {
	req := &CodeExecuteRequest{
		Language: language,
		Code:     code,
		Timeout:  int(timeoutSec),
	}

	switch strings.ToLower(language) {
	case "python", "python3":
		return c.executePythonSync(req)
	case "nodejs", "node", "javascript", "js":
		return c.executeNodeJSSync(req)
	default:
		return "", fmt.Sprintf("unsupported language: %s", language), -1, fmt.Errorf("unsupported language: %s", language)
	}
}

// executePythonSync executes Python code synchronously.
func (c *CodeService) executePythonSync(req *CodeExecuteRequest) (string, string, int, error) {
	if c.pythonPath == "" {
		return "", "python is not available", -1, fmt.Errorf("python not available")
	}

	tmpFile, err := os.CreateTemp("", "nexusbox-python-*.py")
	if err != nil {
		return "", err.Error(), -1, err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(req.Code)
	tmpFile.Close()

	timeout := c.maxExecutionTime
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.pythonPath, tmpFile.Name())
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdout := &limitedWriter{max: c.maxOutputBytes}
	stderr := &limitedWriter{max: c.maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	exitCode := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// executeNodeJSSync executes Node.js code synchronously.
func (c *CodeService) executeNodeJSSync(req *CodeExecuteRequest) (string, string, int, error) {
	if c.nodePath == "" {
		return "", "nodejs is not available", -1, fmt.Errorf("nodejs not available")
	}

	tmpFile, err := os.CreateTemp("", "nexusbox-node-*.js")
	if err != nil {
		return "", err.Error(), -1, err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(req.Code)
	tmpFile.Close()

	timeout := c.maxExecutionTime
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.nodePath, tmpFile.Name())
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdout := &limitedWriter{max: c.maxOutputBytes}
	stderr := &limitedWriter{max: c.maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	exitCode := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// executePython executes Python code with timeout and output limits.
func (c *CodeService) executePython(w http.ResponseWriter, req *CodeExecuteRequest) {
	if c.pythonPath == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeUnsupported, "python is not available")
		return
	}

	// Write code to a temp file
	tmpFile, err := os.CreateTemp("", "nexusbox-python-*.py")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, "failed to create temp file")
		return
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(req.Code); err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, "failed to write temp file")
		return
	}
	tmpFile.Close()

	// Determine timeout
	timeout := c.maxExecutionTime
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.pythonPath, tmpFile.Name())
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdout := &limitedWriter{max: c.maxOutputBytes}
	stderr := &limitedWriter{max: c.maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	resp := CodeExecuteResponse{
		ExitCode: 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Runtime:  "python",
	}

	if stdout.truncated || stderr.truncated {
		resp.Stderr += "\n[output truncated due to size limit]"
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			resp.TimedOut = true
			resp.ExitCode = -1
			resp.Stderr += "\n[execution timed out]"
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			resp.ExitCode = exitErr.ExitCode()
		} else {
			resp.ExitCode = -1
			resp.Stderr = err.Error()
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// executeNodeJS executes Node.js code with timeout and output limits.
func (c *CodeService) executeNodeJS(w http.ResponseWriter, req *CodeExecuteRequest) {
	if c.nodePath == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeUnsupported, "nodejs is not available")
		return
	}

	// Write code to a temp file
	tmpFile, err := os.CreateTemp("", "nexusbox-node-*.js")
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, "failed to create temp file")
		return
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(req.Code); err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, "failed to write temp file")
		return
	}
	tmpFile.Close()

	// Determine timeout
	timeout := c.maxExecutionTime
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.nodePath, tmpFile.Name())
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdout := &limitedWriter{max: c.maxOutputBytes}
	stderr := &limitedWriter{max: c.maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	resp := CodeExecuteResponse{
		ExitCode: 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Runtime:  "nodejs",
	}

	if stdout.truncated || stderr.truncated {
		resp.Stderr += "\n[output truncated due to size limit]"
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			resp.TimedOut = true
			resp.ExitCode = -1
			resp.Stderr += "\n[execution timed out]"
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			resp.ExitCode = exitErr.ExitCode()
		} else {
			resp.ExitCode = -1
			resp.Stderr = err.Error()
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// limitedWriter is an io.Writer that limits the total bytes written,
// preventing memory exhaustion from runaway processes.
type limitedWriter struct {
	buf       strings.Builder
	max       int
	written   int
	truncated bool
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.truncated {
		return len(p), nil // silently discard
	}
	remaining := lw.max - lw.written
	if remaining <= 0 {
		lw.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		lw.buf.Write(p[:remaining])
		lw.written = lw.max
		lw.truncated = true
		return len(p), nil
	}
	lw.buf.Write(p)
	lw.written += len(p)
	return len(p), nil
}

func (lw *limitedWriter) String() string {
	return lw.buf.String()
}

// getPythonVersion returns the Python version string.
func (c *CodeService) getPythonVersion() string {
	if c.pythonPath == "" {
		return ""
	}
	cmd := exec.Command(c.pythonPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

// getNodeVersion returns the Node.js version string.
func (c *CodeService) getNodeVersion() string {
	if c.nodePath == "" {
		return ""
	}
	cmd := exec.Command(c.nodePath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

// findExecutable finds an executable in PATH.
func findExecutable(names ...string) string {
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}
