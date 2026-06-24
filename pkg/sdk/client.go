/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the NexusBox Go SDK client.
// Inspired by agent-infra/sandbox's SDK architecture, it provides:
// - Sandbox management
// - Shell/Bash execution
// - File operations
// - Browser automation
// - Code execution
// - MCP integration
type Client struct {
	baseURL    string
	httpClient *http.Client
	apiKey     string
	sandboxID  string

	Sandbox  *SandboxService
	Shell    *ShellService
	File     *FileService
	Browser  *BrowserService
	Code     *CodeService
	MCP      *MCPService
}

// ClientConfig holds configuration for the SDK client.
type ClientConfig struct {
	BaseURL   string
	APIKey    string
	SandboxID string
	Timeout   time.Duration
}

// NewClient creates a new SDK client.
func NewClient(config *ClientConfig) *Client {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}

	c := &Client{
		baseURL: strings.TrimRight(config.BaseURL, "/"),
		apiKey:  config.APIKey,
		sandboxID: config.SandboxID,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}

	c.Sandbox = &SandboxService{client: c}
	c.Shell = &ShellService{client: c}
	c.File = &FileService{client: c}
	c.Browser = &BrowserService{client: c}
	c.Code = &CodeService{client: c}
	c.MCP = &MCPService{client: c}

	return c
}

// doRequest performs an HTTP request.
func (c *Client) doRequest(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if c.sandboxID != "" {
		req.Header.Set("X-Sandbox-ID", c.sandboxID)
	}

	return c.httpClient.Do(req)
}

// doJSON performs an HTTP request and decodes the JSON response.
func (c *Client) doJSON(ctx context.Context, method, path string, body interface{}, result interface{}) error {
	resp, err := c.doRequest(ctx, method, path, body)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errResp map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&errResp); err == nil {
			return fmt.Errorf("API error (%d): %s", resp.StatusCode, errResp["error"])
		}
		return fmt.Errorf("API error (%d)", resp.StatusCode)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// --- Sandbox Service ---

// SandboxService provides sandbox management via the SDK.
type SandboxService struct {
	client *Client
}

// CreateSandboxRequest is the request to create a sandbox.
type CreateSandboxRequest struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace,omitempty"`
	Image     string            `json:"image,omitempty"`
	Resources map[string]string `json:"resources,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// SandboxInfo contains sandbox information.
type SandboxInfo struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Phase     string `json:"phase"`
	Image     string `json:"image"`
}

// Create creates a new sandbox.
func (s *SandboxService) Create(ctx context.Context, req *CreateSandboxRequest) (*SandboxInfo, error) {
	var result SandboxInfo
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/sandboxes", req, &result)
	return &result, err
}

// Get gets a sandbox by name.
func (s *SandboxService) Get(ctx context.Context, namespace, name string) (*SandboxInfo, error) {
	var result SandboxInfo
	err := s.client.doJSON(ctx, http.MethodGet, fmt.Sprintf("/v1/sandboxes/%s/%s", namespace, name), nil, &result)
	return &result, err
}

// List lists all sandboxes.
func (s *SandboxService) List(ctx context.Context) ([]SandboxInfo, error) {
	var result struct {
		Items []SandboxInfo `json:"items"`
	}
	err := s.client.doJSON(ctx, http.MethodGet, "/v1/sandboxes", nil, &result)
	return result.Items, err
}

// Delete deletes a sandbox.
func (s *SandboxService) Delete(ctx context.Context, namespace, name string) error {
	return s.client.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/v1/sandboxes/%s/%s", namespace, name), nil, nil)
}

// --- Shell Service ---

// ShellService provides shell execution via the SDK.
type ShellService struct {
	client *Client
}

// ExecRequest is the request for shell execution.
type ExecRequest struct {
	Command   string            `json:"command"`
	SessionID string            `json:"sessionId,omitempty"`
	Timeout   int               `json:"timeout,omitempty"`
	WorkDir   string            `json:"workDir,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// ExecResponse is the response for shell execution.
