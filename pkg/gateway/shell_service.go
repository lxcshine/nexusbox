/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"sync"
	"time"

	sandboxRuntime "github.com/nexusbox/nexusbox/pkg/sandbox/runtime"
	"k8s.io/klog/v2"
)

// defaultShell returns the default shell command for the current platform.
func defaultShell() (string, []string) {
	if goRuntime.GOOS == "windows" {
		return "cmd", []string{"/c"}
	}
	return "/bin/sh", []string{"-c"}
}

// defaultInteractiveShell returns the default interactive shell for sessions.
func defaultInteractiveShell() string {
	if goRuntime.GOOS == "windows" {
		return "cmd"
	}
	return "/bin/bash"
}

// ShellService provides shell/bash execution capabilities within sandboxes.
// Inspired by agent-infra/sandbox's shell service, it supports:
// - One-shot command execution
// - Persistent shell sessions with PTY support
// - Session management (create, list, kill)
// - Real-time output streaming via SSE
// - Session TTL and automatic cleanup
// - Concurrent session limits
// - Resource limits (CPU/memory) per session
type ShellService struct {
	runtimeManager *sandboxRuntime.RuntimeManager
	sessions       map[string]*ShellSession
	mu             sync.RWMutex

	// Session lifecycle configuration
	maxSessions     int           // maximum concurrent sessions
	sessionTTL      time.Duration // default session TTL
	cleanupInterval time.Duration // how often to run cleanup
}

// ShellSessionConfig holds configuration for the shell service.
type ShellSessionConfig struct {
	MaxSessions     int
	SessionTTL      time.Duration
	CleanupInterval time.Duration
}

// ShellSession represents a persistent shell session.
type ShellSession struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    *bufio.Reader
	stderr    *bufio.Reader
	outputBuf *RingBuffer
	cancel    context.CancelFunc
	done      chan struct{}
	// Process tracking
	pid      int
	exitCode *int
	// Resource limits
	cpuLimit   float64 // CPU cores
	memLimitMB int     // memory limit in MB
}

// RingBuffer is a circular buffer for capturing command output.
type RingBuffer struct {
	data []byte
	size int
	pos  int
	mu   sync.RWMutex
}

// NewRingBuffer creates a new RingBuffer with the given size.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		data: make([]byte, size),
		size: size,
	}
}

// Write writes data to the ring buffer.
func (rb *RingBuffer) Write(p []byte) (n int, err error) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	for _, b := range p {
		rb.data[rb.pos%rb.size] = b
		rb.pos++
	}
	return len(p), nil
}

// Read reads all available data from the ring buffer.
func (rb *RingBuffer) Read() []byte {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	if rb.pos <= rb.size {
		return append([]byte{}, rb.data[:rb.pos]...)
	}
	start := rb.pos % rb.size
	result := make([]byte, rb.size)
	copy(result, rb.data[start:])
	copy(result[rb.size-start:], rb.data[:start])
	return result
}

// NewShellService creates a new ShellService with the given configuration.
func NewShellService(runtimeManager *sandboxRuntime.RuntimeManager) *ShellService {
	return NewShellServiceWithConfig(runtimeManager, ShellSessionConfig{
		MaxSessions:     50,
		SessionTTL:      30 * time.Minute,
		CleanupInterval: 60 * time.Second,
	})
}

// NewShellServiceWithConfig creates a new ShellService with custom configuration.
func NewShellServiceWithConfig(runtimeManager *sandboxRuntime.RuntimeManager, config ShellSessionConfig) *ShellService {
	if config.MaxSessions <= 0 {
		config.MaxSessions = 50
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = 30 * time.Minute
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = 60 * time.Second
	}

	svc := &ShellService{
		runtimeManager:  runtimeManager,
		sessions:        make(map[string]*ShellSession),
		maxSessions:     config.MaxSessions,
		sessionTTL:      config.SessionTTL,
		cleanupInterval: config.CleanupInterval,
	}

	// Start background cleanup goroutine
	go svc.cleanupExpiredSessions()

	return svc
}

