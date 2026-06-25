package config

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type fakeConfig struct {
	Value string `json:"value"`
}

// fakeReloader counts Reload calls and stores the last applied value.
type fakeReloader struct {
	name   string
	calls  int32
	last   fakeConfig
	failOn string // if the new Value == this, Reload returns errFake
}

func (f *fakeReloader) Name() string { return f.name }

func (f *fakeReloader) Reload(ctx context.Context, newConfig any) error {
	cfg, ok := newConfig.(*fakeConfig)
	if !ok {
		return errors.New("config: unexpected type")
	}
	if cfg.Value == f.failOn {
		return errors.New("fake reload error")
	}
	atomic.AddInt32(&f.calls, 1)
	f.last = *cfg
	return nil
}

func TestWatcher_ReloadsOnFileChange(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")

	initial := fakeConfig{Value: "v1"}
	if err := SaveJSON(cfgPath, &initial); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	parser := func(raw []byte) (any, error) {
		var c fakeConfig
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		return &c, nil
	}

	w := NewWatcher(parser, 50*time.Millisecond)
	if err := w.AddFile(cfgPath); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	r := &fakeReloader{name: "fake"}
	w.Register(r)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	defer w.Stop()

	// Give the watcher one tick to register the initial mtime.
	time.Sleep(120 * time.Millisecond)

	// Mutate the config file with a small delay so the mtime changes.
	updated := fakeConfig{Value: "v2"}
	if err := SaveJSON(cfgPath, &updated); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	// Wait for at least a couple of poll intervals for the change to be seen.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&r.calls) >= 1 && r.last.Value == "v2" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if r.last.Value != "v2" {
		t.Errorf("reloader did not see new value: got %q, want v2 (calls=%d)", r.last.Value, r.calls)
	}
}

func TestWatcher_DoesNotReloadUnchangedFile(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	if err := SaveJSON(cfgPath, &fakeConfig{Value: "x"}); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	parser := func(raw []byte) (any, error) { return &fakeConfig{Value: "x"}, nil }
	w := NewWatcher(parser, 50*time.Millisecond)
	_ = w.AddFile(cfgPath)
	r := &fakeReloader{name: "fake"}
	w.Register(r)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	defer w.Stop()

	time.Sleep(250 * time.Millisecond)
	if got := atomic.LoadInt32(&r.calls); got != 0 {
		t.Errorf("reloader called %d times on unchanged file, want 0", got)
	}
}

func TestWatcher_ReloadErrorKeepsOldConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.json")
	_ = SaveJSON(cfgPath, &fakeConfig{Value: "good"})

	parser := func(raw []byte) (any, error) {
		var c fakeConfig
		_ = json.Unmarshal(raw, &c)
		return &c, nil
	}
	w := NewWatcher(parser, 50*time.Millisecond)
	_ = w.AddFile(cfgPath)
	r := &fakeReloader{name: "fake", failOn: "bad"}
	w.Register(r)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	defer w.Stop()

	time.Sleep(120 * time.Millisecond)

	// Write a config that the reloader rejects.
	_ = SaveJSON(cfgPath, &fakeConfig{Value: "bad"})
	time.Sleep(250 * time.Millisecond)

	if r.last.Value == "bad" {
		t.Errorf("reloader applied rejected config")
	}
}

func TestLoadAndSaveJSON_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "c.json")

	want := fakeConfig{Value: "round-trip"}
	if err := SaveJSON(cfgPath, &want); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	var got fakeConfig
	if err := LoadJSON(cfgPath, &got); err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if got.Value != want.Value {
		t.Errorf("round-trip: got %q, want %q", got.Value, want.Value)
	}
}

func TestNewWatcher_DefaultInterval(t *testing.T) {
	w := NewWatcher(nil, 0)
	if w.interval <= 0 {
		t.Errorf("default interval = %v, want > 0", w.interval)
	}
}

func TestWatcher_AddFile_NonExistent(t *testing.T) {
	w := NewWatcher(func(b []byte) (any, error) { return nil, nil }, time.Second)
	missing := filepath.Join(t.TempDir(), "nope.json")
	if err := w.AddFile(missing); err != nil {
		t.Errorf("AddFile on missing path returned error: %v", err)
	}
}

func TestWatcher_StopIsIdempotent(t *testing.T) {
	w := NewWatcher(func(b []byte) (any, error) { return nil, nil }, time.Second)
	w.Stop() // should not panic
	w.Stop()
}

func TestWatcher_StartIsIdempotent(t *testing.T) {
	w := NewWatcher(func(b []byte) (any, error) { return nil, nil }, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.Start(ctx)
	w.Start(ctx) // second Start must be a no-op, not a double goroutine
	w.Stop()
}

// Ensure os is referenced so unused-import checks stay happy even if a future
// edit removes the only os usage.
var _ = os.Stat
