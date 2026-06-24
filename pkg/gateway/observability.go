/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"
)

// MetricsCollector tracks API request metrics for observability.
// It provides counters, latency histograms, and error rate tracking.
type MetricsCollector struct {
	mu sync.RWMutex

	// Counters
	totalRequests   atomic.Int64
	totalErrors     atomic.Int64
	totalLatencyUs atomic.Int64

	// Per-endpoint metrics
	endpoints map[string]*EndpointMetrics
}

// EndpointMetrics tracks metrics for a single endpoint.
type EndpointMetrics struct {
	mu         sync.RWMutex
	requests   int64
	errors     int64
	latencyUs  int64
	lastAccess time.Time
}

// NewMetricsCollector creates a new MetricsCollector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		endpoints: make(map[string]*EndpointMetrics),
	}
}

// Record records a request's metrics.
func (mc *MetricsCollector) Record(method, path string, statusCode int, latency time.Duration) {
	mc.totalRequests.Add(1)
	mc.totalLatencyUs.Add(latency.Microseconds())
	if statusCode >= 400 {
		mc.totalErrors.Add(1)
	}

	key := method + " " + path
	mc.mu.RLock()
	em, ok := mc.endpoints[key]
	mc.mu.RUnlock()

	if !ok {
		mc.mu.Lock()
		em, ok = mc.endpoints[key]
		if !ok {
			em = &EndpointMetrics{}
			mc.endpoints[key] = em
		}
		mc.mu.Unlock()
	}

	em.mu.Lock()
	em.requests++
	if statusCode >= 400 {
		em.errors++
	}
	em.latencyUs += latency.Microseconds()
	em.lastAccess = time.Now()
	em.mu.Unlock()
}

// GetMetrics returns a snapshot of current metrics.
func (mc *MetricsCollector) GetMetrics() map[string]interface{} {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	totalReq := mc.totalRequests.Load()
	totalErr := mc.totalErrors.Load()
	totalLat := mc.totalLatencyUs.Load()

	endpoints := make(map[string]interface{})
	for key, em := range mc.endpoints {
		em.mu.RLock()
		defer em.mu.RUnlock()
		var avgLatency float64
		if em.requests > 0 {
			avgLatency = float64(em.latencyUs) / float64(em.requests) / 1000.0 // to ms
		}
		endpoints[key] = map[string]interface{}{
			"requests":   em.requests,
			"errors":     em.errors,
			"avgLatencyMs": avgLatency,
			"lastAccess": em.lastAccess.Format(time.RFC3339),
		}
	}

	var avgLatency float64
	if totalReq > 0 {
		avgLatency = float64(totalLat) / float64(totalReq) / 1000.0
	}

	return map[string]interface{}{
		"totalRequests":   totalReq,
		"totalErrors":     totalErr,
		"errorRate":       float64(totalErr) / float64(totalReq) * 100,
		"avgLatencyMs":    avgLatency,
		"endpoints":       endpoints,
	}
}

// --- Request Logging Middleware ---

// requestLogMiddleware logs every request with method, path, status, and latency.
func requestLogMiddleware(next http.Handler, mc *MetricsCollector) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap the ResponseWriter to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(rw, r)

		latency := time.Since(start)
		statusCode := rw.statusCode

		// Log the request
		klog.V(2).Infof("%s %s %d %s %s",
			r.Method,
			r.URL.Path,
			statusCode,
			latency.Round(time.Millisecond),
			r.RemoteAddr,
		)

		// Record metrics
		if mc != nil {
			mc.Record(r.Method, r.URL.Path, statusCode, latency)
		}
	})
}

// responseWriter wraps http.ResponseWriter to capture the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}

// --- Audit Trail ---

// AuditEntry represents a single audit log entry.
type AuditEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	StatusCode int       `json:"statusCode"`
	Latency    string    `json:"latency"`
	RemoteAddr string    `json:"remoteAddr"`
	UserAgent  string    `json:"userAgent"`
	SandboxID  string    `json:"sandboxId,omitempty"`
	Action     string    `json:"action"`
	Resource   string    `json:"resource"`
	Success    bool      `json:"success"`
}

// AuditLogger records audit entries for security and compliance.
type AuditLogger struct {
	mu      sync.Mutex
	entries []AuditEntry
	maxSize int
}

// NewAuditLogger creates a new AuditLogger with a circular buffer.
func NewAuditLogger(maxSize int) *AuditLogger {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &AuditLogger{
		entries: make([]AuditEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

// Log records an audit entry.
func (al *AuditLogger) Log(entry AuditEntry) {
	al.mu.Lock()
	defer al.mu.Unlock()

	if len(al.entries) >= al.maxSize {
		// Remove oldest entry (circular buffer behavior)
		al.entries = al.entries[1:]
	}
	al.entries = append(al.entries, entry)
}

// GetEntries returns recent audit entries.
func (al *AuditLogger) GetEntries(limit int) []AuditEntry {
	al.mu.Lock()
	defer al.mu.Unlock()

	if limit <= 0 || limit > len(al.entries) {
		limit = len(al.entries)
	}

	start := len(al.entries) - limit
	result := make([]AuditEntry, limit)
	copy(result, al.entries[start:])
	return result
}

// classifyAction determines the action type from the request path.
func classifyAction(method, path string) (action, resource string) {
	path = strings.TrimPrefix(path, "/v1/")
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		resource = parts[0]
	}
	switch method {
	case http.MethodGet:
		action = "read"
	case http.MethodPost:
		action = "write"
	case http.MethodPut:
		action = "update"
	case http.MethodDelete:
		action = "delete"
	default:
		action = "other"
	}
	return
}

// formatLatency formats a duration for human-readable output.
func formatLatency(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dμs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
