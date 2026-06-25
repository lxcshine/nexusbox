# NexusBox Phase 2 Integration Test Guide

> How to run and extend the end-to-end integration test that verifies the four Phase 2 developer-experience features work together in a realistic scenario.

This guide explains the test located at [`test/integration/phase2_integration_test.go`](../test/integration/phase2_integration_test.go). The test is **hermetic** — it runs against temporary directories and does not require a container runtime or a live API server.

---

## What the Test Verifies

The Phase 2 release introduced four features that must cooperate correctly:

| # | Feature | Package | Value |
|---|---------|---------|-------|
| 1 | **Snapshot / Restore (VSS)** | `pkg/snapshot` | "Return to the last good state" |
| 2 | **Multi-language runtime** (Python / Node / Go / Java) | `pkg/gateway` | "Cover mainstream AI programming scenarios" |
| 3 | **Hot-reload configuration** | `pkg/config` | "Update sandbox config without restart" |
| 4 | **Workspace isolation** (parallel projects) | `pkg/workspace` | "Multiple AI sessions do not conflict" |

The integration test exercises all four together to prove they do not interfere with one another in real-world conditions.

---

## Test Scenarios

The file contains two top-level test functions:

### 1. `Test_Phase2_Features_WorkTogether`

Simulates **two parallel AI sessions** (session-a / session-b), each in its own isolated workspace, and runs three subtests:

| Subtest | Scenario | Assertion |
|---------|----------|-----------|
| `workspace_isolation` | Session A tries to reach session B's files via `..` traversal and via an absolute path | Traversal is rejected; absolute path is re-rooted under A and does not exist (no cross-workspace leak) |
| `multi_language_execution` | Both sessions concurrently execute Go code; Python / Node / Java run if the toolchain is present | Concurrent Go output does not cross sessions; optional languages print `hello-from-<language>` |
| `snapshot_restore` | Snapshot A → delete A's marker, corrupt A's `extra.txt` → restore into a fresh dir; meanwhile mutate B | Restored A has `marker=A` and `extra=good`; B's `extra` is unchanged |

### 2. `Test_HotReload_TakesEffectWithoutRestart`

Validates the hot-reload watcher end-to-end:

1. Writes an initial `runtime.json` with `MaxConcurrentOperations=10`.
2. Starts a `config.Watcher` polling every 50 ms and registers the `RuntimeManager` as a reloader.
3. Rewrites the file to set `MaxConcurrentOperations=77` and `CreateTimeout=3s`, leaving endpoints empty.
4. Polls `RuntimeManager.GetConfig()` until the new values appear (no process restart).

Assertion: the new config is visible within seconds; empty endpoint fields are safely inherited from the default config.

---

## Prerequisites

- **Go 1.22+** (required to build and run the test)
- **Go toolchain on `PATH`** (always required — Go is the baseline language for the multi-language subtest)
- **Optional** toolchains, auto-detected and skipped if absent:
  - `python3` / `python`
  - `node` (Node.js)
  - `javac` / `java`
- **Windows** is recommended to exercise the VSS backend; on non-Windows hosts the snapshot manager auto-selects the filesystem backend.
- For the VSS backend on Windows, **administrator privileges** are required. Without them, VSS fails and the manager **gracefully falls back** to the filesystem backend — the test still passes.

Verify the toolchain:

```bash
go version          # go1.22+
go vet ./...        # static checks
```

---

## Running the Test

### Run only the integration test

From the repository root:

```bash
go test ./test/integration/... -v -count=1 -timeout 180s
```

Expected output (abbreviated):

```
=== RUN   Test_Phase2_Features_WorkTogether
=== RUN   Test_Phase2_Features_WorkTogether/workspace_isolation
=== RUN   Test_Phase2_Features_WorkTogether/multi_language_execution
=== RUN   Test_Phase2_Features_WorkTogether/snapshot_restore
--- PASS: Test_Phase2_Features_WorkTogether (2.48s)
    --- PASS: Test_Phase2_Features_WorkTogether/workspace_isolation (0.00s)
    --- PASS: Test_Phase2_Features_WorkTogether/multi_language_execution (2.20s)
    --- PASS: Test_Phase2_Features_WorkTogether/snapshot_restore (0.15s)
=== RUN   Test_HotReload_TakesEffectWithoutRestart
--- PASS: Test_HotReload_TakesEffectWithoutRestart (0.28s)
PASS
ok      github.com/nexusbox/nexusbox/test/integration   3.404s
```

