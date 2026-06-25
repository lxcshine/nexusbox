package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// backgroundProcesses tracks all background shell commands started via MCP.
var backgroundProcesses sync.Map

// ShellMCPServer provides real shell execution tools via MCP.
// AI agents can use these tools to execute commands in the sandbox workspace.
type ShellMCPServer struct {
	workspace string
}

// NewShellMCPServer creates a new ShellMCPServer bound to the given workspace.
func NewShellMCPServer(workspace string) *ShellMCPServer {
	return &ShellMCPServer{workspace: workspace}
}

// Name returns the server name.
func (s *ShellMCPServer) Name() string { return "shell" }

// ListTools returns the list of shell tools.
func (s *ShellMCPServer) ListTools(ctx context.Context) ([]Tool, error) {
	return []Tool{
		{
			Name:        "shell_exec",
			Description: "Execute a shell command in the sandbox and return stdout, stderr, and exit code. The command runs in the workspace directory. Use this for running builds, tests, git commands, etc.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"command": {Type: "string", Description: "The shell command to execute"},
					"timeout": {Type: "integer", Description: "Timeout in seconds (default 30, max 300)", Default: 30},
					"workDir": {Type: "string", Description: "Working directory (default: workspace root)"},
				},
				Required: []string{"command"},
			},
		},
		{
			Name:        "shell_background",
			Description: "Start a long-running shell command in the background. Returns immediately with a process ID. Use shell_check to get output later.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"command": {Type: "string", Description: "The shell command to run in background"},
					"workDir": {Type: "string", Description: "Working directory"},
				},
				Required: []string{"command"},
			},
		},
		{
			Name:        "shell_check",
			Description: "Check the status and output of a background shell command",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"pid": {Type: "string", Description: "Process ID returned by shell_background"},
				},
				Required: []string{"pid"},
			},
		},
	}, nil
}

// backgroundProcess tracks a background shell command.
type backgroundProcess struct {
	cmd      *exec.Cmd
	stdout   *bytes.Buffer
	stderr   *bytes.Buffer
	done     chan struct{}
	exitCode int
	finished bool
}

// CallTool invokes a shell tool.
func (s *ShellMCPServer) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*CallToolResult, error) {
	switch name {
	case "shell_exec":
		return s.execCommand(ctx, arguments)
	case "shell_background":
		return s.startBackground(ctx, arguments)
	case "shell_check":
		return s.checkBackground(ctx, arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// execCommand executes a shell command synchronously and returns the output.
func (s *ShellMCPServer) execCommand(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	command, _ := arguments["command"].(string)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	timeoutSec := 30
	if t, ok := arguments["timeout"].(float64); ok && t > 0 {
		timeoutSec = int(t)
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}

	workDir := s.workspace
	if wd, ok := arguments["workDir"].(string); ok && wd != "" {
		workDir = wd
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(execCtx, "cmd", "/c", command)
	} else {
		cmd = exec.CommandContext(execCtx, "/bin/sh", "-c", command)
	}
	cmd.Dir = workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if execCtx.Err() == context.DeadlineExceeded {
			return &CallToolResult{
				Content: []ContentBlock{
					{Type: "text", Text: fmt.Sprintf("Command timed out after %d seconds\n\n--- stdout ---\n%s\n--- stderr ---\n%s",
						timeoutSec, stdout.String(), stderr.String())},
				},
				IsError: true,
			}, nil
		} else {
			exitCode = -1
		}
	}

	result := fmt.Sprintf("Exit code: %d\n\n--- stdout ---\n%s\n--- stderr ---\n%s",
		exitCode, stdout.String(), stderr.String())

	return &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: result},
		},
		IsError: exitCode != 0,
	}, nil
}

// startBackground starts a command in the background.
func (s *ShellMCPServer) startBackground(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	command, _ := arguments["command"].(string)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	workDir := s.workspace
	if wd, ok := arguments["workDir"].(string); ok && wd != "" {
		workDir = wd
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("/bin/sh", "-c", command)
	}
	cmd.Dir = workDir

	bp := &backgroundProcess{
		cmd:    cmd,
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		done:   make(chan struct{}),
	}
	cmd.Stdout = bp.stdout
	cmd.Stderr = bp.stderr

	if err := cmd.Start(); err != nil {
		return &CallToolResult{
			Content: []ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Failed to start command: %v", err)},
			},
			IsError: true,
		}, nil
	}

	pid := fmt.Sprintf("%d", cmd.Process.Pid)

	go func() {
		defer close(bp.done)
		err := cmd.Wait()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				bp.exitCode = exitErr.ExitCode()
			} else {
				bp.exitCode = -1
			}
		}
		bp.finished = true
	}()

	// Store in global registry
	backgroundProcesses.Store(pid, bp)

	return &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: fmt.Sprintf("Started background process (pid: %s)\nCommand: %s", pid, command)},
		},
	}, nil
}

// checkBackground checks the status of a background process.
func (s *ShellMCPServer) checkBackground(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	pid, _ := arguments["pid"].(string)
	if pid == "" {
		return nil, fmt.Errorf("pid is required")
	}

	val, ok := backgroundProcesses.Load(pid)
	if !ok {
		return &CallToolResult{
			Content: []ContentBlock{
				{Type: "text", Text: fmt.Sprintf("Process %s not found", pid)},
			},
			IsError: true,
		}, nil
	}

	bp := val.(*backgroundProcess)

	status := "running"
	if bp.finished {
		status = fmt.Sprintf("finished (exit code: %d)", bp.exitCode)
	}

	result := fmt.Sprintf("Process %s: %s\n\n--- stdout ---\n%s\n--- stderr ---\n%s",
		pid, status, bp.stdout.String(), bp.stderr.String())

	return &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: result},
		},
		IsError: bp.finished && bp.exitCode != 0,
	}, nil
}
