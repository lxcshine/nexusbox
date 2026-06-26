# NexusBox Testing Guide

> How to run, organize, and extend the NexusBox test suite.

NexusBox ships with **325 test functions across 26 test files** under `pkg/`, covering API types, gateway services, sandbox runtime, security, networking, scheduling, and more. This guide explains how to run them, what each package tests, and the conventions to follow when adding new tests.

---

## Prerequisites

- [Go 1.22+](https://go.dev/dl/)
- (Optional, for integration tests) Windows for the Job Object suite, `python3` / `node` on `PATH` for the code-execution tests, `/bin/bash` for the shell-service tests.
- (Optional) `golangci-lint` for linting, `make` for the convenience targets.

Verify the toolchain before running the suite:

```bash
go version          # go1.22+
go vet ./...        # static checks
```

---

## Quick Start

Run the entire unit test suite from the repository root:

```bash
# All packages, with race detector and coverage
go test -race -count=1 -coverprofile=coverage.out ./pkg/...

# Or via the Makefile target
make test-unit
```

Open the HTML coverage report:

```bash
make coverage     # builds coverage.html from coverage.out
```

Run a single package:

```bash
go test -v ./pkg/gateway/...
```

Run a single test by name:

```bash
go test -v -run TestShellExec_EchoCommand ./pkg/gateway/
```

---

## Test Categories

### Unit Tests (default)

Most tests under `pkg/` are pure unit tests with no external dependencies. They run on every platform and finish in seconds.

```bash
go test -short ./pkg/...      # skip heavier integration tests
```

### Integration Tests

A few tests interact with the host (Windows Job Object, real `python`/`node`/`bash` binaries, real filesystem). They are guarded by `testing.Short()` and/or build tags so they don't run in the default fast path:

| Guard | Where | What it skips |
|-------|-------|---------------|
| `//go:build windows` | `pkg/sandbox/runtime/jobobject_integration_test.go` | Windows Job Object CPU/memory/filesystem integration (only compiles on Windows) |
| `if testing.Short() { t.Skip(...) }` | same file, plus `pkg/gateway/code_service_test.go`, `pkg/gateway/shell_service_test.go` | Real interpreter / shell invocations |
| `t.Skip("python not available")` etc. | `pkg/gateway/code_service_test.go` | Skips when the binary is missing from `PATH` |

Run the full integration suite on a capable host:

```bash
# Everything, including slow tests
go test -race -count=1 ./pkg/...

# Just the Windows Job Object suite (Windows host only)
go test -v -run TestJobObject ./pkg/sandbox/runtime/
```

The Makefile also exposes separate targets (currently pointing at future `test/integration` and `test/e2e` trees):

```bash
make test-integration
make test-e2e
```

---

## Test Files by Package

The table below maps every test file to the package it exercises and what it covers.

| Package | Test File | # Tests | Covers |
|---------|-----------|--------:|--------|
| `pkg/apis/sandbox/v1alpha1` | [types_test.go](../pkg/apis/sandbox/v1alpha1/types_test.go) | 12 | Sandbox phases, runtime/priority/network/isolation constants, spec defaults, status conditions, resource requirements, tenant quotas, security specs, batch scheduling |
| `pkg/auth` | [jwt_test.go](../pkg/auth/jwt_test.go) | 18 | JWT authenticator init, token validation (valid/invalid/expired/wrong-issuer), revocation, `AuthInfo` roles/admin/sandbox access, HTTP middleware (health bypass, no-key skip, valid/invalid token), query-token, context helpers |
| `pkg/gateway` | [gateway_test.go](../pkg/gateway/gateway_test.go) | 12 | `/healthz`, `/readyz`, `/v1/system/env`, CORS middleware, method-not-allowed, list sandboxes, create/get tenant, proxy health, sandbox-id header propagation, JSON write helpers |
| `pkg/gateway` | [shell_service_test.go](../pkg/gateway/shell_service_test.go) | 21 | `shell_exec` / `bash_exec`, missing/failing commands, workdir & env, session create/list/get/kill, ring buffer, session lifecycle, workdir resolution (skips `/bin/bash` when absent) |
| `pkg/gateway` | [file_service_test.go](../pkg/gateway/file_service_test.go) | 16 | File write/read (incl. append & base64), create-dirs, list, find, grep (with include filter), glob, absolute/relative path resolution |
| `pkg/gateway` | [code_service_test.go](../pkg/gateway/code_service_test.go) | 10 | Code info, missing code, unsupported language, Python & Node.js execution (incl. errors & aliases), `findExecutable` (skips when interpreter missing) |
| `pkg/gateway` | [browser_service_test.go](../pkg/gateway/browser_service_test.go) | 13 | Browser info, screenshot (default/JPEG), navigate (missing/valid URL), click, type, scroll, cookies, JS escaping, JSON helper |
| `pkg/gateway` | [e2b_service_test.go](../pkg/gateway/e2b_service_test.go) | 19 | E2B route registration, health, list/get templates, sandbox CRUD, missing-id/not-found/method-not-allowed, object meta, apply template, time helpers |
| `pkg/sdk` | [client_test.go](../pkg/sdk/client_test.go) | 10 | Client init (incl. trailing slash), shell/file/code/browser services, API error handling, auth header |
| `pkg/sandbox/runtime` | [jobobject_integration_test.go](../pkg/sandbox/runtime/jobobject_integration_test.go) | 6 | Windows-only. JobObject availability/type, filesystem sandbox, memory-limit kills the process, CPU rate control (no-error + real throttling) |
| `pkg/sandbox/runtime` | [template_pool_test.go](../pkg/sandbox/runtime/template_pool_test.go) | 11 | Template pool reuse and lifecycle |
| `pkg/runtime/containerd` | [client_test.go](../pkg/runtime/containerd/client_test.go) | 17 | Capability dropping, default dropped caps (no duplicates, critical caps included), `SecuritySpec` options (no-new-privileges, read-only rootfs, runAs user, AppArmor), GID handling |
| `pkg/security` | [manager_test.go](../pkg/security/manager_test.go) | 3 | Security manager wiring |
| `pkg/security/filesystem` | [sandbox_test.go](../pkg/security/filesystem/sandbox_test.go) | 18 | Sandbox init, read/write validation (in/out of workspace), path traversal (`..`, null byte), blocked/allowed paths, safe read/write/list/delete, max-size enforcement |
| `pkg/security/resource` | [manager_test.go](../pkg/security/resource/manager_test.go) | 10 | Disk quota check (no-limit/exceeded), usage/limits lookup, storage-string parsing, spec conversion, platform detection |
| `pkg/security/rootless` | [manager_test.go](../pkg/security/rootless/manager_test.go) | 15 | subuid/subgid file parsing (valid/malformed/not-found), config disabled states (no ranges / only subuid / only subgid) |
| `pkg/template` | [manager_test.go](../pkg/template/manager_test.go) | 15 | Manager init, template CRUD (defaults, duplicate, not-found), update, list, usage increment, apply-to-sandbox, seed-defaults (idempotent) |
| `pkg/store/filestore` | [store_test.go](../pkg/store/filestore/store_test.go) | 7 | Store init, sandbox/template/snapshot/tenant CRUD, flush, persistence reload |
| `pkg/logging` | [persistence_test.go](../pkg/logging/persistence_test.go) | 9 | Log index, index+search, plain & klog formats, flush+load, cleanup old entries, retention, persisted reader, retention manager |
| `pkg/proxy` | [port_proxy_test.go](../pkg/proxy/port_proxy_test.go) | 10 | Forwarding add/list/remove, health endpoint, port check (reachable/unreachable), diagnose, proxy/preview handlers (missing/invalid port) |
| `pkg/network/egress` | [gateway_test.go](../pkg/network/egress/gateway_test.go) | 15 | Egress gateway behavior |
| `pkg/network/ebpf` | [engine_test.go](../pkg/network/ebpf/engine_test.go) | 12 | eBPF engine behavior |
| `pkg/mcp` | [hub_test.go](../pkg/mcp/hub_test.go) | 23 | MCP hub tool registration and dispatch |
| `pkg/scheduler/framework` | [framework_test.go](../pkg/scheduler/framework/framework_test.go) | 10 | Scheduler framework plugins |
| `pkg/scheduler/queue` | [queue_test.go](../pkg/scheduler/queue/queue_test.go) | 8 | Scheduling queue behavior |
| `pkg/tenant/isolation` | [manager_test.go](../pkg/tenant/isolation/manager_test.go) | 5 | Tenant isolation manager init, node availability (standard/dedicated), VNI assignment, standard isolation enforcement |

**Totals:** 26 files, 325 test functions.

---

## Common Patterns

### Skipping on missing dependencies

Tests that need a real interpreter, shell, or system file skip themselves when the dependency is missing, so the suite stays green on minimal CI runners:

```go
if _, err := exec.LookPath("python3"); err != nil {
    t.Skip("python not available on this system")
}
```

### Short mode

Long-running integration tests honour `-short`:

```go
if testing.Short() {
    t.Skip("skipping integration test in short mode")
}
```

Run with `go test -short ./pkg/...` in fast feedback loops.

### Platform-specific tests

Windows-only tests use a build constraint so they don't even compile elsewhere:

```go
//go:build windows

package runtime
```

Run them on a Windows host:

```bash
go test -v -run TestJobObject ./pkg/sandbox/runtime/
```

---

## Useful Commands

```bash
# Full suite with race detector and coverage
go test -race -count=1 -coverprofile=coverage.out ./pkg/...

# Only fast unit tests
go test -short ./pkg/...

# One package, verbose
go test -v ./pkg/gateway/...

# One test, verbose
go test -v -run TestAuthenticate_ValidToken ./pkg/auth/

# Benchmarks (when present)
go test -bench=. -benchmem ./pkg/...

# Format check (CI enforces this)
test -z "$(gofmt -l .)" || (echo "Files need formatting:"; gofmt -l .; exit 1)

# Static checks
go vet ./...
golangci-lint run ./...        # if installed
```

---

## Continuous Integration

The CI workflow in [.github/workflows/ci.yaml](../.github/workflows/ci.yaml) runs the following jobs on every push and pull request:

| Job | What it does |
|-----|--------------|
| `lint` | `golangci-lint` with a 5-minute timeout |
| `fmt` | Fails if `gofmt -l .` reports any unformatted file |
| `vet` | `go vet ./...` |
| `test-unit` | `go test -race -count=1 -coverprofile=coverage.out ./pkg/...` and uploads coverage to Codecov |
| `build` | Builds `sandbox-manager`, `sandbox-agent`, `sandbox-scheduler` |
| `codegen-check` | Runs `make generate` and fails if generated code is stale |
| `mod-check` | Fails if `go.mod` / `go.sum` are not tidy |

Unit tests currently run on `ubuntu-latest`; the Windows Job Object integration tests are therefore skipped on CI (build-tag guarded) and must be run manually on a Windows host.

---

## Verifying JupyterLab Access Through the DevTool Proxy

The DevTool subsystem (`pkg/devtool`) launches JupyterLab / code-server inside a NexusBox sandbox and exposes them through a WebSocket-compatible reverse proxy. The end-to-end script [test_jupyter_proxy.ps1](../test_jupyter_proxy.ps1) verifies the full path: sandbox creation → DevTool start → health check → proxy access → cleanup.

### Prerequisites

- Windows host (the script is PowerShell).
- NexusBox dev server running on `http://localhost:8080`. Start it from the repo root:

  ```powershell
  go run ./cmd/sandbox-dev/ -port=8080 -mcp-port=8079 -proxy-port=6081 `
      -workspace=$env:TEMP\nexusbox-test -log-level=info
  ```

- `jupyter` binary resolvable on `PATH` (the launcher logs the resolved path at startup, e.g. `D:\software\anaconda3\Scripts\jupyter.exe`). If absent, the DevTool API returns `jupyter binary not found`.

### Running the Script

From the repository root:

```powershell
powershell -ExecutionPolicy Bypass -File test_jupyter_proxy.ps1
```

The script is self-contained and idempotent — it creates its own sandbox (`jupyter-test-sb`) and working directory (`%TEMP%\nexusbox-jupyter-test`), then deletes them at the end.

### What the Script Does

| Step | Action | API / URL |
|------|--------|-----------|
| 1/6 | Health-check the Gateway | `GET /healthz` |
| 2/6 | Create a sandbox with an isolated working dir | `POST /v1/sandboxes` |
| 3/6 | Start JupyterLab via the DevTool API (auth disabled for the test) | `POST /v1/devtools` |
| 4/6 | Poll the DevTool health endpoint until JupyterLab is ready (up to 40s) | `GET /v1/devtools/{instanceId}/health` |
| 5/6 | Fetch JupyterLab through the proxy and verify the response contains Jupyter markers | `GET /v1/devtools/proxy/jupyter/jupyter-test-sb/` |
| 6/6 | List DevTool instances, then stop the DevTool and delete the sandbox | `DELETE /v1/devtools/{instanceId}` / `DELETE /v1/sandboxes/{name}` |

### Expected Output

A successful run prints:

```
[1/6] Checking NexusBox Gateway health...
  OK: Gateway is healthy
[2/6] Creating NexusBox sandbox...
  OK: Sandbox created
[3/6] Starting JupyterLab via DevTool API...
  OK: JupyterLab dev tool started
  Instance ID: dt-jupyter-xxxxxxxxxxxx
  Port: 49152
  Status: pending
[4/6] Waiting for JupyterLab to become ready...
  OK: JupyterLab is healthy (waited 4s)
[5/6] Testing JupyterLab access through DevTool proxy...
  HTTP Status: 200
  Content Length: 3929 bytes
  OK: Response contains JupyterLab content!
[6/6] Listing all dev tool instances...
  Found 1 dev tool instance(s):
    - ID: dt-jupyter-xxxxxxxxxxxx, Type: jupyter, Port: 49152, Status: running
Cleaning up: stopping JupyterLab instance...
  OK: Dev tool stopped
Cleaning up: deleting sandbox...
  OK: Sandbox deleted
```

### Manual Access

Once the script (or any DevTool start call) leaves JupyterLab running, you can also open the proxy URL directly in a browser:

```
http://localhost:8080/v1/devtools/proxy/jupyter/{sandboxId}/lab
```

The proxy rewrites Jupyter's `302 / → /lab?` redirect so the root URL works too. WebSocket upgrade is supported for live kernel sessions.

### Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| `jupyter binary not found` | `jupyter` not on `PATH` seen by the Go process | Install JupyterLab or add its `Scripts` directory to `PATH` and restart the dev server |
| DevTool status stays `failed` | JupyterLab cannot write its runtime files | Launcher already redirects `JUPYTER_RUNTIME_DIR` / `JUPYTER_DATA_DIR` into the working dir — make sure the working dir exists and is writable |
| Proxy returns `404 page not found` | JupyterLab not yet ready, or proxy path mistyped | Wait for `status: running` and use the exact path `/v1/devtools/proxy/jupyter/{sandboxId}/...` |
| Proxy returns `503 dev tool is not running` | DevTool exited after startup | Inspect `.devtool-jupyter.log` in the sandbox working dir for the Python traceback |

---

## Adding a New Test

1. Place the file next to the code it tests, named `<source>_test.go` (e.g. `foo.go` → `foo_test.go`).
2. Use the standard `testing` package and table-driven subtests where possible.
3. If the test needs an external binary or is slow, guard it with `t.Skip(...)` and/or `testing.Short()` so the default suite stays fast and portable.
4. Keep the package list table in this document up to date when you add a new file.
5. Run `gofmt -w .` and `go vet ./...` before pushing — CI enforces both.
