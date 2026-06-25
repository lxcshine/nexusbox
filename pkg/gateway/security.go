package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// --- Standardized Error Codes ---

const (
	ErrCodeBadRequest       = "BAD_REQUEST"
	ErrCodeUnauthorized     = "UNAUTHORIZED"
	ErrCodeForbidden        = "FORBIDDEN"
	ErrCodeNotFound         = "NOT_FOUND"
	ErrCodeConflict         = "CONFLICT"
	ErrCodePayloadTooLarge  = "PAYLOAD_TOO_LARGE"
	ErrCodeRateLimited      = "RATE_LIMITED"
	ErrCodeInternal         = "INTERNAL_ERROR"
	ErrCodeTimeout          = "TIMEOUT"
	ErrCodePathTraversal    = "PATH_TRAVERSAL_DENIED"
	ErrCodeResourceExhausted = "RESOURCE_EXHAUSTED"
	ErrCodeUnsupported     = "UNSUPPORTED"
)

// APIError is a structured error response with code, message, and optional details.
type APIError struct {
	Code      string      `json:"code"`
	Message   string      `json:"message"`
	Details   interface{} `json:"details,omitempty"`
	RequestID string      `json:"requestId,omitempty"`
	Timestamp string      `json:"timestamp"`
}

// writeAPIError writes a structured API error response.
func writeAPIError(w http.ResponseWriter, statusCode int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(APIError{
		Code:      code,
		Message:   message,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// writeAPIErrorWithDetails writes a structured API error response with details.
func writeAPIErrorWithDetails(w http.ResponseWriter, statusCode int, code, message string, details interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(APIError{
		Code:      code,
		Message:   message,
		Details:   details,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

// --- Path Security ---

// PathGuard enforces workspace sandboxing and prevents path traversal attacks.
// All file operations must go through PathGuard to ensure they stay within
// the allowed workspace directory.
type PathGuard struct {
	workspace string // absolute, cleaned workspace root
}

// NewPathGuard creates a PathGuard rooted at the given workspace.
func NewPathGuard(workspace string) *PathGuard {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = workspace
	}
	return &PathGuard{workspace: filepath.Clean(abs)}
}

// Resolve resolves a user-supplied path to an absolute filesystem path,
// ensuring the result stays within the workspace.
// Returns an error if the path escapes the workspace (path traversal attack).
func (pg *PathGuard) Resolve(userPath string) (string, error) {
	// Allow absolute paths only if they're within the workspace
	var candidate string
	if filepath.IsAbs(userPath) {
		candidate = filepath.Clean(userPath)
	} else {
		candidate = filepath.Clean(filepath.Join(pg.workspace, userPath))
	}

	// Check for path traversal: the resolved path must be within workspace
	rel, err := filepath.Rel(pg.workspace, candidate)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}

	// If the relative path starts with "..", it escapes the workspace
	if strings.HasPrefix(rel, "..") {
		return "", &PathTraversalError{
			Requested: userPath,
			Workspace: pg.workspace,
		}
	}

	return candidate, nil
}

// ResolveAllowAbsolute resolves a path, allowing absolute paths outside the workspace
// (for system-level operations). Used sparingly with explicit caller awareness.
func (pg *PathGuard) ResolveAllowAbsolute(userPath string) (string, error) {
	if filepath.IsAbs(userPath) {
		return filepath.Clean(userPath), nil
	}
	return pg.Resolve(userPath)
}

// Workspace returns the workspace root.
func (pg *PathGuard) Workspace() string {
	return pg.workspace
}

// PathTraversalError is returned when a path traversal attempt is detected.
type PathTraversalError struct {
	Requested string
	Workspace string
}

func (e *PathTraversalError) Error() string {
	return fmt.Sprintf("path traversal denied: %q escapes workspace %q", e.Requested, e.Workspace)
}

// --- Request Size Limiting ---

// MaxRequestBodySize limits request body to prevent memory exhaustion (10MB default).
const MaxRequestBodySize = 10 * 1024 * 1024

// limitedReader wraps http.Request.Body with a size limit.
func limitedReader(r *http.Request) *json.Decoder {
	r.Body = http.MaxBytesReader(nil, r.Body, MaxRequestBodySize)
	return json.NewDecoder(r.Body)
}