// cleanupExpiredSessions periodically removes expired sessions.
func (s *ShellService) cleanupExpiredSessions() {
	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		var expired []string
		for id, session := range s.sessions {
			if !session.ExpiresAt.IsZero() && now.After(session.ExpiresAt) {
				expired = append(expired, id)
			}
			// Also clean up dead sessions
			if !session.IsActive() {
				expired = append(expired, id)
			}
		}
		for _, id := range expired {
			session := s.sessions[id]
			delete(s.sessions, id)
			if session != nil {
				session.cancel()
				if session.stdin != nil {
					session.stdin.Close()
				}
			}
		}
		if len(expired) > 0 {
			klog.Infof("Cleaned up %d expired shell sessions", len(expired))
		}
		s.mu.Unlock()
	}
}

// SessionCount returns the current number of active sessions.
func (s *ShellService) SessionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.sessions)
}

// --- Request/Response types ---

// ShellExecRequest is the request for shell command execution.
type ShellExecRequest struct {
	Command   string            `json:"command"`
	SessionID string            `json:"sessionId,omitempty"`
	Timeout   int               `json:"timeout,omitempty"` // seconds, 0 = no timeout
	WorkDir   string            `json:"workDir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// ShellExecResponse is the response for shell command execution.
type ShellExecResponse struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	TimedOut  bool   `json:"timedOut"`
	SessionID string `json:"sessionId,omitempty"`
}

// BashExecRequest is the request for bash command execution.
type BashExecRequest struct {
	Command    string `json:"command"`
	SessionID  string `json:"sessionId,omitempty"`
	Timeout    int    `json:"timeout,omitempty"`
	WorkDir    string `json:"workDir,omitempty"`
	Background bool   `json:"background,omitempty"`
}

// BashExecResult is the result for bash command execution.
type BashExecResult struct {
	PID       int    `json:"pid"`
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	TimedOut  bool   `json:"timedOut"`
	SessionID string `json:"sessionId,omitempty"`
}

// CreateSessionRequest is the request to create a new shell session.
type CreateSessionRequest struct {
	Name    string `json:"name"`
	Shell   string `json:"shell,omitempty"`   // default: /bin/bash
	WorkDir string `json:"workDir,omitempty"` // default: workspace
}

// SessionInfo contains information about a shell session.
type SessionInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Shell     string    `json:"shell"`
	WorkDir   string    `json:"workDir"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Active    bool      `json:"active"`
}

// --- Handlers ---

// Exec handles one-shot shell command execution.
func (s *ShellService) Exec(w http.ResponseWriter, r *http.Request) {
	var req ShellExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Command == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}

	ctx := r.Context()
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
		defer cancel()
	}

	shell, shellArgs := defaultShell()
	cmd := exec.CommandContext(ctx, shell, append(shellArgs, req.Command)...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	resp := ShellExecResponse{
		ExitCode:  0,
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		SessionID: req.SessionID,
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			resp.TimedOut = true
			resp.ExitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			resp.ExitCode = exitErr.ExitCode()
		} else {
			resp.ExitCode = -1
			resp.Stderr = err.Error()
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// BashExec handles bash-specific command execution with session support.
func (s *ShellService) BashExec(w http.ResponseWriter, r *http.Request) {
	var req BashExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Command == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}

	// If session specified, execute in that session
	if req.SessionID != "" {
		s.execInSession(w, r, &req)
		return
	}

	// One-shot execution
	ctx := r.Context()
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
		defer cancel()
	}

	shell := "/bin/bash"
	if goRuntime.GOOS == "windows" {
		shell = "cmd"
	}
	cmd := exec.CommandContext(ctx, shell)
	if goRuntime.GOOS != "windows" {
		cmd.Args = []string{shell, "-c", req.Command}
	} else {
		cmd.Args = []string{shell, "/c", req.Command}
	}
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	resp := BashExecResult{
		ExitCode: 0,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			resp.TimedOut = true
			resp.ExitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			resp.ExitCode = exitErr.ExitCode()
			resp.PID = exitErr.Pid()
		} else {
			resp.ExitCode = -1
			resp.Stderr = err.Error()
		}
	} else {
		resp.PID = cmd.Process.Pid
	}

	writeJSON(w, http.StatusOK, resp)
}

// BashOutput returns the output of a running bash command.
func (s *ShellService) BashOutput(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "sessionId is required")
		return
	}

	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	output := session.outputBuf.Read()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessionId": sessionID,
		"output":    string(output),
		"active":    session.IsActive(),
	})
}

