/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
*/

package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FileMCPServer provides real file operation tools via MCP.
// All paths are resolved relative to the workspace root to prevent
// path traversal attacks. AI agents can read, write, list, and search files.
type FileMCPServer struct {
	workspace string
}

// NewFileMCPServer creates a new FileMCPServer bound to the given workspace.
func NewFileMCPServer(workspace string) *FileMCPServer {
	return &FileMCPServer{workspace: workspace}
}

// Name returns the server name.
func (s *FileMCPServer) Name() string { return "file" }

// ListTools returns the list of file tools.
func (s *FileMCPServer) ListTools(ctx context.Context) ([]Tool, error) {
	return []Tool{
		{
			Name:        "file_read",
			Description: "Read the contents of a file from the sandbox workspace. Returns the text content. For binary files, use encoding=base64.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path":     {Type: "string", Description: "Path to the file (relative to workspace or absolute within workspace)"},
					"encoding": {Type: "string", Description: "Encoding: utf-8 (default) or base64", Enum: []string{"utf-8", "base64"}},
					"offset":   {Type: "integer", Description: "Byte offset to start reading from (default: 0)"},
					"limit":    {Type: "integer", Description: "Maximum bytes to read (default: 1MB)"},
				},
				Required: []string{"path"},
			},
		},
		{
			Name:        "file_write",
			Description: "Write content to a file in the sandbox workspace. Creates parent directories if needed. Use append=true to append instead of overwrite.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path":    {Type: "string", Description: "Path to the file"},
					"content": {Type: "string", Description: "Content to write"},
					"append":  {Type: "boolean", Description: "Append to file instead of overwriting (default: false)"},
				},
				Required: []string{"path", "content"},
			},
		},
		{
			Name:        "file_list",
			Description: "List files and directories in a path. Returns names, sizes, and types.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path":      {Type: "string", Description: "Directory path (default: workspace root)"},
					"recursive": {Type: "boolean", Description: "List recursively (default: false)"},
				},
			},
		},
		{
			Name:        "file_search",
			Description: "Search for files matching a glob pattern (e.g. *.go, **/*.py). Returns matching file paths.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path":    {Type: "string", Description: "Search root path (default: workspace root)"},
					"pattern": {Type: "string", Description: "Glob pattern (e.g. *.go, **/*.py)"},
				},
				Required: []string{"pattern"},
			},
		},
		{
			Name:        "file_replace",
			Description: "Search and replace text in a file. By default replaces the first occurrence; use replaceAll=true for all.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path":       {Type: "string", Description: "Path to the file"},
					"search":     {Type: "string", Description: "Text to search for"},
					"replace":    {Type: "string", Description: "Replacement text"},
					"replaceAll": {Type: "boolean", Description: "Replace all occurrences (default: false)"},
				},
				Required: []string{"path", "search", "replace"},
			},
		},
		{
			Name:        "file_delete",
			Description: "Delete a file or empty directory from the sandbox workspace.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"path": {Type: "string", Description: "Path to the file or directory to delete"},
				},
				Required: []string{"path"},
			},
		},
		{
			Name:        "file_move",
			Description: "Move or rename a file/directory within the sandbox workspace.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertyDef{
					"src":  {Type: "string", Description: "Source path"},
					"dest": {Type: "string", Description: "Destination path"},
				},
				Required: []string{"src", "dest"},
			},
		},
	}, nil
}

