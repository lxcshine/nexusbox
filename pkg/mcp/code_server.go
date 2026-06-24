/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
*/

package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// CodeMCPServer provides real code execution tools via MCP.
// AI agents can execute Python and Node.js code in the sandbox,
// which is the core capability for autonomous coding workflows.
type CodeMCPServer struct {
	workspace string
}

// NewCodeMCPServer creates a new CodeMCPServer bound to the given workspace.
func NewCodeMCPServer(workspace string) *CodeMCPServer {
	return &CodeMCPServer{workspace: workspace}
}

// Name returns the server name.
func (s *CodeMCPServer) Name() string { return "code" }

// ListTools returns the list of code tools.
func (s *CodeMCPServer) ListTools(ctx context.Context) ([]Tool, error) {
	return []Tool{
		{
			Name:        "code_run",
			Description: "Execute Python or Node.js code in the sandbox. The code is written to a temp file and executed. Returns stdout, stderr, and exit code. Use this for running scripts, testing code, data analysis, etc.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"language": {Type: "string", Description: "Programming language", Enum: []string{"python", "nodejs"}},
					"code":     {Type: "string", Description: "The source code to execute"},
					"timeout":  {Type: "integer", Description: "Timeout in seconds (default 30, max 120)", Default: 30},
				},
				Required: []string{"language", "code"},
			},
		},
		{
			Name:        "code_install",
			Description: "Install a package using pip (Python) or npm (Node.js). Useful for adding dependencies before running code.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"language": {Type: "string", Description: "Package manager", Enum: []string{"python", "nodejs"}},
					"packages": {Type: "string", Description: "Package name(s) to install (space-separated for multiple)"},
				},
				Required: []string{"language", "packages"},
			},
		},
	}, nil
}

// CallTool invokes a code tool.
func (s *CodeMCPServer) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*CallToolResult, error) {
	switch name {
	case "code_run":
		return s.runCode(ctx, arguments)
	case "code_install":
		return s.installPackage(ctx, arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// runCode executes Python or Node.js code.
func (s *CodeMCPServer) runCode(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	language, _ := arguments["language"].(string)
	code, _ := arguments["code"].(string)
	if language == "" || code == "" {
		return nil, fmt.Errorf("language and code are required")
	}

	timeoutSec := 30
	if t, ok := arguments["timeout"].(float64); ok && t > 0 {
		timeoutSec = int(t)
	}
	if timeoutSec > 120 {
		timeoutSec = 120
	}

	// Write code to a temp file
	var ext, runner string
	switch language {
	case "python", "py":
		ext = ".py"
		runner = "python3"
	case "nodejs", "node", "js":
		ext = ".js"
		runner = "node"
	default:
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Unsupported language: %s", language)}},
			IsError: true,
		}, nil
	}

	tmpFile := filepath.Join(s.workspace, ".nexusbox-tmp", fmt.Sprintf("exec-%d%s", time.Now().UnixNano(), ext))
	if err := os.MkdirAll(filepath.Dir(tmpFile), 0755); err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to create temp dir: %v", err)}},
			IsError: true,
		}, nil
	}
	defer os.Remove(tmpFile)

	if err := os.WriteFile(tmpFile, []byte(code), 0644); err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to write code file: %v", err)}},
			IsError: true,
		}, nil
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(execCtx, runner, tmpFile)
	} else {
		cmd = exec.CommandContext(execCtx, runner, tmpFile)
	}
	cmd.Dir = s.workspace

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
					{Type: "text", Text: fmt.Sprintf("Code execution timed out after %d seconds\n\n--- stdout ---\n%s\n--- stderr ---\n%s",
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

// installPackage installs a package using pip or npm.
func (s *CodeMCPServer) installPackage(ctx context.Context, arguments map[string]interface{}) (*CallToolResult, error) {
	language, _ := arguments["language"].(string)
	packages, _ := arguments["packages"].(string)
	if language == "" || packages == "" {
		return nil, fmt.Errorf("language and packages are required")
	}

	timeoutSec := 120
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch language {
	case "python", "py":
		cmd = exec.CommandContext(execCtx, "pip3", "install", "--break-system-packages", packages)
	case "nodejs", "node", "js":
		cmd = exec.CommandContext(execCtx, "npm", "install", "-g", packages)
	default:
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Unsupported language: %s", language)}},
			IsError: true,
		}, nil
	}
	cmd.Dir = s.workspace

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	result := fmt.Sprintf("Install %s packages: %s\nExit code: %d\n\n--- stdout ---\n%s\n--- stderr ---\n%s",
		language, packages, exitCode, stdout.String(), stderr.String())

	return &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: result},
		},
		IsError: exitCode != 0,
	}, nil
}
