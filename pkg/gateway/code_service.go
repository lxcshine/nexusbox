package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CodeService provides code execution capabilities within sandboxes.
// Inspired by agent-infra/sandbox's code execution service, it supports:
// - Python code execution with timeout and resource limits
// - Node.js code execution with timeout and resource limits
// - Go code execution (compiled to a temp binary, then run)
// - Java code execution (compiled + run via javac/java)
// - Language info queries
// - Output truncation to prevent memory exhaustion
// - Process cleanup on timeout
type CodeService struct {
	pythonPath string
	nodePath   string
	goPath     string
	javaPath   string
	javacPath  string

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
		goPath:           findExecutable("go"),
		javaPath:         findExecutable("java"),
		javacPath:        findExecutable("javac"),
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
	case "go", "golang":
		c.executeGo(w, &req)
	case "java":
		c.executeJava(w, &req)
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
		{
			Language:  "go",
			Version:   c.getGoVersion(),
			Path:      c.goPath,
			Available: c.goPath != "",
		},
		{
			Language:  "java",
			Version:   c.getJavaVersion(),
			Path:      c.javaPath,
			Available: c.javaPath != "" && c.javacPath != "",
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
	case "go", "golang":
		return c.executeGoSync(req)
	case "java":
		return c.executeJavaSync(req)
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

// --- Go execution ---

// executeGoSync compiles and runs Go code synchronously. The code is wrapped
// in `package main` if it does not already declare a package, written to a
// temp module, built with `go build`, and executed.
func (c *CodeService) executeGoSync(req *CodeExecuteRequest) (string, string, int, error) {
	if c.goPath == "" {
		return "", "go is not available", -1, fmt.Errorf("go not available")
	}

	workDir, cleanup, err := c.prepareGoWorkspace(req)
	if err != nil {
		return "", err.Error(), -1, err
	}
	defer cleanup()

	timeout := c.maxExecutionTime
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	// Build the binary.
	buildCtx, buildCancel := context.WithTimeout(context.Background(), timeout)
	defer buildCancel()
	binary := filepath.Join(workDir, "out.bin")
	build := exec.CommandContext(buildCtx, c.goPath, "build", "-o", binary, ".")
	build.Dir = workDir
	build.Env = c.buildEnv(req, []string{"GO111MODULE=on", "GOFLAGS=-mod=mod", "GOPROXY=off"})
	buildOut := &limitedWriter{max: c.maxOutputBytes}
	build.Stdout = buildOut
	build.Stderr = buildOut
	if err := build.Run(); err != nil {
		return buildOut.String(), fmt.Sprintf("[go build failed]\n%s", buildOut.String()), -1, err
	}

	// Run the binary.
	runCtx, runCancel := context.WithTimeout(context.Background(), timeout)
	defer runCancel()
	run := exec.CommandContext(runCtx, binary)
	if req.WorkDir != "" {
		run.Dir = req.WorkDir
	} else {
		run.Dir = workDir
	}
	run.Env = c.buildEnv(req, nil)
	stdout := &limitedWriter{max: c.maxOutputBytes}
	stderr := &limitedWriter{max: c.maxOutputBytes}
	run.Stdout = stdout
	run.Stderr = stderr

	err = run.Run()
	exitCode := 0
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// executeGo runs Go code with timeout and output limits, writing the HTTP response.
func (c *CodeService) executeGo(w http.ResponseWriter, req *CodeExecuteRequest) {
	if c.goPath == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeUnsupported, "go is not available")
		return
	}

	workDir, cleanup, err := c.prepareGoWorkspace(req)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, "failed to prepare go workspace")
		return
	}
	defer cleanup()

	timeout := c.maxExecutionTime
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	// Build.
	buildCtx, buildCancel := context.WithTimeout(context.Background(), timeout)
	defer buildCancel()
	binary := filepath.Join(workDir, "out.bin")
	build := exec.CommandContext(buildCtx, c.goPath, "build", "-o", binary, ".")
	build.Dir = workDir
	build.Env = c.buildEnv(req, []string{"GO111MODULE=on", "GOFLAGS=-mod=mod", "GOPROXY=off"})
	buildOut := &limitedWriter{max: c.maxOutputBytes}
	build.Stdout = buildOut
	build.Stderr = buildOut
	if err := build.Run(); err != nil {
		writeJSON(w, http.StatusOK, CodeExecuteResponse{
			ExitCode: -1,
			Stdout:   buildOut.String(),
			Stderr:   fmt.Sprintf("[go build failed]\n%s", buildOut.String()),
			Runtime:  "go",
		})
		return
	}

	// Run.
	runCtx, runCancel := context.WithTimeout(context.Background(), timeout)
	defer runCancel()
	run := exec.CommandContext(runCtx, binary)
	if req.WorkDir != "" {
		run.Dir = req.WorkDir
	} else {
		run.Dir = workDir
	}
	run.Env = c.buildEnv(req, nil)
	stdout := &limitedWriter{max: c.maxOutputBytes}
	stderr := &limitedWriter{max: c.maxOutputBytes}
	run.Stdout = stdout
	run.Stderr = stderr

	err = run.Run()
	resp := CodeExecuteResponse{
		ExitCode: 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Runtime:  "go",
	}
	if stdout.truncated || stderr.truncated {
		resp.Stderr += "\n[output truncated due to size limit]"
	}
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
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

