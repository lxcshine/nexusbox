package logging

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// LogEntry represents a structured log entry for indexing.
type LogEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	SandboxID  string    `json:"sandbox_id"`
	Level      string    `json:"level"`
	Message    string    `json:"message"`
	Source     string    `json:"source,omitempty"`
	LineNumber int       `json:"line_number,omitempty"`
}

// LogIndex provides indexed search over sandbox logs.
type LogIndex struct {
	mu       sync.RWMutex
	baseDir  string
	entries  map[string][]*LogEntry // sandboxID -> entries
	maxAge   time.Duration
	maxEntries int
}

// NewLogIndex creates a new log index.
func NewLogIndex(baseDir string) *LogIndex {
	if baseDir == "" {
		baseDir = "/var/log/nexusbox/sandboxes"
	}
	return &LogIndex{
		baseDir:    baseDir,
		entries:    make(map[string][]*LogEntry),
		maxAge:     7 * 24 * time.Hour, // 7 days default retention.
		maxEntries: 10000,              // Max entries per sandbox.
	}
}

// SetRetention sets the log retention policy.
func (li *LogIndex) SetRetention(maxAge time.Duration, maxEntries int) {
	li.mu.Lock()
	defer li.mu.Unlock()
	li.maxAge = maxAge
	li.maxEntries = maxEntries
}

// Index indexes a log line for a sandbox.
func (li *LogIndex) Index(sandboxID, line string) {
	entry := parseLogLine(sandboxID, line)
	if entry == nil {
		return
	}

	li.mu.Lock()
	defer li.mu.Unlock()

	entries := li.entries[sandboxID]
	entries = append(entries, entry)

	// Enforce max entries.
	if len(entries) > li.maxEntries {
		entries = entries[len(entries)-li.maxEntries:]
	}
	li.entries[sandboxID] = entries
}

// Search searches logs for a sandbox matching the given query.
func (li *LogIndex) Search(sandboxID, query string, level string, limit int) []*LogEntry {
	li.mu.RLock()
	defer li.mu.RUnlock()

	entries := li.entries[sandboxID]
	if entries == nil {
		return nil
	}

	query = strings.ToLower(query)
	var results []*LogEntry
	for _, entry := range entries {
		// Filter by level.
		if level != "" && entry.Level != level {
			continue
		}
		// Filter by query (case-insensitive substring match).
		if query != "" && !strings.Contains(strings.ToLower(entry.Message), query) {
			continue
		}
		results = append(results, entry)
		if limit > 0 && len(results) >= limit {
			break
		}
	}
	return results
}

// GetEntries returns all indexed entries for a sandbox.
func (li *LogIndex) GetEntries(sandboxID string) []*LogEntry {
	li.mu.RLock()
	defer li.mu.RUnlock()
	return li.entries[sandboxID]
}

// FlushIndex saves the index to disk as JSON for crash recovery.
func (li *LogIndex) FlushIndex(sandboxID string) error {
	li.mu.RLock()
	defer li.mu.RUnlock()

	entries := li.entries[sandboxID]
	if entries == nil {
		return nil
	}

	indexPath := filepath.Join(li.baseDir, sandboxID+".index.json")
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}

	// Atomic write.
	tmpPath := indexPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, indexPath)
}

