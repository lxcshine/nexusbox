# NexusBox 测试指南

> 如何运行、组织和扩展 NexusBox 测试套件。

NexusBox 在 `pkg/` 目录下共有 **26 个测试文件、325 个测试函数**，覆盖 API 类型、网关服务、沙箱运行时、安全、网络、调度等模块。本指南说明如何运行这些测试、各包测试的内容，以及新增测试时需遵守的约定。

---

## 前置条件

- [Go 1.22+](https://go.dev/dl/)
- （可选，用于集成测试）Windows 主机用于 Job Object 测试套件；`python3` / `node` 在 `PATH` 中用于代码执行测试；`/bin/bash` 用于 Shell 服务测试。
- （可选）`golangci-lint` 用于代码检查，`make` 用于便捷命令。

运行前先校验工具链：

```bash
go version          # go1.22+
go vet ./...        # 静态检查
```

---

## 快速开始

在仓库根目录运行完整的单元测试套件：

```bash
# 全部包，带竞态检测和覆盖率
go test -race -count=1 -coverprofile=coverage.out ./pkg/...

# 或使用 Makefile 目标
make test-unit
```

打开 HTML 覆盖率报告：

```bash
make coverage     # 基于 coverage.out 生成 coverage.html
```

运行单个包：

```bash
go test -v ./pkg/gateway/...
```

按名称运行单个测试：

```bash
go test -v -run TestShellExec_EchoCommand ./pkg/gateway/
```

---

## 测试分类

### 单元测试（默认）

`pkg/` 下大部分测试是纯单元测试，无外部依赖，可在任意平台运行，通常数秒内完成。

```bash
go test -short ./pkg/...      # 跳过较重的集成测试
```

### 集成测试

少数测试会与宿主机交互（Windows Job Object、真实的 `python` / `node` / `bash` 二进制、真实文件系统）。这些测试通过 `testing.Short()` 和/或构建标签保护，默认快速路径下不会运行：

| 保护方式 | 位置 | 跳过的内容 |
|----------|------|------------|
| `//go:build windows` | `pkg/sandbox/runtime/jobobject_integration_test.go` | Windows Job Object CPU / 内存 / 文件系统集成测试（仅在 Windows 上编译） |
| `if testing.Short() { t.Skip(...) }` | 同上文件，以及 `pkg/gateway/code_service_test.go`、`pkg/gateway/shell_service_test.go` | 真实解释器 / Shell 调用 |
| `t.Skip("python not available")` 等 | `pkg/gateway/code_service_test.go` | 当 `PATH` 中缺少对应二进制时跳过 |

在具备条件的主机上运行完整集成套件：

```bash
# 全部测试，包括慢测试
go test -race -count=1 ./pkg/...

# 仅 Windows Job Object 套件（需 Windows 主机）
go test -v -run TestJobObject ./pkg/sandbox/runtime/
```

Makefile 还暴露了独立目标（当前指向未来的 `test/integration` 和 `test/e2e` 目录）：

```bash
make test-integration
make test-e2e
```

---

## 按包划分的测试文件

下表将每个测试文件映射到它所验证的包及其覆盖范围。

| 包 | 测试文件 | 测试数 | 覆盖内容 |
|----|----------|-------:|----------|
| `pkg/apis/sandbox/v1alpha1` | [types_test.go](../pkg/apis/sandbox/v1alpha1/types_test.go) | 12 | Sandbox 阶段、runtime/priority/network/isolation 常量、spec 默认值、status conditions、资源需求、租户配额、安全配置、批量调度 |
| `pkg/auth` | [jwt_test.go](../pkg/auth/jwt_test.go) | 18 | JWT 认证器初始化、token 校验（有效/无效/过期/错误签发者）、撤销、`AuthInfo` 角色/管理员/沙箱访问、HTTP 中间件（健康检查旁路、无密钥跳过、有效/无效 token）、query-token、上下文辅助函数 |
| `pkg/gateway` | [gateway_test.go](../pkg/gateway/gateway_test.go) | 12 | `/healthz`、`/readyz`、`/v1/system/env`、CORS 中间件、method-not-allowed、列出沙箱、创建/获取租户、代理健康、sandbox-id 头部传递、JSON 写入辅助 |
| `pkg/gateway` | [shell_service_test.go](../pkg/gateway/shell_service_test.go) | 21 | `shell_exec` / `bash_exec`、缺失/失败命令、工作目录与环境、会话创建/列出/获取/终止、环形缓冲区、会话生命周期、工作目录解析（缺少 `/bin/bash` 时跳过） |
| `pkg/gateway` | [file_service_test.go](../pkg/gateway/file_service_test.go) | 16 | 文件写入/读取（含追加和 base64）、自动创建目录、列出、查找、grep（含 include 过滤）、glob、绝对/相对路径解析 |
| `pkg/gateway` | [code_service_test.go](../pkg/gateway/code_service_test.go) | 10 | 代码信息、缺失代码、不支持的语言、Python 与 Node.js 执行（含错误与别名）、`findExecutable`（缺少解释器时跳过） |
| `pkg/gateway` | [browser_service_test.go](../pkg/gateway/browser_service_test.go) | 13 | 浏览器信息、截图（默认/JPEG）、导航（缺失/有效 URL）、点击、输入、滚动、cookies、JS 转义、JSON 辅助 |
| `pkg/gateway` | [e2b_service_test.go](../pkg/gateway/e2b_service_test.go) | 19 | E2B 路由注册、健康、列出/获取模板、沙箱 CRUD、缺失 id/未找到/不允许的方法、object meta、应用模板、时间辅助 |
| `pkg/sdk` | [client_test.go](../pkg/sdk/client_test.go) | 10 | 客户端初始化（含尾部斜杠）、shell/file/code/browser 服务、API 错误处理、认证头 |
| `pkg/sandbox/runtime` | [jobobject_integration_test.go](../pkg/sandbox/runtime/jobobject_integration_test.go) | 6 | 仅 Windows。JobObject 可用性/类型、文件系统沙箱、内存超限杀进程、CPU 速率控制（无错误 + 真实限流） |
| `pkg/sandbox/runtime` | [template_pool_test.go](../pkg/sandbox/runtime/template_pool_test.go) | 11 | 模板池复用与生命周期 |
| `pkg/runtime/containerd` | [client_test.go](../pkg/runtime/containerd/client_test.go) | 17 | Capabilities 丢弃、默认丢弃 caps（无重复、包含关键 caps）、`SecuritySpec` 选项（no-new-privileges、只读 rootfs、runAs user、AppArmor）、GID 处理 |
| `pkg/security` | [manager_test.go](../pkg/security/manager_test.go) | 3 | 安全管理器装配 |
| `pkg/security/filesystem` | [sandbox_test.go](../pkg/security/filesystem/sandbox_test.go) | 18 | 沙箱初始化、读写校验（工作区内/外）、路径遍历（`..`、空字节）、阻塞/允许路径、安全读/写/列/删、最大大小限制 |
| `pkg/security/resource` | [manager_test.go](../pkg/security/resource/manager_test.go) | 10 | 磁盘配额检查（无限制/超限）、usage/limits 查询、存储字符串解析、spec 转换、平台检测 |
| `pkg/security/rootless` | [manager_test.go](../pkg/security/rootless/manager_test.go) | 15 | subuid/subgid 文件解析（有效/格式错误/未找到）、配置禁用状态（无 ranges / 仅 subuid / 仅 subgid） |
| `pkg/template` | [manager_test.go](../pkg/template/manager_test.go) | 15 | 管理器初始化、模板 CRUD（默认值、重复、未找到）、更新、列出、使用计数、应用到沙箱、种子默认值（幂等） |
| `pkg/store/filestore` | [store_test.go](../pkg/store/filestore/store_test.go) | 7 | 存储初始化、沙箱/模板/快照/租户 CRUD、flush、持久化重载 |
| `pkg/logging` | [persistence_test.go](../pkg/logging/persistence_test.go) | 9 | 日志索引、索引+搜索、plain 与 klog 格式、flush+load、清理旧条目、保留策略、持久化 reader、保留管理器 |
| `pkg/proxy` | [port_proxy_test.go](../pkg/proxy/port_proxy_test.go) | 10 | 端口转发添加/列出/移除、健康端点、端口检查（可达/不可达）、诊断、代理/预览处理器（缺失/非法端口） |
| `pkg/network/egress` | [gateway_test.go](../pkg/network/egress/gateway_test.go) | 15 | 出口网关行为 |
| `pkg/network/ebpf` | [engine_test.go](../pkg/network/ebpf/engine_test.go) | 12 | eBPF 引擎行为 |
| `pkg/mcp` | [hub_test.go](../pkg/mcp/hub_test.go) | 23 | MCP hub 工具注册与分发 |
| `pkg/scheduler/framework` | [framework_test.go](../pkg/scheduler/framework/framework_test.go) | 10 | 调度框架插件 |
| `pkg/scheduler/queue` | [queue_test.go](../pkg/scheduler/queue/queue_test.go) | 8 | 调度队列行为 |
| `pkg/tenant/isolation` | [manager_test.go](../pkg/tenant/isolation/manager_test.go) | 5 | 租户隔离管理器初始化、节点可用性（标准/专用）、VNI 分配、标准隔离强制 |

**合计：** 26 个文件，325 个测试函数。

---

## 常用模式

### 缺少依赖时跳过

需要真实解释器、Shell 或系统文件的测试，在依赖缺失时会自动跳过，保证最小化 CI 运行器上的套件仍然通过：

```go
if _, err := exec.LookPath("python3"); err != nil {
    t.Skip("python not available on this system")
}
```

### Short 模式

耗时较长的集成测试遵循 `-short` 标志：

```go
if testing.Short() {
    t.Skip("skipping integration test in short mode")
}
```

快速反馈循环中使用 `go test -short ./pkg/...`。

### 平台特定测试

仅 Windows 的测试使用构建约束，在其他平台上甚至不会编译：

```go
//go:build windows

package runtime
```

在 Windows 主机上运行：

```bash
go test -v -run TestJobObject ./pkg/sandbox/runtime/
```

---

## 常用命令

```bash
# 完整套件，带竞态检测和覆盖率
go test -race -count=1 -coverprofile=coverage.out ./pkg/...

# 仅快速单元测试
go test -short ./pkg/...

# 单个包，详细输出
go test -v ./pkg/gateway/...

# 单个测试，详细输出
go test -v -run TestAuthenticate_ValidToken ./pkg/auth/

# 基准测试（如存在）
go test -bench=. -benchmem ./pkg/...

# 格式检查（CI 强制要求）
test -z "$(gofmt -l .)" || (echo "Files need formatting:"; gofmt -l .; exit 1)

# 静态检查
go vet ./...
golangci-lint run ./...        # 如已安装
```

---

## 持续集成

[.github/workflows/ci.yaml](../.github/workflows/ci.yaml) 中的 CI 工作流在每次 push 和 pull request 时运行以下任务：

| 任务 | 内容 |
|------|------|
| `lint` | `golangci-lint`，超时 5 分钟 |
| `fmt` | 若 `gofmt -l .` 报告任何未格式化文件则失败 |
| `vet` | `go vet ./...` |
| `test-unit` | `go test -race -count=1 -coverprofile=coverage.out ./pkg/...` 并上传覆盖率到 Codecov |
| `build` | 构建 `sandbox-manager`、`sandbox-agent`、`sandbox-scheduler` |
| `codegen-check` | 运行 `make generate`，若生成的代码过期则失败 |
| `mod-check` | 若 `go.mod` / `go.sum` 不 tidy 则失败 |

单元测试当前在 `ubuntu-latest` 上运行；因此 Windows Job Object 集成测试在 CI 上会被跳过（构建标签保护），需在 Windows 主机上手动运行。

---

## 新增测试

1. 将测试文件放在被测代码旁边，命名为 `<source>_test.go`（如 `foo.go` → `foo_test.go`）。
2. 使用标准 `testing` 包，尽量采用表驱动子测试。
3. 若测试需要外部二进制或较慢，使用 `t.Skip(...)` 和/或 `testing.Short()` 保护，保证默认套件快速且可移植。
4. 新增文件时同步更新本文档的包列表表格。
5. 推送前运行 `gofmt -w .` 和 `go vet ./...` —— CI 会强制执行这两项。