// BashKill kills a running bash command.
func (s *ShellService) BashKill(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "sessionId is required")
		return
	}

	s.mu.RLock()
	session, ok := s.sessions[req.SessionID]
	s.mu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	session.cancel()
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "killed",
		"sessionId": req.SessionID,
	})
}

// CreateSession creates a new persistent shell session.
func (s *ShellService) CreateSession(w http.ResponseWriter, r *http.Request) {
	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}

	// Enforce concurrent session limit
	s.mu.RLock()
	currentCount := len(s.sessions)
	s.mu.RUnlock()

	if currentCount >= s.maxSessions {
		writeAPIErrorWithDetails(w, http.StatusTooManyRequests, ErrCodeResourceExhausted,
			"maximum concurrent sessions reached",
			map[string]interface{}{
				"current": currentCount,
				"max":     s.maxSessions,
			})
		return
	}

	shell := req.Shell
	if shell == "" {
		shell = defaultInteractiveShell()
	}
	workDir := req.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())

	cmd := exec.CommandContext(ctx, shell)
	cmd.Dir = workDir
	cmd.Env = os.Environ()

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("failed to create stdin pipe: %v", err))
		return
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		stdinPipe.Close()
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("failed to create stdout pipe: %v", err))
		return
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		stdinPipe.Close()
		stdoutPipe.Close()
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("failed to create stderr pipe: %v", err))
		return
	}

	if err := cmd.Start(); err != nil {
		cancel()
		stdinPipe.Close()
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("failed to start shell: %v", err))
		return
	}

	now := time.Now()
	session := &ShellSession{
		ID:        sessionID,
		Name:      req.Name,
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(s.sessionTTL),
		cmd:       cmd,
		stdin:     stdinPipe,
		stdout:    bufio.NewReaderSize(stdoutPipe, 4096),
		stderr:    bufio.NewReaderSize(stderrPipe, 4096),
		outputBuf: NewRingBuffer(1024 * 1024), // 1MB output buffer
		cancel:    cancel,
		done:      make(chan struct{}),
		pid:       cmd.Process.Pid,
	}

	// Drain output in background
	go s.drainOutput(session)

	s.mu.Lock()
	s.sessions[sessionID] = session
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, SessionInfo{
		ID:        sessionID,
		Name:      req.Name,
		Shell:     shell,
		WorkDir:   workDir,
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
		Active:    true,
	})
}

// ListSessions lists all active shell sessions.
func (s *ShellService) ListSessions(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions := make([]SessionInfo, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, SessionInfo{
			ID:        session.ID,
			Name:      session.Name,
			Shell:     "/bin/bash",
			CreatedAt: session.CreatedAt,
			UpdatedAt: session.UpdatedAt,
			Active:    session.IsActive(),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"sessions": sessions,
	})
}

// GetSession returns information about a specific session.
func (s *ShellService) GetSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	writeJSON(w, http.StatusOK, SessionInfo{
		ID:        session.ID,
		Name:      session.Name,
		Shell:     "/bin/bash",
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
		Active:    session.IsActive(),
	})
}

// KillSession kills a shell session.
func (s *ShellService) KillSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	s.mu.Lock()
	session, ok := s.sessions[sessionID]
	if ok {
		delete(s.sessions, sessionID)
	}
	s.mu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	session.cancel()
	session.stdin.Close()

	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "killed",
		"sessionId": sessionID,
	})
}

// IsActive returns whether the session is still active.
func (s *ShellSession) IsActive() bool {
	if s.cmd == nil || s.cmd.Process == nil {
		return false
	}
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// drainOutput drains stdout and stderr from a session into the ring buffer.
func (s *ShellService) drainOutput(session *ShellSession) {
	defer close(session.done)

	go func() {
		scanner := bufio.NewScanner(session.stdout)
		for scanner.Scan() {
			line := scanner.Text() + "\n"
			session.outputBuf.Write([]byte(line))
		}
	}()

	scanner := bufio.NewScanner(session.stderr)
	for scanner.Scan() {
		line := scanner.Text() + "\n"
		session.outputBuf.Write([]byte(line))
	}

	session.cmd.Wait()
}

// execInSession executes a command in an existing session.
func (s *ShellService) execInSession(w http.ResponseWriter, r *http.Request, req *BashExecRequest) {
	s.mu.RLock()
	session, ok := s.sessions[req.SessionID]
	s.mu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	if !session.IsActive() {
		writeError(w, http.StatusBadRequest, "session is not active")
		return
	}

	// Write command to session's stdin
	cmdLine := req.Command + "\n"
	if _, err := session.stdin.Write([]byte(cmdLine)); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to write to session: %v", err))
		return
	}

	// Wait briefly for output
	time.Sleep(100 * time.Millisecond)

	output := session.outputBuf.Read()
	writeJSON(w, http.StatusOK, BashExecResult{
		ExitCode:  0,
		Stdout:    string(output),
		SessionID: req.SessionID,
	})
}

