package gateway

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FileService provides file operations within the sandbox workspace.
// Inspired by agent-infra/sandbox's unified file system, it supports:
// - Read/write files with atomic writes
// - List directories
// - Find/glob/grep files
// - Move/copy/delete/stat
// - File watching
// All paths are sandboxed via PathGuard to prevent path traversal attacks.
type FileService struct {
	workspace string
	pathGuard *PathGuard
}

// NewFileService creates a new FileService.
func NewFileService(workspace string) *FileService {
	pg := NewPathGuard(workspace)
	return &FileService{
		workspace: pg.Workspace(),
		pathGuard: pg,
	}
}

// --- Request/Response types ---

// FileReadRequest is the request for reading a file.
type FileReadRequest struct {
	Path     string `json:"path"`
	Encoding string `json:"encoding,omitempty"` // "utf-8" (default) or "base64"
	Offset   int64  `json:"offset,omitempty"`   // byte offset
	Limit    int64  `json:"limit,omitempty"`    // max bytes to read
}

// FileReadResponse is the response for reading a file.
type FileReadResponse struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	Size     int64  `json:"size"`
}

// FileWriteRequest is the request for writing a file.
type FileWriteRequest struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Encoding   string `json:"encoding,omitempty"` // "utf-8" (default) or "base64"
	Append     bool   `json:"append,omitempty"`
	CreateDirs bool   `json:"createDirs,omitempty"`
}

// FileWriteResponse is the response for writing a file.
type FileWriteResponse struct {
	Path   string `json:"path"`
	Size   int    `json:"size"`
	Status string `json:"status"`
}

// FileListRequest is the request for listing a directory.
type FileListRequest struct {
	Path       string `json:"path"`
	Recursive  bool   `json:"recursive,omitempty"`
	ShowHidden bool   `json:"showHidden,omitempty"`
}

// FileListResponse is the response for listing a directory.
type FileListResponse struct {
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries"`
}

// FileEntry represents a file or directory entry.
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Mode    string    `json:"mode"`
}

// FileFindRequest is the request for finding files.
type FileFindRequest struct {
	Path     string `json:"path"`
	Pattern  string `json:"pattern"`
	MaxDepth int    `json:"maxDepth,omitempty"`
}

// FileGlobRequest is the request for globbing files.
type FileGlobRequest struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
}

// FileGrepRequest is the request for grepping files.
type FileGrepRequest struct {
	Path    string `json:"path"`
	Pattern string `json:"pattern"`
	Include string `json:"include,omitempty"` // file pattern to include
}

// FileWatchRequest is the request for watching files.
type FileWatchRequest struct {
	Paths   []string `json:"paths"`
	Timeout int      `json:"timeout,omitempty"` // seconds
}

// --- Handlers ---

// Read handles file read requests.
// ReadFile reads a file synchronously and returns its contents.
// Used by the E2B compatibility layer and other internal callers.
func (f *FileService) ReadFile(path string) ([]byte, error) {
	fullPath, err := f.resolvePathStrict(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory: %s", path)
	}
	return os.ReadFile(fullPath)
}

// WriteFile writes content to a file synchronously.
// Used by the E2B compatibility layer and other internal callers.
func (f *FileService) WriteFile(path string, content []byte) error {
	fullPath, err := f.resolvePathStrict(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// Atomic write: write to temp file then rename
	tmp := fullPath + ".tmp"
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, fullPath)
}