type ExecResponse struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	TimedOut  bool   `json:"timedOut"`
	SessionID string `json:"sessionId,omitempty"`
}

// Exec executes a shell command.
func (s *ShellService) Exec(ctx context.Context, req *ExecRequest) (*ExecResponse, error) {
	var result ExecResponse
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/shell/exec", req, &result)
	return &result, err
}

// CreateSessionRequest is the request to create a shell session.
type CreateSessionRequest struct {
	Name    string `json:"name"`
	Shell   string `json:"shell,omitempty"`
	WorkDir string `json:"workDir,omitempty"`
}

// SessionInfo contains session information.
type SessionInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Shell     string    `json:"shell"`
	CreatedAt time.Time `json:"createdAt"`
	Active    bool      `json:"active"`
}

// CreateSession creates a new shell session.
func (s *ShellService) CreateSession(ctx context.Context, req *CreateSessionRequest) (*SessionInfo, error) {
	var result SessionInfo
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/shell/sessions", req, &result)
	return &result, err
}

// KillSession kills a shell session.
func (s *ShellService) KillSession(ctx context.Context, sessionID string) error {
	return s.client.doJSON(ctx, http.MethodDelete, fmt.Sprintf("/v1/shell/sessions/%s", sessionID), nil, nil)
}

// ListSessions lists all shell sessions.
func (s *ShellService) ListSessions(ctx context.Context) ([]SessionInfo, error) {
	var result struct {
		Sessions []SessionInfo `json:"sessions"`
	}
	err := s.client.doJSON(ctx, http.MethodGet, "/v1/shell/sessions", nil, &result)
	return result.Sessions, err
}

// --- File Service ---

// FileService provides file operations via the SDK.
type FileService struct {
	client *Client
}

// FileReadRequest is the request for reading a file.
type FileReadRequest struct {
	Path     string `json:"path"`
	Encoding string `json:"encoding,omitempty"`
	Offset   int64  `json:"offset,omitempty"`
	Limit    int64  `json:"limit,omitempty"`
}

// FileReadResponse is the response for reading a file.
type FileReadResponse struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	Size     int64  `json:"size"`
}

// Read reads a file.
func (s *FileService) Read(ctx context.Context, req *FileReadRequest) (*FileReadResponse, error) {
	var result FileReadResponse
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/file/read", req, &result)
	return &result, err
}

// FileWriteRequest is the request for writing a file.
type FileWriteRequest struct {
	Path        string `json:"path"`
	Content     string `json:"content"`
	Encoding    string `json:"encoding,omitempty"`
	Append      bool   `json:"append,omitempty"`
	CreateDirs  bool   `json:"createDirs,omitempty"`
}

// Write writes a file.
func (s *FileService) Write(ctx context.Context, req *FileWriteRequest) error {
	return s.client.doJSON(ctx, http.MethodPost, "/v1/file/write", req, nil)
}

// FileListRequest is the request for listing a directory.
type FileListRequest struct {
	Path       string `json:"path"`
	Recursive  bool   `json:"recursive,omitempty"`
	ShowHidden bool   `json:"showHidden,omitempty"`
}

// FileEntry represents a file entry.
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// List lists files in a directory.
func (s *FileService) List(ctx context.Context, req *FileListRequest) ([]FileEntry, error) {
	var result struct {
		Entries []FileEntry `json:"entries"`
	}
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/file/list", req, &result)
	return result.Entries, err
}

// Find finds files matching a pattern.
func (s *FileService) Find(ctx context.Context, path, pattern string) ([]string, error) {
	var result struct {
		Files []string `json:"files"`
	}
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/file/find", map[string]string{
		"path": path, "pattern": pattern,
	}, &result)
	return result.Files, err
}

