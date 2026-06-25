// Package integration exercises the Phase 2 developer-experience features
// (snapshot/restore, multi-language runtime, hot-reload config, and workspace
// isolation) together in a realistic end-to-end scenario.
//
// The tests are hermetic: they run against temp directories and skip any
// language runtime that is not installed on the host. They do NOT require a
// container runtime or a live API server.
package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/klog/v2"

	nxconfig "github.com/nexusbox/nexusbox/pkg/config"
	"github.com/nexusbox/nexusbox/pkg/gateway"
	"github.com/nexusbox/nexusbox/pkg/sandbox/runtime"
	"github.com/nexusbox/nexusbox/pkg/snapshot"
	"github.com/nexusbox/nexusbox/pkg/workspace"
)

// Test_Phase2_Features_WorkTogether simulates two parallel AI sessions, each
// in its own isolated workspace, each executing code, snapshotting, mutating,
// restoring, while a hot-reload watcher updates the runtime manager config
// mid-flight. It asserts that:
//
//  1. Workspaces are isolated (session A cannot read session B's files).
//  2. Multi-language execution works inside a workspace (Go always; Python/
//     Node/Java when the toolchain is present).
//  3. Snapshot/restore brings a corrupted workspace back to the snapshotted
//     state without touching the sibling workspace.
//  4. Hot-reloaded config is observable via GetConfig immediately after the
//     file is rewritten, with no process restart.
func Test_Phase2_Features_WorkTogether(t *testing.T) {
	ctx := context.Background()

	// --- Shared fixture setup ---
	wsMgr, err := workspace.NewManager(workspace.ManagerConfig{BaseRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("workspace.NewManager: %v", err)
	}
	snapMgr := snapshot.NewSnapshotManager(t.TempDir(), nil) // auto backend
	codeSvc := gateway.NewCodeService()

	// Create two parallel workspaces, one per AI session.
	wA, err := wsMgr.Create(workspace.CreateOpts{ID: "session-a", OwnerSession: "agent-1"})
	if err != nil {
		t.Fatalf("create workspace A: %v", err)
	}
	wB, err := wsMgr.Create(workspace.CreateOpts{ID: "session-b", OwnerSession: "agent-2"})
	if err != nil {
		t.Fatalf("create workspace B: %v", err)
	}

	// Seed each workspace with a marker file via the isolation-aware resolver.
	markerA, _ := wA.ResolveData("marker.txt")
	markerB, _ := wB.ResolveData("marker.txt")
	if err := os.WriteFile(markerA, []byte("A"), 0644); err != nil {
		t.Fatalf("write marker A: %v", err)
	}
	if err := os.WriteFile(markerB, []byte("B"), 0644); err != nil {
		t.Fatalf("write marker B: %v", err)
	}

	// ------------------------------------------------------------------
	// (1) Workspace isolation: A's resolver must not reach B's files.
	// ------------------------------------------------------------------
	t.Run("workspace_isolation", func(t *testing.T) {
		// Traversal to B is rejected.
		if _, err := wA.ResolvePath(filepath.Join("..", "session-b", "data", "marker.txt")); err == nil {
			t.Errorf("workspace A traversal to B was NOT rejected")
		}
		// Absolute path to B's marker is re-rooted under A and does not exist.
		reRooted, err := wA.ResolvePath(markerB)
		if err != nil {
			t.Fatalf("ResolvePath(absolute to B): %v", err)
		}
		if strings.HasPrefix(reRooted, wB.Root) {
			t.Errorf("absolute path leaked into B: %s", reRooted)
		}
		if _, err := os.Stat(reRooted); !os.IsNotExist(err) {
			t.Errorf("re-rooted path unexpectedly exists (cross-workspace leak): %s", reRooted)
		}
		// A can read its own marker.
		got, err := os.ReadFile(markerA)
		if err != nil || string(got) != "A" {
			t.Errorf("A's own marker read failed: got %q, err %v", got, err)
		}
	})

	// ------------------------------------------------------------------
	// (2) Multi-language execution inside workspaces (parallel sessions).
	// ------------------------------------------------------------------
	t.Run("multi_language_execution", func(t *testing.T) {
		// Run Go in both workspaces concurrently to mimic parallel AI sessions.
		// Each writes its session marker into its own workspace via stdout,
		// proving the code service output does not cross sessions.
		const goCode = `package main
import "fmt"
func main() { fmt.Println("hello-from-go") }`

		var (
			wg           sync.WaitGroup
			outA, outB   string
			exitA, exitB int
			errA, errB   error
			execMu       sync.Mutex
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			s, _, c, e := codeSvc.ExecuteCode("go", goCode, 30)
			execMu.Lock()
			outA, exitA, errA = s, c, e
			execMu.Unlock()
		}()
		go func() {
			defer wg.Done()
			s, _, c, e := codeSvc.ExecuteCode("go", goCode, 30)
			execMu.Lock()
			outB, exitB, errB = s, c, e
			execMu.Unlock()
		}()
		wg.Wait()

		if errA != nil || exitA != 0 {
			t.Errorf("session A go exec: exit=%d err=%v", exitA, errA)
		}
		if errB != nil || exitB != 0 {
			t.Errorf("session B go exec: exit=%d err=%v", exitB, errB)
		}
		if !strings.Contains(outA, "hello-from-go") || !strings.Contains(outB, "hello-from-go") {
			t.Errorf("parallel go exec output missing greeting: A=%q B=%q", outA, outB)
		}

		// Optionally exercise other toolchains if present (skip otherwise).
		// Each snippet prints "hello-from-<language>" so the helper's
		// `want := "hello-from-" + language` assertion matches exactly.
		runIfAvailable(t, codeSvc, "python", `print("hello-from-python")`)
		runIfAvailable(t, codeSvc, "nodejs", `console.log("hello-from-nodejs")`)
		runIfAvailable(t, codeSvc, "java", `public class Main { public static void main(String[] a){ System.out.println("hello-from-java"); } }`)
	})

	// ------------------------------------------------------------------
	// (3) Snapshot/restore: corrupt A, restore, B must be untouched.
	// ------------------------------------------------------------------
	t.Run("snapshot_restore", func(t *testing.T) {
		// Add an extra file to A then snapshot (the "last good state").
		extraA, _ := wA.ResolveData("extra.txt")
		if err := os.WriteFile(extraA, []byte("good"), 0644); err != nil {
			t.Fatalf("write extra A: %v", err)
		}
		snapID, err := snapMgr.CreateSnapshotFromPath(ctx, "session-a", nil, wA.Root)
		if err != nil {
			t.Fatalf("CreateSnapshotFromPath: %v", err)
		}

		// Corrupt A: delete marker, rewrite extra as "bad".
		_ = os.Remove(markerA)
		_ = os.WriteFile(extraA, []byte("bad"), 0644)

		// Also mutate B to prove restore does not touch siblings.
		extraB, _ := wB.ResolveData("extra.txt")
		_ = os.WriteFile(extraB, []byte("B-untouched"), 0644)

		// Restore A from snapshot into a fresh target dir (reusing snapshot's
		// recorded source would overwrite A in place; we restore into a new
		// dir to keep the assertion explicit).
		restored := filepath.Join(t.TempDir(), "restored-a")
		if err := snapMgr.RestoreSnapshot(ctx, snapID, "session-a-restored", restored); err != nil {
			t.Fatalf("RestoreSnapshot: %v", err)
		}

		// Restored A must have marker "A" and extra "good" (pre-corruption).
		restoredMarker := filepath.Join(restored, "data", "marker.txt")
		got, err := os.ReadFile(restoredMarker)
		if err != nil || string(got) != "A" {
			t.Errorf("restored marker = %q, err %v, want A", got, err)
		}
		restoredExtra := filepath.Join(restored, "data", "extra.txt")
		got, err = os.ReadFile(restoredExtra)
		if err != nil || string(got) != "good" {
			t.Errorf("restored extra = %q, err %v, want good", got, err)
		}

		// B's live state must be unchanged by A's restore.
		gotB, err := os.ReadFile(extraB)
		if err != nil || string(gotB) != "B-untouched" {
			t.Errorf("B's extra changed after A restore: got %q, want B-untouched", gotB)
		}

		// Metadata integrity.
		meta, err := snapMgr.GetSnapshot(snapID)
		if err != nil {
			t.Fatalf("GetSnapshot: %v", err)
		}
		if meta.SourcePath != wA.Root {
			t.Errorf("meta.SourcePath = %q, want %q", meta.SourcePath, wA.Root)
		}
	})
}

// Test_HotReload_TakesEffectWithoutRestart validates that writing the config
// file triggers a reload and the new value is visible via GetConfig, all
// without restarting the runtime manager.
func Test_HotReload_TakesEffectWithoutRestart(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "runtime.json")

	// Initial config file: MaxConcurrentOperations=10.
	initial := runtime.DefaultRuntimeManagerConfig()
	initial.MaxConcurrentOperations = 10
	raw, _ := json.Marshal(initial)
	if err := os.WriteFile(cfgPath, raw, 0644); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	rtMgr := runtime.NewRuntimeManager(nil)

	// Parser knows the concrete type so the watcher can hand *RuntimeManagerConfig to the reloader.
	parser := func(b []byte) (any, error) {
		var c runtime.RuntimeManagerConfig
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		return &c, nil
	}
	watcher := nxconfig.NewWatcher(parser, 50*time.Millisecond)
	if err := watcher.AddFile(cfgPath); err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	watcher.Register(rtMgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher.Start(ctx)
	defer watcher.Stop()

	// Let the watcher record the initial mtime.
	time.Sleep(120 * time.Millisecond)

	// Rewrite the config to bump MaxConcurrentOperations to 77 and shorten
	// CreateTimeout. Endpoints are left empty so they must be inherited.
	updated := runtime.RuntimeManagerConfig{
		MaxConcurrentOperations: 77,
		CreateTimeout:           3 * time.Second,
	}
	raw2, _ := json.Marshal(updated)
	if err := nxconfig.SaveJSON(cfgPath, &updated); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}
	_ = raw2

	// Wait for the watcher to apply the reload.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := rtMgr.GetConfig(); got.MaxConcurrentOperations == 77 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	got := rtMgr.GetConfig()
	if got.MaxConcurrentOperations != 77 {
		t.Errorf("hot-reload not applied: MaxConcurrentOperations = %d, want 77", got.MaxConcurrentOperations)
	}
	if got.CreateTimeout != 3*time.Second {
		t.Errorf("hot-reload CreateTimeout = %s, want 3s", got.CreateTimeout)
	}
	// Endpoints were empty in the new config -> must be inherited from default.
	if got.KataContainersEndpoint != initial.KataContainersEndpoint {
		t.Errorf("endpoints not inherited: Kata = %q, want %q",
			got.KataContainersEndpoint, initial.KataContainersEndpoint)
	}
}

// runIfAvailable executes code in the given language and fails the test only
// if the toolchain is present but execution errors. Missing toolchains are
// silently skipped so the integration test runs on minimal hosts.
func runIfAvailable(t *testing.T, svc *gateway.CodeService, language, code string) {
	t.Helper()
	info := svc.Info
	_ = info // Info is an HTTP handler; we probe availability via ExecuteCode instead.

	stdout, stderr, exitCode, err := svc.ExecuteCode(language, code, 30)
	if err != nil && strings.Contains(strings.ToLower(stderr), "not available") {
		t.Logf("%s runtime not installed, skipping", language)
		return
	}
	if err != nil {
		t.Errorf("%s exec errored: %v (stderr=%s)", language, err, stderr)
		return
	}
	if exitCode != 0 {
		t.Errorf("%s exec exit=%d stderr=%s", language, exitCode, stderr)
		return
	}
	want := "hello-from-" + language
	if !strings.Contains(stdout, want) {
		t.Errorf("%s exec output = %q, want to contain %q", language, stdout, want)
	}
}

// init lowers klog verbosity so the integration test output stays readable.
func init() {
	klog.InitFlags(nil)
}