func (f *FileService) Read(w http.ResponseWriter, r *http.Request) {
	var req FileReadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "path is required")
		return
	}

	fullPath, err := f.resolvePathStrict(req.Path)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, ErrCodePathTraversal, err.Error())
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("file not found: %s", req.Path))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is a directory, not a file")
		return
	}

	file, err := os.Open(fullPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer file.Close()

	if req.Offset > 0 {
		if _, err := file.Seek(req.Offset, 0); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	var reader io.Reader = file
	if req.Limit > 0 {
		reader = io.LimitReader(reader, req.Limit)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	encoding := req.Encoding
	if encoding == "" {
		encoding = "utf-8"
	}

	var content string
	if encoding == "base64" {
		content = base64.StdEncoding.EncodeToString(data)
	} else {
		content = string(data)
	}

	writeJSON(w, http.StatusOK, FileReadResponse{
		Path:     req.Path,
		Content:  content,
		Encoding: encoding,
		Size:     info.Size(),
	})
}

// Write handles file write requests with atomic write semantics.
func (f *FileService) Write(w http.ResponseWriter, r *http.Request) {
	var req FileWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "path is required")
		return
	}

	fullPath, err := f.resolvePathStrict(req.Path)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, ErrCodePathTraversal, err.Error())
		return
	}

	// Create parent directories if requested
	if req.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("failed to create directories: %v", err))
			return
		}
	}

	encoding := req.Encoding
	if encoding == "" {
		encoding = "utf-8"
	}

	var data []byte
	if encoding == "base64" {
		data, err = base64.StdEncoding.DecodeString(req.Content)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid base64 content")
			return
		}
	} else {
		data = []byte(req.Content)
	}

	// Atomic write: write to temp file then rename
	tmpFile := fullPath + ".nexusbox-tmp"
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if req.Append {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
		// For append mode, write directly (can't use atomic rename)
		file, err := os.OpenFile(fullPath, flags, 0644)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("failed to open file: %v", err))
			return
		}
		defer file.Close()
		n, err := file.Write(data)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("failed to write file: %v", err))
			return
		}
		writeJSON(w, http.StatusOK, FileWriteResponse{Path: req.Path, Size: n, Status: "ok"})
		return
	}

	// Atomic write for non-append mode
	file, err := os.OpenFile(tmpFile, flags, 0644)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("failed to open file: %v", err))
		return
	}
	n, err := file.Write(data)
	file.Close()
	if err != nil {
		os.Remove(tmpFile)
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("failed to write file: %v", err))
		return
	}

	// Atomic rename
	if err := os.Rename(tmpFile, fullPath); err != nil {
		os.Remove(tmpFile)
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("failed to commit file: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, FileWriteResponse{
		Path:   req.Path,
		Size:   n,
		Status: "ok",
	})
}

// List handles directory listing requests.
func (f *FileService) List(w http.ResponseWriter, r *http.Request) {
	var req FileListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		req.Path = "/"
	}

	fullPath := f.resolvePath(req.Path)

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("path not found: %s", req.Path))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is not a directory")
		return
	}

	var entries []FileEntry
	if req.Recursive {
		err = filepath.Walk(fullPath, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			relPath, _ := filepath.Rel(fullPath, path)
			if !req.ShowHidden && strings.HasPrefix(fi.Name(), ".") && fi.Name() != "." {
				if fi.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			entries = append(entries, FileEntry{
				Name:    fi.Name(),
				Path:    filepath.Join(req.Path, relPath),
				IsDir:   fi.IsDir(),
				Size:    fi.Size(),
				ModTime: fi.ModTime(),
				Mode:    fi.Mode().String(),
			})
			return nil
		})
	} else {
		dirEntries, err2 := os.ReadDir(fullPath)
		if err2 != nil {
			writeError(w, http.StatusInternalServerError, err2.Error())
			return
		}
		for _, de := range dirEntries {
			if !req.ShowHidden && strings.HasPrefix(de.Name(), ".") {
				continue
			}
			fi, _ := de.Info()
			entries = append(entries, FileEntry{
				Name:    de.Name(),
				Path:    filepath.Join(req.Path, de.Name()),
				IsDir:   de.IsDir(),
				Size:    fi.Size(),
				ModTime: fi.ModTime(),
				Mode:    fi.Mode().String(),
			})
		}
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, FileListResponse{
		Path:    req.Path,
		Entries: entries,
	})
}

// Find handles file find requests.
func (f *FileService) Find(w http.ResponseWriter, r *http.Request) {
	var req FileFindRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		req.Path = "/"
	}
	if req.Pattern == "" {
		writeError(w, http.StatusBadRequest, "pattern is required")
		return
	}

	fullPath := f.resolvePath(req.Path)
	var results []string

	filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		matched, _ := filepath.Match(req.Pattern, info.Name())
		if matched {
			relPath, _ := filepath.Rel(fullPath, path)
			results = append(results, filepath.Join(req.Path, relPath))
		}
		return nil
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"files": results,
		"count": len(results),
	})
}

