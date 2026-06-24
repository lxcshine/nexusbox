/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// Hub implements the MCP (Model Context Protocol) Hub that aggregates
// multiple MCP servers and provides a unified interface for AI agents.
// Inspired by agent-infra/sandbox's MCP Hub architecture.
type Hub struct {
	servers    map[string]Server
	transport  Transport
	workspace  string
	mu         sync.RWMutex
	port       int
	httpServer *http.Server
	stopCh     chan struct{}
}

// Server is the interface for an MCP server.
type Server interface {
	// Name returns the server name.
	Name() string
	// ListTools returns the list of available tools.
	ListTools(ctx context.Context) ([]Tool, error)
	// CallTool invokes a tool.
	CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*CallToolResult, error)
}

// Transport defines the MCP transport interface.
type Transport interface {
	// Send sends a message.
	Send(ctx context.Context, message *JSONRPCMessage) (*JSONRPCMessage, error)
}

// JSONRPCMessage represents a JSON-RPC 2.0 message.
type JSONRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC error.
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Tool represents an MCP tool definition.
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

// InputSchema defines the input schema for a tool.
type InputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]PropertyDef `json:"properties,omitempty"`
	Required   []string               `json:"required,omitempty"`
}

// PropertyDef defines a property in the input schema.
type PropertyDef struct {
	Type        string      `json:"type"`
	Description string      `json:"description,omitempty"`
	Enum        []string    `json:"enum,omitempty"`
	Default     interface{} `json:"default,omitempty"`
}

// CallToolResult is the result of a tool call.
type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock represents a content block in a tool result.
type ContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// HubConfig holds configuration for the MCP Hub.
type HubConfig struct {
	Port      int
	Workspace string
}

// NewHub creates a new MCP Hub.
func NewHub(config *HubConfig) *Hub {
	workspace := config.Workspace
	if workspace == "" {
		workspace = "/home/sandbox"
	}
	h := &Hub{
		servers:   make(map[string]Server),
		port:      config.Port,
		workspace: workspace,
		stopCh:    make(chan struct{}),
	}

	// Register built-in servers with real implementations
	h.RegisterServer(NewShellMCPServer(workspace))
	h.RegisterServer(NewFileMCPServer(workspace))
	h.RegisterServer(NewCodeMCPServer(workspace))
	h.RegisterServer(NewBrowserMCPServer(workspace))

	return h
}

// RegisterServer registers an MCP server with the hub.
func (h *Hub) RegisterServer(server Server) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.servers[server.Name()] = server
	klog.Infof("Registered MCP server: %s", server.Name())
}

// UnregisterServer removes an MCP server from the hub.
func (h *Hub) UnregisterServer(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.servers, name)
	klog.Infof("Unregistered MCP server: %s", name)
}

// ListServers returns the names of all registered servers.
func (h *Hub) ListServers() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	names := make([]string, 0, len(h.servers))
	for name := range h.servers {
		names = append(names, name)
	}
	return names
}

// Start starts the MCP Hub HTTP server.
func (h *Hub) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", h.handleMCP)
	mux.HandleFunc("/mcp/", h.handleMCP)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok")
	})

	h.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", h.port),
		Handler: mux,
	}

	// Probe port availability before starting the goroutine
	ln, err := net.Listen("tcp", h.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("mcp-hub: failed to listen on %s: %w", h.httpServer.Addr, err)
	}

	go func() {
		klog.Infof("MCP Hub listening on :%d", h.port)
		if err := h.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			klog.Errorf("MCP Hub error: %v", err)
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			h.Shutdown()
		case <-h.stopCh:
			return
		}
	}()

	return nil
}

// Shutdown gracefully shuts down the MCP Hub.
func (h *Hub) Shutdown() {
	close(h.stopCh)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if h.httpServer != nil {
		h.httpServer.Shutdown(ctx)
	}
}

// handleMCP handles MCP JSON-RPC requests.
func (h *Hub) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var msg JSONRPCMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		writeMCPError(w, nil, -32700, "Parse error")
		return
	}

	if msg.JSONRPC != "2.0" {
		writeMCPError(w, msg.ID, -32600, "Invalid Request: jsonrpc must be 2.0")
		return
	}

	var result interface{}
	var rpcErr *JSONRPCError

	switch msg.Method {
	case "initialize":
		result = h.handleInitialize(r.Context())
	case "tools/list":
		result = h.handleToolsList(r.Context(), msg)
	case "tools/call":
		result, rpcErr = h.handleToolsCall(r.Context(), msg)
	case "resources/list":
		result = map[string]interface{}{"resources": []interface{}{}}
	case "prompts/list":
		result = map[string]interface{}{"prompts": []interface{}{}}
	case "ping":
		result = map[string]interface{}{}
	default:
		writeMCPError(w, msg.ID, -32601, fmt.Sprintf("Method not found: %s", msg.Method))
		return
	}

	if rpcErr != nil {
		writeMCPError(w, msg.ID, rpcErr.Code, rpcErr.Message)
		return
	}

	resultBytes, _ := json.Marshal(result)
	resp := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  resultBytes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleInitialize handles the MCP initialize request.
func (h *Hub) handleInitialize(ctx context.Context) interface{} {
	return map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{
				"listChanged": true,
			},
		},
		"serverInfo": map[string]interface{}{
			"name":    "nexusbox-mcp-hub",
			"version": "0.1.0",
		},
	}
}

// handleToolsList handles the tools/list request.
func (h *Hub) handleToolsList(ctx context.Context, msg JSONRPCMessage) interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var allTools []Tool
	for _, server := range h.servers {
		tools, err := server.ListTools(ctx)
		if err != nil {
			klog.Warningf("Failed to list tools from server %s: %v", server.Name(), err)
			continue
		}
		allTools = append(allTools, tools...)
	}

	return map[string]interface{}{
		"tools": allTools,
	}
}

// handleToolsCall handles the tools/call request.
func (h *Hub) handleToolsCall(ctx context.Context, msg JSONRPCMessage) (interface{}, *JSONRPCError) {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return nil, &JSONRPCError{Code: -32602, Message: "Invalid params"}
	}

	// Find the server that owns this tool
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, server := range h.servers {
		tools, err := server.ListTools(ctx)
		if err != nil {
			continue
		}
		for _, tool := range tools {
			if tool.Name == params.Name {
				result, err := server.CallTool(ctx, params.Name, params.Arguments)
				if err != nil {
					return nil, &JSONRPCError{Code: -32603, Message: err.Error()}
				}
				return result, nil
			}
		}
	}

	return nil, &JSONRPCError{Code: -32602, Message: fmt.Sprintf("Tool not found: %s", params.Name)}
}

// writeMCPError writes a JSON-RPC error response.
func writeMCPError(w http.ResponseWriter, id interface{}, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	resp := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
	json.NewEncoder(w).Encode(resp)
}

// parseStringSlice parses a string or string slice from interface{}.
func parseStringSlice(v interface{}) []string {
	if v == nil {
		return nil
	}
	if s, ok := v.(string); ok {
		return []string{s}
	}
	if slice, ok := v.([]interface{}); ok {
		result := make([]string, 0, len(slice))
		for _, item := range slice {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

// ensure Hub methods reference strings correctly
var _ = strings.TrimSpace
