# NexusBox Phase 2 集成测试使用指南

> 如何运行和扩展用于验证 Phase 2 开发者体验四个功能在真实场景下协同工作的端到端集成测试。

本指南说明位于 [`test/integration/phase2_integration_test.go`](../test/integration/phase2_integration_test.go) 的测试。该测试是**自包含的（hermetic）** —— 它运行在临时目录上，不需要容器运行时或在线 API 服务。

---

## 测试验证的内容

Phase 2 引入了四个必须正确协同工作的功能：

| 编号 | 功能 | 包 | 价值 |
|------|------|----|----|
| 1 | **快照/恢复（VSS）** | `pkg/snapshot` | "回到上次好的状态" |
| 2 | **多语言运行时**（Python / Node / Go / Java） | `pkg/gateway` | "覆盖主流 AI 编程场景" |
| 3 | **热重载配置** | `pkg/config` | "不重启更新沙箱配置" |
| 4 | **工作区隔离**（多项目并行） | `pkg/workspace` | "多个 AI 会话不冲突" |

集成测试将四个功能一起运行，验证它们在真实场景下互不干扰。

---

## 测试场景

文件包含两个顶层测试函数：

### 1. `Test_Phase2_Features_WorkTogether`

模拟**两个并行 AI 会话**（session-a / session-b），每个会话位于各自隔离的工作区中，运行三个子测试：

| 子测试 | 场景 | 断言 |
|--------|------|------|
| `workspace_isolation` | 会话 A 尝试通过 `..` 遍历和绝对路径访问会话 B 的文件 | 遍历被拒绝；绝对路径被重新定位到 A 下且不存在（无跨工作区泄漏） |
| `multi_language_execution` | 两个会话并发执行 Go 代码；若工具链可用则运行 Python / Node / Java | 并发 Go 输出不串扰；可选语言输出 `hello-from-<language>` |
| `snapshot_restore` | 快照 A → 删除 A 的 marker、破坏 A 的 `extra.txt` → 恢复到新目录；同时修改 B | 恢复后的 A 包含 `marker=A` 和 `extra=good`；B 的 `extra` 未被改动 |

### 2. `Test_HotReload_TakesEffectWithoutRestart`

端到端验证热重载 watcher：

1. 写入初始 `runtime.json`，`MaxConcurrentOperations=10`。
2. 启动 `config.Watcher`，每 50 ms 轮询一次，将 `RuntimeManager` 注册为 reloader。
3. 重写文件，将 `MaxConcurrentOperations=77`、`CreateTimeout=3s`，端点字段留空。
4. 轮询 `RuntimeManager.GetConfig()` 直到新值出现（无需重启进程）。

断言：新配置在数秒内可见；空的端点字段被安全地从默认配置继承。

---

## 前置条件

- **Go 1.22+**（构建和运行测试必需）
- **Go 工具链在 `PATH` 上**（始终必需 —— Go 是多语言子测试的基线语言）
- **可选** 工具链，自动检测，缺失时自动跳过：
  - `python3` / `python`
  - `node`（Node.js）
  - `javac` / `java`
- **Windows** 推荐用于验证 VSS 后端；非 Windows 主机上快照管理器自动选择 filesystem 后端。
- 在 Windows 上使用 VSS 后端需要**管理员权限**。无管理员权限时 VSS 失败，管理器会**优雅回退**到 filesystem 后端 —— 测试仍然通过。

校验工具链：

```bash
go version          # go1.22+
go vet ./...        # 静态检查
```

---

## 运行测试

### 仅运行集成测试

在仓库根目录执行：

```bash
go test ./test/integration/... -v -count=1 -timeout 180s
```

预期输出（节选）：

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

### 启用竞态检测

```bash
go test ./test/integration/... -race -count=1 -timeout 300s
```

### 与相关包的单元测试一起运行

```bash
go test ./pkg/snapshot/... ./pkg/config/... ./pkg/workspace/... `
         ./pkg/sandbox/runtime/... ./pkg/gateway/... `
         ./test/integration/... `
         -count=1 -timeout 300s
```

> 注意：Windows PowerShell 使用反引号 `` ` `` 续行；bash/zsh 使用 `\` 续行。

### 仅运行单个子测试

```bash
go test ./test/integration/... -run 'Test_Phase2_Features_WorkTogether/snapshot_restore' -v
```

---

## 解读输出

### 正常日志行

- `workspace: created session-a at <tmp>\session-a (owner=agent-1)` —— 已创建两个并行工作区。
- `Creating snapshot snap-session-a-<ts> for sandbox session-a via vss backend` —— 快照创建已启动。
- `Created snapshot ... (size: 5 bytes, backend: filesystem)` —— 快照成功。
- `Runtime manager config hot-reloaded: ... maxConcurrentOps=77 createTimeout=3s` —— 热重载已应用。
- `hotreload: reloader runtime-manager applied new config` —— reloader 已确认新配置。

### 警告：VSS 回退

如果你看到下面的警告，在**非管理员 Windows 或 Linux/macOS 上是预期行为**，测试仍然通过：

```
W vss_windows.go:78] VSS: create shadow failed for volume C:: ...
; falling back to filesystem backend
```

这条警告证明后端抽象工作正常：VSS 不可用时，快照管理器透明切换到跨平台 filesystem 后端。

### 失败信号

测试在以下情况会失败：

- 工作区解析器允许路径逃逸到兄弟工作区（`workspace_isolation` 子测试）。
- 并发 Go 执行产生跨会话输出，或已安装的语言工具链执行失败（`multi_language_execution` 子测试）。
- 恢复后的工作区与破坏前状态不符，或恢复操作改动了兄弟工作区（`snapshot_restore` 子测试）。
- 配置文件被重写后 `GetConfig()` 仍返回旧值（`Test_HotReload_TakesEffectWithoutRestart`）。

---

## 测试设计说明

- **自包含**：每个测试使用 `t.TempDir()` 创建工作区、快照和配置文件；不修改全局状态。
- **缺失工具链的优雅降级**：`runIfAvailable` 辅助函数检测 stderr 中的 "not available"，用 `t.Logf` 跳过该语言而非失败。
- **真实并发**：多语言子测试启动两个 goroutine 同时调用 `ExecuteCode("go", ...)`，模拟两个 AI 会话共享代码服务。
- **恢复目标为新目录**：`snapshot_restore` 子测试恢复到 `<tmp>/restored-a` 而非覆盖活跃工作区，使断言显式且保留活跃状态用于兄弟隔离检查。
- **无网络、无容器运行时**：测试从不启动 gateway HTTP 服务或沙箱容器；直接在进程内调用各个管理器。

---

## 扩展测试

向集成场景添加新功能时：

1. 在 `Test_Phase2_Features_WorkTogether` 内添加新的子测试块（若场景独立则新增顶层函数）。
2. 复用测试顶部声明的共享 `wsMgr` / `snapMgr` / `codeSvc` 实例，使新子测试参与同一端到端故事。
3. 保持断言**严格** —— 一旦兄弟功能的状态意外变化就大声失败，因为本测试的全部意义就是捕获跨功能回归。
4. 若新功能需要外部依赖（如运行时、二进制），用 `runIfAvailable` 风格的检测做门控，使测试在最小化 CI 主机上仍可运行。

---

## 相关文档

- [Testing Guide (EN)](testing.md) / [测试指南 (中文)](testing_zh.md) —— 通用测试套件概览、覆盖率与约定。
- [Quick Start Guide (EN)](quick-start-guide.md) / [快速启动指南 (中文)](quick-start-guide_zh.md) —— 构建和运行 NexusBox 本身。