// LoadIndex loads the index from disk.
func (li *LogIndex) LoadIndex(sandboxID string) error {
	li.mu.Lock()
	defer li.mu.Unlock()

	indexPath := filepath.Join(li.baseDir, sandboxID+".index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var entries []*LogEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	li.entries[sandboxID] = entries
	klog.Infof("Loaded %d log entries for sandbox %s", len(entries), sandboxID)
	return nil
}

// CleanupOldEntries removes entries older than the retention period.
func (li *LogIndex) CleanupOldEntries() int {
	li.mu.Lock()
	defer li.mu.Unlock()

	cutoff := time.Now().Add(-li.maxAge)
	removed := 0

	for sandboxID, entries := range li.entries {
		var kept []*LogEntry
		for _, entry := range entries {
			if entry.Timestamp.After(cutoff) {
				kept = append(kept, entry)
			} else {
				removed++
			}
		}
		li.entries[sandboxID] = kept
	}

	if removed > 0 {
		klog.Infof("Cleaned up %d old log entries", removed)
	}
	return removed
}

// CleanupOldLogFiles removes log files older than the retention period.
func (li *LogIndex) CleanupOldLogFiles() int {
	entries, err := os.ReadDir(li.baseDir)
	if err != nil {
		return 0
	}

	cutoff := time.Now().Add(-li.maxAge)
	removed := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(li.baseDir, entry.Name())
			if err := os.Remove(path); err == nil {
				removed++
			}
		}
	}

	if removed > 0 {
		klog.Infof("Removed %d old log files", removed)
	}
	return removed
}

// parseLogLine parses a log line into a LogEntry.
// Supports common log formats: JSON, klog, syslog, plain text.
func parseLogLine(sandboxID, line string) *LogEntry {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	// Try JSON format first.
	var entry LogEntry
	if err := json.Unmarshal([]byte(line), &entry); err == nil {
		entry.SandboxID = sandboxID
		return &entry
	}

	// Try klog format: I0625 14:30:45.123456  12345 file.go:42] message
	if len(line) > 30 && (line[0] == 'I' || line[0] == 'W' || line[0] == 'E' || line[0] == 'F') {
		level := map[byte]string{
			'I': "INFO", 'W': "WARN", 'E': "ERROR", 'F': "FATAL",
		}[line[0]]
		if level != "" {
			// Parse timestamp.
			ts, err := time.Parse("0102 15:04:05.000000", line[1:25])
			if err == nil {
				ts = ts.AddDate(time.Now().Year(), 0, 0)
				return &LogEntry{
					Timestamp: ts,
					SandboxID: sandboxID,
					Level:     level,
					Message:   line,
				}
			}
		}
	}

	// Plain text: use current timestamp and INFO level.
	return &LogEntry{
		Timestamp: time.Now(),
		SandboxID: sandboxID,
		Level:     "INFO",
		Message:   line,
	}
}

// PersistedLogReader reads logs from a persisted file with seeking.
type PersistedLogReader struct {
	file      *os.File
	reader    *bufio.Reader
	sandboxID string
}

// NewPersistedLogReader creates a new reader for a sandbox's log file.
func NewPersistedLogReader(baseDir, sandboxID string) (*PersistedLogReader, error) {
	logPath := filepath.Join(baseDir, sandboxID+".log")
	f, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	return &PersistedLogReader{
		file:      f,
		reader:    bufio.NewReader(f),
		sandboxID: sandboxID,
	}, nil
}

// ReadLine reads the next log line.
func (r *PersistedLogReader) ReadLine() (string, error) {
	line, err := r.reader.ReadString('\n')
	if err != nil {
		return strings.TrimRight(line, "\n"), err
	}
	return strings.TrimRight(line, "\n"), nil
}

// Close closes the reader.
func (r *PersistedLogReader) Close() error {
	return r.file.Close()
}

// LogRetentionManager manages log retention policies.
type LogRetentionManager struct {
	collector *LogCollector
	index     *LogIndex
	stopCh    chan struct{}
}

// NewLogRetentionManager creates a new retention manager.
func NewLogRetentionManager(collector *LogCollector, index *LogIndex) *LogRetentionManager {
	return &LogRetentionManager{
		collector: collector,
		index:     index,
		stopCh:    make(chan struct{}),
	}
}

// Start starts the retention manager goroutine.
func (m *LogRetentionManager) Start() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Rotate large log files (100MB).
			m.collector.RotateLogs(100 * 1024 * 1024)
			// Clean up old entries.
			m.index.CleanupOldEntries()
			// Clean up old log files.
			m.index.CleanupOldLogFiles()
		case <-m.stopCh:
			return
		}
	}
}

// Stop stops the retention manager.
func (m *LogRetentionManager) Stop() {
	close(m.stopCh)
}
