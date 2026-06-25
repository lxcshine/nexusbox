package logging

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewLogIndex(t *testing.T) {
	tmpDir := t.TempDir()
	idx := NewLogIndex(tmpDir)
	if idx == nil {
		t.Fatal("NewLogIndex returned nil")
	}
}

func TestIndexAndSearch(t *testing.T) {
	tmpDir := t.TempDir()
	idx := NewLogIndex(tmpDir)

	// Index some log lines.
	idx.Index("sandbox-1", `{"timestamp":"2026-01-01T10:00:00Z","level":"INFO","message":"Starting process"}`)
	idx.Index("sandbox-1", `{"timestamp":"2026-01-01T10:01:00Z","level":"ERROR","message":"Connection failed"}`)
	idx.Index("sandbox-1", `{"timestamp":"2026-01-01T10:02:00Z","level":"INFO","message":"Retrying connection"}`)

	// Search all.
	results := idx.Search("sandbox-1", "", "", 10)
	if len(results) != 3 {
		t.Errorf("Search returned %d results, want 3", len(results))
	}

	// Search by level.
	errorResults := idx.Search("sandbox-1", "", "ERROR", 10)
	if len(errorResults) != 1 {
		t.Errorf("ERROR search returned %d results, want 1", len(errorResults))
	}

	// Search by query.
	connResults := idx.Search("sandbox-1", "connection", "", 10)
	if len(connResults) != 2 {
		t.Errorf("connection search returned %d results, want 2", len(connResults))
	}
}

func TestIndexPlain(t *testing.T) {
	tmpDir := t.TempDir()
	idx := NewLogIndex(tmpDir)

	// Index plain text.
	idx.Index("sandbox-1", "This is a plain log message")

	results := idx.Search("sandbox-1", "plain", "", 10)
	if len(results) != 1 {
		t.Errorf("Search returned %d results, want 1", len(results))
	}
}

func TestIndexKlogFormat(t *testing.T) {
	tmpDir := t.TempDir()
	idx := NewLogIndex(tmpDir)

	// Index klog format.
	idx.Index("sandbox-1", "I0625 14:30:45.123456  12345 file.go:42] Started server")

	results := idx.Search("sandbox-1", "", "INFO", 10)
	if len(results) != 1 {
		t.Errorf("INFO search returned %d results, want 1", len(results))
	}
}

func TestFlushAndLoadIndex(t *testing.T) {
	tmpDir := t.TempDir()
	idx := NewLogIndex(tmpDir)

	idx.Index("sandbox-1", `{"timestamp":"2026-01-01T10:00:00Z","level":"INFO","message":"Test entry"}`)

	// Flush to disk.
	if err := idx.FlushIndex("sandbox-1"); err != nil {
		t.Fatalf("FlushIndex failed: %v", err)
	}

	// Verify file exists.
	indexPath := filepath.Join(tmpDir, "sandbox-1.index.json")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("index file not created: %v", err)
	}

	// Create new index and load.
	idx2 := NewLogIndex(tmpDir)
	if err := idx2.LoadIndex("sandbox-1"); err != nil {
		t.Fatalf("LoadIndex failed: %v", err)
	}

	results := idx2.Search("sandbox-1", "", "", 10)
	if len(results) != 1 {
		t.Errorf("After load, Search returned %d results, want 1", len(results))
	}
}

func TestCleanupOldEntries(t *testing.T) {
	tmpDir := t.TempDir()
	idx := NewLogIndex(tmpDir)
	idx.SetRetention(1*time.Hour, 10000)

	// Add an old entry.
	idx.Index("sandbox-1", `{"timestamp":"2020-01-01T10:00:00Z","level":"INFO","message":"Old entry"}`)
	// Add a recent entry (using current time to ensure it's within retention).
	recentTS := time.Now().UTC().Format(time.RFC3339)
	idx.Index("sandbox-1", `{"timestamp":"`+recentTS+`","level":"INFO","message":"Recent entry"}`)

	removed := idx.CleanupOldEntries()
	if removed == 0 {
		t.Error("expected some entries to be removed")
	}

	results := idx.Search("sandbox-1", "", "", 10)
	if len(results) == 0 {
		t.Error("expected some entries to remain")
	}
}

func TestSetRetention(t *testing.T) {
	tmpDir := t.TempDir()
	idx := NewLogIndex(tmpDir)
	idx.SetRetention(24*time.Hour, 5000)

	if idx.maxAge != 24*time.Hour {
		t.Errorf("maxAge = %v, want 24h", idx.maxAge)
	}
	if idx.maxEntries != 5000 {
		t.Errorf("maxEntries = %d, want 5000", idx.maxEntries)
	}
}

func TestPersistedLogReader(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "sandbox-1.log")
	os.WriteFile(logPath, []byte("line1\nline2\nline3\n"), 0644)

	reader, err := NewPersistedLogReader(tmpDir, "sandbox-1")
	if err != nil {
		t.Fatalf("NewPersistedLogReader failed: %v", err)
	}
	defer reader.Close()

	line1, _ := reader.ReadLine()
	if line1 != "line1" {
		t.Errorf("ReadLine = %q, want %q", line1, "line1")
	}

	line2, _ := reader.ReadLine()
	if line2 != "line2" {
		t.Errorf("ReadLine = %q, want %q", line2, "line2")
	}
}

func TestLogRetentionManager(t *testing.T) {
	tmpDir := t.TempDir()
	collector := NewLogCollector(tmpDir)
	idx := NewLogIndex(tmpDir)

	manager := NewLogRetentionManager(collector, idx)
	go manager.Start()
	defer manager.Stop()

	// Give it a moment.
	time.Sleep(100 * time.Millisecond)
}
