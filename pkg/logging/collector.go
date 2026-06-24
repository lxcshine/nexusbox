package logging

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// LogCollector manages log collection for sandboxes.
type LogCollector struct {
	mu       sync.RWMutex
	baseDir  string
	writers  map[string]*logWriter
}

type logWriter struct {
	file   *os.File
	writer *bufio.Writer
	mu     sync.Mutex
}

// NewLogCollector creates a new log collector.
func NewLogCollector(baseDir string) *LogCollector {
	if baseDir == "" {
		baseDir = "/var/log/nexusbox/sandboxes"
	}
	return &LogCollector{
		baseDir: baseDir,
		writers: make(map[string]*logWriter),
	}
}

// Open opens a log stream for a sandbox.
func (lc *LogCollector) Open(sandboxID string) (io.WriteCloser, error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	if lw, ok := lc.writers[sandboxID]; ok {
		return &writeCloser{lw: lw}, nil
	}

	if err := os.MkdirAll(lc.baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	logPath := filepath.Join(lc.baseDir, sandboxID+".log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %w", logPath, err)
	}

	lw := &logWriter{
		file:   f,
		writer: bufio.NewWriter(f),
	}
	lc.writers[sandboxID] = lw

	klog.V(4).Infof("Opened log stream for sandbox %s at %s", sandboxID, logPath)
	return &writeCloser{lw: lw}, nil
}

// Close closes the log stream for a sandbox.
func (lc *LogCollector) Close(sandboxID string) error {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	lw, ok := lc.writers[sandboxID]
	if !ok {
		return nil
	}

	lw.mu.Lock()
	defer lw.mu.Unlock()
	lw.writer.Flush()
	err := lw.file.Close()
	delete(lc.writers, sandboxID)
	return err
}

// StreamLogs streams logs for a sandbox starting from the given offset.
func (lc *LogCollector) StreamLogs(ctx context.Context, sandboxID string, follow bool, tailLines int64, out io.Writer) error {
	logPath := filepath.Join(lc.baseDir, sandboxID+".log")

	f, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}
	defer f.Close()

	if tailLines > 0 {
		// Seek to the last N lines
		if err := seekLastNLines(f, tailLines, out); err != nil {
			klog.Warningf("Failed to tail %d lines: %v", tailLines, err)
		}
	} else {
		// Stream from beginning
		if _, err := io.Copy(out, f); err != nil {
			return err
		}
	}

	if follow {
		// Follow the log file (like tail -f)
		reader := bufio.NewReader(f)
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
				line, err := reader.ReadString('\n')
				if err != nil {
					if err == io.EOF {
						time.Sleep(100 * time.Millisecond)
						continue
					}
					return err
				}
				if _, err := out.Write([]byte(line)); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// GetLogSize returns the size of a sandbox's log file.
func (lc *LogCollector) GetLogSize(sandboxID string) (int64, error) {
	logPath := filepath.Join(lc.baseDir, sandboxID+".log")
	stat, err := os.Stat(logPath)
	if err != nil {
		return 0, err
	}
	return stat.Size(), nil
}

// RotateLogs rotates log files that exceed the maximum size.
func (lc *LogCollector) RotateLogs(maxSize int64) error {
	lc.mu.RLock()
	defer lc.mu.RUnlock()

	entries, err := os.ReadDir(lc.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Size() > maxSize {
			oldPath := filepath.Join(lc.baseDir, entry.Name())
			rotatedPath := oldPath + "." + time.Now().Format("20060102-150405")
			if err := os.Rename(oldPath, rotatedPath); err != nil {
				klog.Warningf("Failed to rotate log %s: %v", oldPath, err)
			} else {
				klog.V(4).Infof("Rotated log %s -> %s", oldPath, rotatedPath)
			}
		}
	}
	return nil
}

// writeCloser wraps a logWriter to implement io.WriteCloser.
type writeCloser struct {
	lw *logWriter
}

func (wc *writeCloser) Write(p []byte) (n int, err error) {
	wc.lw.mu.Lock()
	defer wc.lw.mu.Unlock()
	return wc.lw.writer.Write(p)
}

func (wc *writeCloser) Close() error {
	wc.lw.mu.Lock()
	defer wc.lw.mu.Unlock()
	return wc.lw.writer.Flush()
}

// seekLastNLines seeks to the last N lines of a file.
func seekLastNLines(f *os.File, n int64, out io.Writer) error {
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	size := stat.Size()

	var count int64
	var pos int64
	buf := make([]byte, 1)

	for pos = size - 1; pos >= 0; pos-- {
		if _, err := f.Seek(pos, io.SeekStart); err != nil {
			return err
		}
		if _, err := f.Read(buf); err != nil {
			return err
		}
		if buf[0] == '\n' {
			count++
			if count >= n {
				break
			}
		}
	}

	_, err = io.Copy(out, f)
	return err
}