// Grep searches for a pattern in files.
func (s *FileService) Grep(ctx context.Context, path, pattern string) ([]GrepMatch, error) {
	var result struct {
		Matches []GrepMatch `json:"matches"`
	}
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/file/grep", map[string]string{
		"path": path, "pattern": pattern,
	}, &result)
	return result.Matches, err
}

// GrepMatch represents a grep match.
type GrepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

// --- Browser Service ---

// BrowserService provides browser automation via the SDK.
type BrowserService struct {
	client *Client
}

// NavigateRequest is the request for navigating to a URL.
type NavigateRequest struct {
	URL     string `json:"url"`
	WaitFor string `json:"waitFor,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// NavigateResponse is the response for navigation.
type NavigateResponse struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Success bool   `json:"success"`
}

// Navigate navigates to a URL.
func (s *BrowserService) Navigate(ctx context.Context, req *NavigateRequest) (*NavigateResponse, error) {
	var result NavigateResponse
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/browser/navigate", req, &result)
	return &result, err
}

// ScreenshotRequest is the request for taking a screenshot.
type ScreenshotRequest struct {
	Format   string `json:"format,omitempty"`
	FullPage bool   `json:"fullPage,omitempty"`
}

// ScreenshotResponse is the response for taking a screenshot.
type ScreenshotResponse struct {
	Image     string `json:"image"`
	Format    string `json:"format"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// Screenshot takes a screenshot.
func (s *BrowserService) Screenshot(ctx context.Context, req *ScreenshotRequest) (*ScreenshotResponse, error) {
	var result ScreenshotResponse
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/browser/screenshot", req, &result)
	return &result, err
}

// Click clicks an element.
func (s *BrowserService) Click(ctx context.Context, selector string) error {
	return s.client.doJSON(ctx, http.MethodPost, "/v1/browser/click", map[string]string{
		"selector": selector,
	}, nil)
}

// Type types text into an element.
func (s *BrowserService) Type(ctx context.Context, selector, text string) error {
	return s.client.doJSON(ctx, http.MethodPost, "/v1/browser/type", map[string]string{
		"selector": selector, "text": text,
	}, nil)
}

// Scroll scrolls the page.
func (s *BrowserService) Scroll(ctx context.Context, direction string, amount int) error {
	return s.client.doJSON(ctx, http.MethodPost, "/v1/browser/scroll", map[string]interface{}{
		"direction": direction, "amount": amount,
	}, nil)
}

// --- Code Service ---

// CodeService provides code execution via the SDK.
type CodeService struct {
	client *Client
}

// CodeExecuteRequest is the request for code execution.
type CodeExecuteRequest struct {
	Language string            `json:"language"`
	Code     string            `json:"code"`
	Timeout  int               `json:"timeout,omitempty"`
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

// Execute executes code.
func (s *CodeService) Execute(ctx context.Context, req *CodeExecuteRequest) (*CodeExecuteResponse, error) {
	var result CodeExecuteResponse
	err := s.client.doJSON(ctx, http.MethodPost, "/v1/code/execute", req, &result)
	return &result, err
}

// --- MCP Service ---

// MCPService provides MCP integration via the SDK.
type MCPService struct {
	client *Client
}

// MCPListToolsResponse is the response for listing MCP tools.
type MCPListToolsResponse struct {
	Tools []MCPTool `json:"tools"`
}

// MCPTool represents an MCP tool.
type MCPTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListTools lists available MCP tools.
func (s *MCPService) ListTools(ctx context.Context) ([]MCPTool, error) {
	var result MCPListToolsResponse
	err := s.client.doJSON(ctx, http.MethodPost, "/mcp", map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/list",
		"id":      1,
	}, &result)
	return result.Tools, err
}

// CallTool calls an MCP tool.
func (s *MCPService) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := s.client.doJSON(ctx, http.MethodPost, "/mcp", map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      name,
			"arguments": arguments,
		},
		"id": 1,
	}, &result)
	return result, err
}