### Run with the race detector

```bash
go test ./test/integration/... -race -count=1 -timeout 300s
```

### Run together with the related package unit tests

```bash
go test ./pkg/snapshot/... ./pkg/config/... ./pkg/workspace/... \
         ./pkg/sandbox/runtime/... ./pkg/gateway/... \
         ./test/integration/... \
         -count=1 -timeout 300s
```

### Run a single subtest

```bash
go test ./test/integration/... -run 'Test_Phase2_Features_WorkTogether/snapshot_restore' -v
```

---

## Interpreting the Output

### Normal log lines

- `workspace: created session-a at <tmp>\session-a (owner=agent-1)` — two parallel workspaces were created.
- `Creating snapshot snap-session-a-<ts> for sandbox session-a via vss backend` — snapshot creation started.
- `Created snapshot ... (size: 5 bytes, backend: filesystem)` — the snapshot succeeded.
- `Runtime manager config hot-reloaded: ... maxConcurrentOps=77 createTimeout=3s` — hot reload applied.
- `hotreload: reloader runtime-manager applied new config` — the reloader confirmed the new config.

### Warning: VSS fallback

If you see the following warning, it is **expected on non-admin Windows or on Linux/macOS**, and the test still passes:

```
W vss_windows.go:78] VSS: create shadow failed for volume C:: ...
; falling back to filesystem backend
```

This warning proves the backend abstraction works: when VSS is unavailable, the snapshot manager transparently switches to the cross-platform filesystem backend.

### Failure indicators

The test fails if:

- A workspace resolver allows a path to escape into a sibling workspace (`workspace_isolation` subtest).
- Concurrent Go executions produce cross-session output, or a present language toolchain fails (`multi_language_execution` subtest).
- The restored workspace does not match the pre-corruption state, or a sibling workspace was modified by the restore (`snapshot_restore` subtest).
- `GetConfig()` keeps returning the old value after the config file is rewritten (`Test_HotReload_TakesEffectWithoutRestart`).

---

## Test Design Notes

- **Hermetic**: every test uses `t.TempDir()` for workspaces, snapshots, and config files; no global state is mutated.
- **Graceful degradation for missing toolchains**: the `runIfAvailable` helper detects "not available" in the stderr and skips the language with `t.Logf` instead of failing.
- **Realistic parallelism**: the multi-language subtest launches two goroutines that call `ExecuteCode("go", ...)` simultaneously to mimic two AI sessions sharing the code service.
- **Restore targets a fresh directory**: the `snapshot_restore` subtest restores into `<tmp>/restored-a` rather than overwriting the live workspace, so the assertion is explicit and the live state is preserved for the sibling-isolation check.
- **No network, no container runtime**: the test never starts the gateway HTTP server or a sandbox container; it directly exercises the in-process managers.

---

## Extending the Test

To add a new feature to the integration scenario:

1. Add a new subtest block inside `Test_Phase2_Features_WorkTogether` (or a new top-level function if the scenario is independent).
2. Reuse the shared `wsMgr` / `snapMgr` / `codeSvc` instances declared at the top of the test so the new subtest participates in the same end-to-end story.
3. Keep assertions **strict** — fail loudly if a sibling feature's state changes unexpectedly, since the whole point of this test is to catch cross-feature regressions.
4. If the new feature needs an external dependency (e.g. a runtime, a binary), gate it behind `runIfAvailable`-style detection so the test still runs on minimal CI hosts.

---

## Related Documentation

- [Testing Guide (EN)](testing.md) / [测试指南 (中文)](testing_zh.md) — general test-suite overview, coverage, and conventions.
- [Quick Start Guide (EN)](quick-start-guide.md) / [快速启动指南 (中文)](quick-start-guide_zh.md) — building and running NexusBox itself.