// Glob handles file glob requests.
func (f *FileService) Glob(w http.ResponseWriter, r *http.Request) {
	var req FileGlobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Pattern == "" {
		writeError(w, http.StatusBadRequest, "pattern is required")
		return
	}

	fullPath := f.resolvePath(req.Path)
	pattern := filepath.Join(fullPath, req.Pattern)

	matches, err := filepath.Glob(pattern)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid glob pattern: %v", err))
		return
	}

	// Convert to relative paths
	results := make([]string, len(matches))
	for i, m := range matches {
		relPath, _ := filepath.Rel(fullPath, m)
		results[i] = filepath.Join(req.Path, relPath)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"files": results,
		"count": len(results),
	})
}

// Grep handles file grep requests.
func (f *FileService) Grep(w http.ResponseWriter, r *http.Request) {
	var req FileGrepRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Pattern == "" {
		writeError(w, http.StatusBadRequest, "pattern is required")
		return
	}

	fullPath := f.resolvePath(req.Path)

	type GrepMatch struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Content string `json:"content"`
	}

	var matches []GrepMatch

	filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// Apply include filter
		if req.Include != "" {
			matched, _ := filepath.Match(req.Include, info.Name())
			if !matched {
				return nil
			}
		}

		// Skip binary files and very large files
		if info.Size() > 10*1024*1024 {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.Contains(line, req.Pattern) {
				relPath, _ := filepath.Rel(fullPath, path)
				matches = append(matches, GrepMatch{
					File:    filepath.Join(req.Path, relPath),
					Line:    i + 1,
					Content: strings.TrimSpace(line),
				})
			}
		}
		return nil
	})

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"matches": matches,
		"count":   len(matches),
	})
}

// Watch handles file watch requests.
func (f *FileService) Watch(w http.ResponseWriter, r *http.Request) {
	var req FileWatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Paths) == 0 {
		writeError(w, http.StatusBadRequest, "paths are required")
		return
	}

	// Record initial state
	initialState := make(map[string]time.Time)
	for _, p := range req.Paths {
		fullPath := f.resolvePath(p)
		if info, err := os.Stat(fullPath); err == nil {
			initialState[p] = info.ModTime()
		}
	}

	// Poll for changes
	timeout := time.Duration(req.Timeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	deadline := time.Now().Add(timeout)
	var changes []string

	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)

		for _, p := range req.Paths {
			fullPath := f.resolvePath(p)
			if info, err := os.Stat(fullPath); err == nil {
				if initialModTime, ok := initialState[p]; ok {
					if info.ModTime().After(initialModTime) {
						changes = append(changes, p)
						delete(initialState, p)
					}
				}
			} else if os.IsNotExist(err) {
				if _, ok := initialState[p]; ok {
					changes = append(changes, p)
					delete(initialState, p)
				}
			}
		}

		if len(changes) > 0 {
			break
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"changes": changes,
	})
}

// --- Additional file operations: Move, Copy, Delete, Stat ---

// FileMoveRequest is the request for moving/renaming a file.
type FileMoveRequest struct {
	SrcPath   string `json:"srcPath"`
	DstPath   string `json:"dstPath"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

// FileCopyRequest is the request for copying a file.
type FileCopyRequest struct {
	SrcPath   string `json:"srcPath"`
	DstPath   string `json:"dstPath"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

// FileDeleteRequest is the request for deleting a file or directory.
type FileDeleteRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"`
}

// FileStatRequest is the request for getting file metadata.
type FileStatRequest struct {
	Path string `json:"path"`
}

// FileStatResponse contains detailed file metadata.
type FileStatResponse struct {
	Path          string    `json:"path"`
	Name          string    `json:"name"`
	IsDir         bool      `json:"isDir"`
	Size          int64     `json:"size"`
	Mode          string    `json:"mode"`
	ModTime       time.Time `json:"modTime"`
	IsSymlink     bool      `json:"isSymlink"`
	SymlinkTarget string    `json:"symlinkTarget,omitempty"`
}

