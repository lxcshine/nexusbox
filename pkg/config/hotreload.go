// Package config provides hot-reloadable configuration watching for the
// NexusBox sandbox manager. The Watcher polls one or more files for mtime
// changes and notifies registered Reloaders, so sandbox configuration (pool
// sizes, timeouts, resource limits, ...) can be updated without restarting
// the process.
//
// A polling implementation is used intentionally: it is dependency-free and
// works identically on Windows, Linux and macOS. fsnotify-style inotify
// backends do not exist on Windows and would add a build constraint.
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// Reloader is implemented by any component that can apply a new
// configuration at runtime. The implementation must be safe to call
// concurrently with normal operation.
type Reloader interface {
	// Reload applies newConfig to the component. It must return an error
	// if the new config is rejected; in that case the old config stays in
	// effect.
	Reload(ctx context.Context, newConfig any) error
	// Name returns a human-readable identifier for logging.
	Name() string
}

// Watcher polls a set of files for modification and notifies Reloader
// instances when a watched file changes. The zero value is not usable;
// construct with NewWatcher.
type Watcher struct {
	mu        sync.RWMutex
	files     map[string]time.Time // path -> last seen mtime
	reloaders map[string]Reloader  // name -> reloader
	parse     ConfigParser
	interval  time.Duration
	stopCh    chan struct{}
	started   bool
	stopped   bool
	wg        sync.WaitGroup
}

// ConfigParser parses the raw bytes of the watched config file(s) into the
// concrete config object passed to Reloader.Reload. Callers supply a parser
// that knows their config struct (JSON, YAML, ...).
type ConfigParser func(raw []byte) (any, error)

// NewWatcher creates a Watcher that polls every interval and, on file
// change, parses the file with parse and calls each registered Reloader.
// A zero or negative interval defaults to 5s.
func NewWatcher(parse ConfigParser, interval time.Duration) *Watcher {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Watcher{
		files:     make(map[string]time.Time),
		reloaders: make(map[string]Reloader),
		parse:     parse,
		interval:  interval,
		stopCh:    make(chan struct{}),
	}
}

// AddFile registers a file to be watched. If the file exists, its current
// mtime is recorded so the first change (not the initial state) triggers a
// reload. If the file does not yet exist, it will be picked up when created.
func (w *Watcher) AddFile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if info, err := os.Stat(abs); err == nil {
		w.files[abs] = info.ModTime()
	} else {
		w.files[abs] = time.Time{}
	}
	return nil
}

// Register attaches a Reloader. Registering a reloader whose Name() is
// already registered replaces the previous one.
func (w *Watcher) Register(r Reloader) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reloaders[r.Name()] = r
}

// Reloaders returns the currently registered reloaders (snapshot).
func (w *Watcher) Reloaders() []Reloader {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]Reloader, 0, len(w.reloaders))
	for _, r := range w.reloaders {
		out = append(out, r)
	}
	return out
}

// Start begins polling in a background goroutine until Stop is called or
// ctx is cancelled. Start is idempotent: calling it twice is a no-op.
func (w *Watcher) Start(ctx context.Context) {
	w.mu.Lock()
	if w.stopped || w.started {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.mu.Unlock()

	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-ticker.C:
				w.tick(ctx)
			}
		}
	}()
}

// Stop halts the watcher and waits for the polling goroutine to exit.
func (w *Watcher) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	close(w.stopCh)
	w.mu.Unlock()
	w.wg.Wait()
}

// tick checks all watched files for mtime changes and, if any changed,
// re-parses the config and notifies all reloaders.
func (w *Watcher) tick(ctx context.Context) {
	w.mu.Lock()
	files := make([]string, 0, len(w.files))
	for p := range w.files {
		files = append(files, p)
	}
	w.mu.Unlock()

	changed := false
	for _, p := range files {
		info, err := os.Stat(p)
		if err != nil {
			continue // missing file is not an error; wait for creation
		}
		w.mu.Lock()
		prev := w.files[p]
		if prev.IsZero() || !info.ModTime().Equal(prev) {
			changed = true
		}
		w.files[p] = info.ModTime()
		w.mu.Unlock()
	}
	if !changed {
		return
	}

	// Read the primary (first) watched file as the config source. If
	// multiple files are watched, callers can extend this; for now we
	// treat the first registered file as authoritative.
	if len(files) == 0 {
		return
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		klog.Warningf("hotreload: failed to read %s: %v", files[0], err)
		return
	}
	cfg, err := w.parse(raw)
	if err != nil {
		klog.Warningf("hotreload: failed to parse %s: %v", files[0], err)
		return
	}

	w.mu.RLock()
	reloaders := make([]Reloader, 0, len(w.reloaders))
	for _, r := range w.reloaders {
		reloaders = append(reloaders, r)
	}
	w.mu.RUnlock()

	for _, r := range reloaders {
		if err := r.Reload(ctx, cfg); err != nil {
			klog.Warningf("hotreload: reloader %s rejected new config: %v", r.Name(), err)
		} else {
			klog.Infof("hotreload: reloader %s applied new config", r.Name())
		}
	}
}

// LoadJSON reads and JSON-unmarshals a config file into target. It is a
// convenience helper for callers that want to load the initial config with
// the same parsing logic used by the watcher.
func LoadJSON(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

// SaveJSON marshals config to JSON and writes it to path atomically (write to
// a temp file then rename), so the watcher sees a single mtime update.
func SaveJSON(path string, config any) error {
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".cfg-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	return nil
}