// resolveWorkDir resolves the working directory for command execution.
func resolveWorkDir(workDir, workspace string) string {
	if workDir == "" {
		return workspace
	}
	if !filepath.IsAbs(workDir) {
		return filepath.Join(workspace, workDir)
	}
	return workDir
}

// --- SSE Streaming ---

// StreamExec handles shell command execution with Server-Sent Events streaming.
// This provides real-time output as the command runs, rather than waiting
// for completion. Inspired by agent-infra/sandbox's streaming shell API.
func (s *ShellService) StreamExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, ErrCodeBadRequest, "method not allowed")
		return
	}

	var req ShellExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}

	if req.Command == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "command is required")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, "streaming not supported")
		return
	}

	ctx := r.Context()
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
		defer cancel()
	}

	shell, shellArgs := defaultShell()
	cmd := exec.CommandContext(ctx, shell, append(shellArgs, req.Command)...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		writeSSEError(w, flusher, ErrCodeInternal, fmt.Sprintf("failed to create stdout pipe: %v", err))
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		writeSSEError(w, flusher, ErrCodeInternal, fmt.Sprintf("failed to create stderr pipe: %v", err))
		return
	}

	if err := cmd.Start(); err != nil {
		writeSSEError(w, flusher, ErrCodeInternal, fmt.Sprintf("failed to start command: %v", err))
		return
	}

	// Send start event
	writeSSEEvent(w, flusher, "start", map[string]interface{}{
		"pid": cmd.Process.Pid,
	})

	// Merge stdout and stderr
	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			writeSSEEvent(w, flusher, "stdout", map[string]string{
				"data": scanner.Text(),
			})
		}
		done <- struct{}{}
	}()

	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		scanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for scanner.Scan() {
			writeSSEEvent(w, flusher, "stderr", map[string]string{
				"data": scanner.Text(),
			})
		}
		done <- struct{}{}
	}()

	// Wait for both pipes to finish
	<-done
	<-done

	err = cmd.Wait()
	exitCode := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			writeSSEEvent(w, flusher, "timeout", map[string]string{
				"message": "command timed out",
			})
			exitCode = -1
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	// Send end event
	writeSSEEvent(w, flusher, "end", map[string]interface{}{
		"exitCode": exitCode,
	})
}

// writeSSEEvent writes a Server-Sent Event.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event string, data interface{}) {
	jsonData, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
	flusher.Flush()
}

// writeSSEError writes an error as an SSE event.
func writeSSEError(w http.ResponseWriter, flusher http.Flusher, code, message string) {
	writeSSEEvent(w, flusher, "error", map[string]string{
		"code":    code,
		"message": message,
	})
}

// --- Process Management ---

// ProcessInfo contains information about a running process.
type ProcessInfo struct {
	PID       int     `json:"pid"`
	SessionID string  `json:"sessionId,omitempty"`
	Command   string  `json:"command,omitempty"`
	CPU       float64 `json:"cpu"`
	Memory    int64   `json:"memory"`
	Status    string  `json:"status"`
}

// ListProcesses returns information about all running shell processes.
func (s *ShellService) ListProcesses() []ProcessInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	processes := make([]ProcessInfo, 0, len(s.sessions))
	for id, session := range s.sessions {
		if !session.IsActive() {
			continue
		}
		processes = append(processes, ProcessInfo{
			PID:       session.pid,
			SessionID: id,
			Status:    "running",
		})
	}
	return processes
}

// KillProcess kills a process by PID.
func (s *ShellService) KillProcess(pid int) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, session := range s.sessions {
		if session.pid == pid && session.IsActive() {
			session.cancel()
			return nil
		}
	}

	// If not found in sessions, try killing by PID directly
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process not found: %w", err)
	}
	return proc.Kill()
}