// prepareGoWorkspace writes the user code to a temp module directory and
// returns the directory plus a cleanup func. If the code does not start with
// a `package` declaration, it is wrapped in `package main`.
func (c *CodeService) prepareGoWorkspace(req *CodeExecuteRequest) (string, func(), error) {
	workDir, err := os.MkdirTemp("", "nexusbox-go-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(workDir) }

	code := strings.TrimSpace(req.Code)
	if !strings.HasPrefix(code, "package ") {
		code = "package main\n\n" + code
	}
	if err := os.WriteFile(filepath.Join(workDir, "main.go"), []byte(code), 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	// A minimal go.mod so `go build` works offline without a module cache.
	modContent := "module nexusboxtmp\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte(modContent), 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	return workDir, cleanup, nil
}

// buildEnv returns os.Environ plus request env plus any extra vars.
func (c *CodeService) buildEnv(req *CodeExecuteRequest, extra []string) []string {
	env := os.Environ()
	for k, v := range req.Env {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	env = append(env, extra...)
	return env
}

// --- Java execution ---

// executeJavaSync compiles and runs Java code synchronously. The code is
// written to Main.java (the public class must be named Main), compiled with
// javac, and executed with java.
func (c *CodeService) executeJavaSync(req *CodeExecuteRequest) (string, string, int, error) {
	if c.javaPath == "" || c.javacPath == "" {
		return "", "java is not available", -1, fmt.Errorf("java not available")
	}

	workDir, cleanup, err := c.prepareJavaWorkspace(req)
	if err != nil {
		return "", err.Error(), -1, err
	}
	defer cleanup()

	timeout := c.maxExecutionTime
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	// Compile.
	compileCtx, compileCancel := context.WithTimeout(context.Background(), timeout)
	defer compileCancel()
	compile := exec.CommandContext(compileCtx, c.javacPath, "Main.java")
	compile.Dir = workDir
	compile.Env = c.buildEnv(req, nil)
	compileOut := &limitedWriter{max: c.maxOutputBytes}
	compile.Stdout = compileOut
	compile.Stderr = compileOut
	if err := compile.Run(); err != nil {
		return compileOut.String(), fmt.Sprintf("[javac failed]\n%s", compileOut.String()), -1, err
	}

	// Run.
	runCtx, runCancel := context.WithTimeout(context.Background(), timeout)
	defer runCancel()
	run := exec.CommandContext(runCtx, c.javaPath, "Main")
	if req.WorkDir != "" {
		run.Dir = req.WorkDir
	} else {
		run.Dir = workDir
	}
	run.Env = c.buildEnv(req, nil)
	stdout := &limitedWriter{max: c.maxOutputBytes}
	stderr := &limitedWriter{max: c.maxOutputBytes}
	run.Stdout = stdout
	run.Stderr = stderr

	err = run.Run()
	exitCode := 0
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// executeJava runs Java code with timeout and output limits, writing the HTTP response.
func (c *CodeService) executeJava(w http.ResponseWriter, req *CodeExecuteRequest) {
	if c.javaPath == "" || c.javacPath == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeUnsupported, "java is not available")
		return
	}

	workDir, cleanup, err := c.prepareJavaWorkspace(req)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, "failed to prepare java workspace")
		return
	}
	defer cleanup()

	timeout := c.maxExecutionTime
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	// Compile.
	compileCtx, compileCancel := context.WithTimeout(context.Background(), timeout)
	defer compileCancel()
	compile := exec.CommandContext(compileCtx, c.javacPath, "Main.java")
	compile.Dir = workDir
	compile.Env = c.buildEnv(req, nil)
	compileOut := &limitedWriter{max: c.maxOutputBytes}
	compile.Stdout = compileOut
	compile.Stderr = compileOut
	if err := compile.Run(); err != nil {
		writeJSON(w, http.StatusOK, CodeExecuteResponse{
			ExitCode: -1,
			Stdout:   compileOut.String(),
			Stderr:   fmt.Sprintf("[javac failed]\n%s", compileOut.String()),
			Runtime:  "java",
		})
		return
	}

	// Run.
	runCtx, runCancel := context.WithTimeout(context.Background(), timeout)
	defer runCancel()
	run := exec.CommandContext(runCtx, c.javaPath, "Main")
	if req.WorkDir != "" {
		run.Dir = req.WorkDir
	} else {
		run.Dir = workDir
	}
	run.Env = c.buildEnv(req, nil)
	stdout := &limitedWriter{max: c.maxOutputBytes}
	stderr := &limitedWriter{max: c.maxOutputBytes}
	run.Stdout = stdout
	run.Stderr = stderr

	err = run.Run()
	resp := CodeExecuteResponse{
		ExitCode: 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Runtime:  "java",
	}
	if stdout.truncated || stderr.truncated {
		resp.Stderr += "\n[output truncated due to size limit]"
	}
	if err != nil {
		if runCtx.Err() == context.DeadlineExceeded {
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

// prepareJavaWorkspace writes the user code to Main.java in a temp dir. The
// class must be named Main (the standard sandbox convention); if the code
// declares a different public class, we rewrite the declaration to Main.
func (c *CodeService) prepareJavaWorkspace(req *CodeExecuteRequest) (string, func(), error) {
	workDir, err := os.MkdirTemp("", "nexusbox-java-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(workDir) }

	code := strings.TrimSpace(req.Code)
	// Rewrite `public class <Anything>` to `public class Main` so the file
	// name (Main.java) matches the public class name, as javac requires.
	publicClassRe := regexp.MustCompile(`(?m)^public\s+class\s+\w+`)
	if publicClassRe.MatchString(code) {
		code = publicClassRe.ReplaceAllString(code, "public class Main")
	} else if !regexp.MustCompile(`(?m)^class\s+Main\b`).MatchString(code) {
		// No class at all: wrap in a Main class with a main method.
		code = "public class Main {\n    public static void main(String[] args) {\n" + code + "\n    }\n}"
	}
	if err := os.WriteFile(filepath.Join(workDir, "Main.java"), []byte(code), 0644); err != nil {
		cleanup()
		return "", nil, err
	}
	return workDir, cleanup, nil
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

// getGoVersion returns the Go toolchain version (e.g. "go1.22.3").
func (c *CodeService) getGoVersion() string {
	if c.goPath == "" {
		return ""
	}
	cmd := exec.Command(c.goPath, "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

// getJavaVersion returns the JRE version string.
func (c *CodeService) getJavaVersion() string {
	if c.javaPath == "" {
		return ""
	}
	cmd := exec.Command(c.javaPath, "-version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// `java -version` prints to stderr and exits 0 on most JDKs, but some
		// return non-zero; the version is still in the output.
		if len(output) == 0 {
			return "unknown"
		}
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