// Move handles file move/rename requests.
func (f *FileService) Move(w http.ResponseWriter, r *http.Request) {
	var req FileMoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.SrcPath == "" || req.DstPath == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "srcPath and dstPath are required")
		return
	}

	srcFull, err := f.resolvePathStrict(req.SrcPath)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, ErrCodePathTraversal, err.Error())
		return
	}
	dstFull, err := f.resolvePathStrict(req.DstPath)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, ErrCodePathTraversal, err.Error())
		return
	}

	if _, err := os.Stat(srcFull); err != nil {
		if os.IsNotExist(err) {
			writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "source file not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}

	if !req.Overwrite {
		if _, err := os.Stat(dstFull); err == nil {
			writeAPIError(w, http.StatusConflict, ErrCodeConflict, "destination already exists")
			return
		}
	}

	if err := os.Rename(srcFull, dstFull); err != nil {
		// Cross-device rename fallback: copy + delete
		if strings.Contains(err.Error(), "invalid cross-device link") ||
			strings.Contains(err.Error(), "cross device") {
			if err := copyPath(srcFull, dstFull); err != nil {
				writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("cross-device copy failed: %v", err))
				return
			}
			os.RemoveAll(srcFull)
		} else {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("move failed: %v", err))
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"srcPath": req.SrcPath,
		"dstPath": req.DstPath,
		"status":  "moved",
	})
}

// Copy handles file copy requests.
func (f *FileService) Copy(w http.ResponseWriter, r *http.Request) {
	var req FileCopyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.SrcPath == "" || req.DstPath == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "srcPath and dstPath are required")
		return
	}

	srcFull, err := f.resolvePathStrict(req.SrcPath)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, ErrCodePathTraversal, err.Error())
		return
	}
	dstFull, err := f.resolvePathStrict(req.DstPath)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, ErrCodePathTraversal, err.Error())
		return
	}

	if _, err := os.Stat(srcFull); err != nil {
		if os.IsNotExist(err) {
			writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "source file not found")
			return
		}
	}

	if !req.Overwrite {
		if _, err := os.Stat(dstFull); err == nil {
			writeAPIError(w, http.StatusConflict, ErrCodeConflict, "destination already exists")
			return
		}
	}

	if err := copyPath(srcFull, dstFull); err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("copy failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"srcPath": req.SrcPath,
		"dstPath": req.DstPath,
		"status":  "copied",
	})
}

// Delete handles file/directory deletion requests.
func (f *FileService) Delete(w http.ResponseWriter, r *http.Request) {
	var req FileDeleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.Path == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "path is required")
		return
	}

	fullPath, err := f.resolvePathStrict(req.Path)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, ErrCodePathTraversal, err.Error())
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "file not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}

	if info.IsDir() && !req.Recursive {
		// Check if directory is empty
		entries, err := os.ReadDir(fullPath)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
			return
		}
		if len(entries) > 0 {
			writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "directory is not empty (use recursive=true)")
			return
		}
	}

	if err := os.RemoveAll(fullPath); err != nil {
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, fmt.Sprintf("delete failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":   req.Path,
		"status": "deleted",
	})
}

// Stat handles file metadata requests.
func (f *FileService) Stat(w http.ResponseWriter, r *http.Request) {
	var req FileStatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body")
		return
	}
	if req.Path == "" {
		writeAPIError(w, http.StatusBadRequest, ErrCodeBadRequest, "path is required")
		return
	}

	fullPath, err := f.resolvePathStrict(req.Path)
	if err != nil {
		writeAPIError(w, http.StatusForbidden, ErrCodePathTraversal, err.Error())
		return
	}

	info, err := os.Lstat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeAPIError(w, http.StatusNotFound, ErrCodeNotFound, "file not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, ErrCodeInternal, err.Error())
		return
	}

	resp := FileStatResponse{
		Path:      req.Path,
		Name:      info.Name(),
		IsDir:     info.IsDir(),
		Size:      info.Size(),
		Mode:      info.Mode().String(),
		ModTime:   info.ModTime(),
		IsSymlink: info.Mode()&os.ModeSymlink != 0,
	}

	if resp.IsSymlink {
		target, err := os.Readlink(fullPath)
		if err == nil {
			resp.SymlinkTarget = target
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// copyPath copies a file or directory from src to dst.
func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst, info.Mode())
}

// copyFile copies a single file.
func copyFile(src, dst string, mode os.FileMode) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// copyDir copies a directory recursively.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if err := copyFile(srcPath, dstPath, info.Mode()); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolvePath resolves a path relative to the workspace with path traversal protection.
func (f *FileService) resolvePath(path string) string {
	resolved, err := f.pathGuard.Resolve(path)
	if err != nil {
		// Return the workspace root on error for safety
		return f.workspace
	}
	return resolved
}

// resolvePathStrict resolves a path and returns an error on path traversal.
func (f *FileService) resolvePathStrict(path string) (string, error) {
	return f.pathGuard.Resolve(path)
}