// CallTool invokes a file tool.
func (s *FileMCPServer) CallTool(ctx context.Context, name string, arguments map[string]interface{}) (*CallToolResult, error) {
	switch name {
	case "file_read":
		return s.readFile(arguments)
	case "file_write":
		return s.writeFile(arguments)
	case "file_list":
		return s.listDir(arguments)
	case "file_search":
		return s.searchFiles(arguments)
	case "file_replace":
		return s.replaceInFile(arguments)
	case "file_delete":
		return s.deleteFile(arguments)
	case "file_move":
		return s.moveFile(arguments)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

// resolvePath resolves a path relative to the workspace, preventing traversal.
func (s *FileMCPServer) resolvePath(p string) (string, error) {
	if p == "" {
		p = "."
	}

	var fullPath string
	if filepath.IsAbs(p) {
		fullPath = filepath.Clean(p)
	} else {
		fullPath = filepath.Join(s.workspace, p)
	}

	absWorkspace, err := filepath.Abs(s.workspace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace: %w", err)
	}

	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path: %w", err)
	}

	if !strings.HasPrefix(absPath, absWorkspace) {
		return "", fmt.Errorf("path %s is outside workspace", p)
	}

	return absPath, nil
}

// readFile reads a file from the workspace.
func (s *FileMCPServer) readFile(arguments map[string]interface{}) (*CallToolResult, error) {
	path, _ := arguments["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	encoding := "utf-8"
	if enc, ok := arguments["encoding"].(string); ok && enc != "" {
		encoding = enc
	}

	fullPath, err := s.resolvePath(path)
	if err != nil {
		return &CallToolResult{Content: []ContentBlock{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to read file: %v", err)}},
			IsError: true,
		}, nil
	}

	offset := int64(0)
	if o, ok := arguments["offset"].(float64); ok {
		offset = int64(o)
	}
	limit := int64(1024 * 1024) // 1MB default
	if l, ok := arguments["limit"].(float64); ok && l > 0 {
		limit = int64(l)
	}

	if offset > 0 && offset < int64(len(data)) {
		data = data[offset:]
	}
	if limit > 0 && limit < int64(len(data)) {
		data = data[:limit]
	}

	var content string
	if encoding == "base64" {
		content = base64.StdEncoding.EncodeToString(data)
	} else {
		content = string(data)
	}

	return &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: content},
		},
	}, nil
}

// writeFile writes content to a file.
func (s *FileMCPServer) writeFile(arguments map[string]interface{}) (*CallToolResult, error) {
	path, _ := arguments["path"].(string)
	content, _ := arguments["content"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	append := false
	if a, ok := arguments["append"].(bool); ok {
		append = a
	}

	fullPath, err := s.resolvePath(path)
	if err != nil {
		return &CallToolResult{Content: []ContentBlock{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to create directories: %v", err)}},
			IsError: true,
		}, nil
	}

	flag := os.O_CREATE | os.O_WRONLY
	if append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}

	f, err := os.OpenFile(fullPath, flag, 0644)
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to open file: %v", err)}},
			IsError: true,
		}, nil
	}
	defer f.Close()

	written, err := f.WriteString(content)
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to write: %v", err)}},
			IsError: true,
		}, nil
	}

	return &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: fmt.Sprintf("Wrote %d bytes to %s", written, path)},
		},
	}, nil
}

// listDir lists files in a directory.
func (s *FileMCPServer) listDir(arguments map[string]interface{}) (*CallToolResult, error) {
	path, _ := arguments["path"].(string)
	if path == "" {
		path = "."
	}

	recursive := false
	if r, ok := arguments["recursive"].(bool); ok {
		recursive = r
	}

	fullPath, err := s.resolvePath(path)
	if err != nil {
		return &CallToolResult{Content: []ContentBlock{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}

	var entries []string
	if recursive {
		err = filepath.Walk(fullPath, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(fullPath, p)
			if rel == "." {
				return nil
			}
			size := int64(0)
			if !info.IsDir() {
				size = info.Size()
			}
			entryType := "dir"
			if !info.IsDir() {
				entryType = "file"
			}
			entries = append(entries, fmt.Sprintf("  %s  %s  %d bytes", rel, entryType, size))
			return nil
		})
	} else {
		infos, err2 := os.ReadDir(fullPath)
		if err2 != nil {
			return &CallToolResult{
				Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to list directory: %v", err2)}},
				IsError: true,
			}, nil
		}
		for _, info := range infos {
			size := int64(0)
			entryType := "dir"
			if !info.IsDir() {
				entryType = "file"
				fi, _ := info.Info()
				if fi != nil {
					size = fi.Size()
				}
			}
			entries = append(entries, fmt.Sprintf("  %s  %s  %d bytes", info.Name(), entryType, size))
		}
	}

	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to walk directory: %v", err)}},
			IsError: true,
		}, nil
	}

	result := fmt.Sprintf("Listing %s (%d entries):\n%s", path, len(entries), strings.Join(entries, "\n"))
	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: result}},
	}, nil
}

// searchFiles searches for files matching a glob pattern.
func (s *FileMCPServer) searchFiles(arguments map[string]interface{}) (*CallToolResult, error) {
	pattern, _ := arguments["pattern"].(string)
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	path, _ := arguments["path"].(string)
	if path == "" {
		path = "."
	}

	fullPath, err := s.resolvePath(path)
	if err != nil {
		return &CallToolResult{Content: []ContentBlock{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}

	var matches []string
	err = filepath.Walk(fullPath, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		matched, _ := filepath.Match(pattern, info.Name())
		if matched {
			rel, _ := filepath.Rel(fullPath, p)
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Search failed: %v", err)}},
			IsError: true,
		}, nil
	}

	result := fmt.Sprintf("Found %d files matching '%s':\n%s", len(matches), pattern, strings.Join(matches, "\n"))
	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: result}},
	}, nil
}

// replaceInFile searches and replaces text in a file.
func (s *FileMCPServer) replaceInFile(arguments map[string]interface{}) (*CallToolResult, error) {
	path, _ := arguments["path"].(string)
	search, _ := arguments["search"].(string)
	replace, _ := arguments["replace"].(string)
	if path == "" || search == "" {
		return nil, fmt.Errorf("path and search are required")
	}

	replaceAll := false
	if ra, ok := arguments["replaceAll"].(bool); ok {
		replaceAll = ra
	}

	fullPath, err := s.resolvePath(path)
	if err != nil {
		return &CallToolResult{Content: []ContentBlock{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to read file: %v", err)}},
			IsError: true,
		}, nil
	}

	content := string(data)
	count := 0
	if replaceAll {
		count = strings.Count(content, search)
		content = strings.ReplaceAll(content, search, replace)
	} else {
		if strings.Contains(content, search) {
			content = strings.Replace(content, search, replace, 1)
			count = 1
		}
	}

	if count == 0 {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("No occurrences of '%s' found in %s", search, path)}},
		}, nil
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to write file: %v", err)}},
			IsError: true,
		}, nil
	}

	return &CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: fmt.Sprintf("Replaced %d occurrence(s) in %s", count, path)},
		},
	}, nil
}

// deleteFile deletes a file or empty directory.
func (s *FileMCPServer) deleteFile(arguments map[string]interface{}) (*CallToolResult, error) {
	path, _ := arguments["path"].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	fullPath, err := s.resolvePath(path)
	if err != nil {
		return &CallToolResult{Content: []ContentBlock{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}

	if err := os.Remove(fullPath); err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to delete: %v", err)}},
			IsError: true,
		}, nil
	}

	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Deleted: %s", path)}},
	}, nil
}

// moveFile moves or renames a file.
func (s *FileMCPServer) moveFile(arguments map[string]interface{}) (*CallToolResult, error) {
	src, _ := arguments["src"].(string)
	dest, _ := arguments["dest"].(string)
	if src == "" || dest == "" {
		return nil, fmt.Errorf("src and dest are required")
	}

	fullSrc, err := s.resolvePath(src)
	if err != nil {
		return &CallToolResult{Content: []ContentBlock{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}
	fullDest, err := s.resolvePath(dest)
	if err != nil {
		return &CallToolResult{Content: []ContentBlock{{Type: "text", Text: err.Error()}}, IsError: true}, nil
	}

	if err := os.MkdirAll(filepath.Dir(fullDest), 0755); err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to create dest directory: %v", err)}},
			IsError: true,
		}, nil
	}

	if err := os.Rename(fullSrc, fullDest); err != nil {
		return &CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Failed to move: %v", err)}},
			IsError: true,
		}, nil
	}

	return &CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: fmt.Sprintf("Moved %s -> %s", src, dest)}},
	}, nil
}
