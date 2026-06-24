<p align="center">
  <img src="logo.png" alt="NexusBox Logo" width="220" />
</p>

<h1 align="center">NexusBox</h1>

<p align="center">
  <strong>AI Agent沙箱 — 集成MCP协议</strong>
</p>

<p align="center">
  <a href="README.md">English</a> &bull;
  <a href="#功能特性">功能特性</a> &bull;
  <a href="#系统架构">系统架构</a> &bull;
  <a href="#快速开始">快速开始</a> &bull;
  <a href="#docker-部署">Docker 部署</a> &bull;
  <a href="#mcp-工具">MCP 工具</a> &bull;
  <a href="#trae-集成">Trae 集成</a> &bull;
  <a href="#api-参考">API 参考</a> &bull;
  <a href="#安全机制">安全机制</a> &bull;
  <a href="#多租户">多租户</a>
</p>

<p align="center">
  <a href="docs/quick-start-guide.md">Quick Start Guide (EN)</a> &bull;
  <a href="docs/quick-start-guide_zh.md">快速启动指南 (中文)</a>
</p>

---

## NexusBox 是什么？

NexusBox 是一款**企业级沙箱平台**，专为 AI Agent 设计。它提供完全隔离的执行环境，AI Agent 可以在其中执行真实操作——运行 Shell 命令、读写文件、执行代码、自动化浏览器——**对宿主机零风险**。

与仅支持 `curl` 或 `docker exec` 的简单 Demo 沙箱不同，NexusBox 实现了 **MCP（Model Context Protocol，模型上下文协议）**，暴露 18 个真实工具供 AI Agent 自主调用。当集成到 Trae、Claude Desktop 或 Cursor 等 AI 编码助手后，NexusBox 将它们从文本生成器转变为**自主 Agent**，可以在隔离工作区中安全执行危险操作。

### 为什么选择 NexusBox？

| 痛点 | NexusBox 解决方案 |
|------|-------------------|
| AI Agent 无法执行真实命令 | 18 个 MCP 工具：Shell、文件、代码、浏览器 |
| 运行 AI 生成的代码有宿主机风险 | 工作区隔离 + 路径遍历防护 |
| 缺乏 Agent-工具交互标准协议 | MCP（Model Context Protocol）— 行业标准 |
| Demo 沙箱仅支持 `curl` | 完整开发环境：Python、Node.js、Chromium、Jupyter、VS Code |
| 无多租户隔离 | 每租户工作区、网络策略、资源配额 |
| 黑盒执行，无可观测性 | 健康检查、结构化日志、Prometheus 指标 |

---

## 功能特性

### 核心能力

- **Shell 执行** — 同步或后台运行任意 Shell 命令，支持超时控制
- **文件操作** — 在沙箱工作区内读取、写入、列出、搜索、替换、删除和移动文件
- **代码执行** — 运行 Python 和 Node.js 代码，支持临时文件处理和超时限制
- **浏览器自动化** — 通过 CDP（Chrome DevTools Protocol）导航、截图、点击、输入和执行 JavaScript
- **端口代理** — 将 HTTP 流量转发到沙箱内部，用于 Web 服务测试

### 企业特性

- **MCP（Model Context Protocol）** — JSON-RPC 2.0 标准接口，用于 AI Agent 集成
- **多租户隔离** — 每租户工作区、网络策略和资源配额
- **安全加固** — 路径遍历防护、Capabilities 丢弃、Rootless 模式、Seccomp
- **完整开发环境** — JupyterLab、code-server（浏览器中的 VS Code）、Chromium、noVNC 桌面
- **可观测性** — 健康检查、结构化日志轮转、Prometheus 指标、审计日志
- **优雅生命周期** — 状态机管理、优雅关闭与资源回收

---

## 快速开始

### 方式一：原生二进制（快速，无需 Docker）

前置条件：[Go 1.22+](https://go.dev/dl/)

```bash
# 克隆仓库
git clone https://github.com/nexusbox/nexusbox.git
cd nexusbox

# 设置 Go 代理（国内用户）
export GOPROXY=https://goproxy.cn,direct

# 编译
go build -o nexusbox-agent ./cmd/sandbox-dev

# 运行
./nexusbox-agent \
  -port=8080 \
  -mcp-port=8079 \
  -proxy-port=6081 \
  -workspace=/path/to/your/workspace \
  -log-level=info
```

### 方式二：Docker Compose（完整环境）

见下方 [Docker 部署](#docker-部署)。

### 验证

```bash
# 健康检查
curl http://localhost:8080/healthz
# 预期输出：ok

# 列出 MCP 工具
curl -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'

# 通过 MCP 执行 Shell 命令
curl -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"shell_exec","arguments":{"command":"echo Hello NexusBox && whoami"}},"id":2}'
```

---

## Docker 部署

### 前置条件

- Docker 20.10+
- Docker Compose 2.0+

### 构建并启动

```bash
cd nexusbox

# 构建并启动（首次构建约 30 分钟）
docker-compose -f deploy/docker/docker-compose.yaml up --build -d

# 查看启动日志（等待 "NexusBox Sandbox - Ready"）
docker logs -f nexusbox-sandbox

# 健康检查
curl http://localhost:8080/healthz
```

### 服务端口

| 端口 | 服务 | URL | 说明 |
|------|------|-----|------|
| 8080 | Gateway API | http://localhost:8080 | REST API 接口 |
| 8079 | MCP Hub | http://localhost:8079/mcp | AI Agent MCP 端点 |
| 6080 | noVNC | http://localhost:6080 | Web 远程桌面 |
| 8888 | JupyterLab | http://localhost:8888 | Python Notebook 环境 |
| 8200 | code-server | http://localhost:8200 | 浏览器中的 VS Code |
| 6081 | 端口代理 | http://localhost:6081/proxy/ | HTTP 端口转发 |

### 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `WORKSPACE` | `/home/sandbox` | 沙箱工作区目录 |
| `LOG_LEVEL` | `info` | 日志级别：debug、info、warn、error |
| `HOST_PORT` | `8080` | Gateway API 宿主机端口映射 |
| `MCP_PORT` | `8079` | MCP Hub 宿主机端口映射 |
| `VNC_PORT` | `6080` | noVNC 宿主机端口映射 |
| `JUPYTER_PORT` | `8888` | JupyterLab 宿主机端口映射 |
| `CODE_SERVER_PORT` | `8200` | code-server 宿主机端口映射 |
| `PROXY_SERVER` | _（空）_ | 出站请求 HTTP 代理 |
| `JWT_PUBLIC_KEY` | _（空）_ | JWT 公钥（用于认证） |

### 自定义配置

```bash
# 使用自定义端口和工作区运行
HOST_PORT=9080 MCP_PORT=9079 WORKSPACE=/data/projects \
  docker-compose -f deploy/docker/docker-compose.yaml up -d
```

### 部署后测试

容器运行后，执行以下测试验证所有服务：

```bash
# 1. Gateway 健康检查
curl http://localhost:8080/healthz
# 预期输出：ok

# 2. 系统环境信息
curl http://localhost:8080/v1/system/env

# 3. 列出所有 MCP 工具（应返回 18 个工具）
curl -s -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1}' | python3 -m json.tool

# 4. 执行 Shell 命令
curl -s -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"shell_exec","arguments":{"command":"uname -a && whoami && pwd"}},"id":2}'

# 5. 写入并读取文件
curl -s -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"file_write","arguments":{"path":"test.txt","content":"Hello from NexusBox!"}},"id":3}'

curl -s -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"file_read","arguments":{"path":"test.txt"}},"id":4}'

# 6. 运行 Python 代码
curl -s -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"code_run","arguments":{"language":"python","code":"import platform; print(f\"Running on {platform.platform()}\")"}},"id":5}'

# 7. 运行 Node.js 代码
curl -s -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"code_run","arguments":{"language":"nodejs","code":"console.log(`Node.js ${process.version} on ${process.platform}`)"}},"id":6}'

# 8. 验证路径遍历防护（应被拦截）
curl -s -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","params":{"name":"file_read","arguments":{"path":"../../etc/passwd"}},"id":7}'
# 预期输出："path is outside workspace"

# 9. 测试 Gateway REST API
curl -s -X POST http://localhost:8080/v1/shell/exec \
  -H "Content-Type: application/json" \
  -d '{"command":"ls -la /home/sandbox"}'

curl -s -X POST http://localhost:8080/v1/code/execute \
  -H "Content-Type: application/json" \
  -d '{"language":"python","code":"print(2+3)"}'

# 10. 检查 noVNC 桌面（在浏览器中打开）
# http://localhost:6080

# 11. 检查 JupyterLab（在浏览器中打开）
# http://localhost:8888

# 12. 检查 code-server（在浏览器中打开）
# http://localhost:8200
```

### 停止和清理

```bash
# 停止容器
docker-compose -f deploy/docker/docker-compose.yaml down

# 停止并删除数据卷（删除所有工作区数据）
docker-compose -f deploy/docker/docker-compose.yaml down -v
```

---

## MCP 工具

NexusBox 通过 MCP（Model Context Protocol）端点 `http://localhost:8079/mcp` 暴露 18 个工具。所有工具使用 JSON-RPC 2.0 格式。

### Shell 工具

| 工具 | 说明 | 关键参数 |
|------|------|----------|
| `shell_exec` | 同步执行 Shell 命令 | `command`、`timeout`（最大 300 秒）、`workDir` |
| `shell_background` | 后台运行长时间命令 | `command`、`id` |
| `shell_check` | 检查后台命令状态 | `id` |

**示例 — 执行命令：**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "shell_exec",
    "arguments": {
      "command": "git clone https://github.com/example/repo.git && cd repo && make build",
      "timeout": 120,
      "workDir": "/home/sandbox"
    }
  },
  "id": 1
}
```

### 文件工具

| 工具 | 说明 | 关键参数 |
|------|------|----------|
| `file_read` | 读取文件内容 | `path`、`offset`、`limit` |
| `file_write` | 写入内容到文件 | `path`、`content`、`append` |
| `file_list` | 列出目录内容 | `path`、`recursive` |
| `file_search` | 搜索文件中的文本 | `path`、`pattern`、`filePattern` |
| `file_replace` | 查找并替换文件中的文本 | `path`、`search`、`replace`、`replaceAll` |
| `file_delete` | 删除文件或目录 | `path`、`recursive` |
| `file_move` | 移动或重命名文件 | `source`、`destination` |

**示例 — 写入并读取文件：**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "file_write",
    "arguments": {
      "path": "src/main.py",
      "content": "from http.server import HTTPServer, BaseHTTPRequestHandler\n\nclass Handler(BaseHTTPRequestHandler):\n    def do_GET(self):\n        self.send_response(200)\n        self.end_headers()\n        self.wfile.write(b'Hello from NexusBox!')\n\nHTTPServer(('0.0.0.0', 8080), Handler).serve_forever()"
    }
  },
  "id": 1
}
```

### 代码工具

| 工具 | 说明 | 关键参数 |
|------|------|----------|
| `code_run` | 执行 Python 或 Node.js 代码 | `language`（python/nodejs）、`code`、`timeout`（最大 120 秒） |
| `code_install` | 安装包 | `language`（python/nodejs）、`packages` |

**示例 — 运行 Python 代码：**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "code_run",
    "arguments": {
      "language": "python",
      "code": "import json\nprint(json.dumps({'status': 'ok', 'value': 42}, indent=2))",
      "timeout": 10
    }
  },
  "id": 1
}
```

### 浏览器工具

| 工具 | 说明 | 关键参数 |
|------|------|----------|
| `browser_navigate` | 导航到 URL | `url` |
| `browser_screenshot` | 截取页面截图 | _（无）_ |
| `browser_click` | 点击页面元素 | `selector` |
| `browser_type` | 在元素中输入文本 | `selector`、`text` |
| `browser_eval` | 执行 JavaScript | `expression` |
| `browser_get_text` | 获取页面文本内容 | `selector` |

**示例 — 导航并截图：**
```json
{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "browser_navigate",
    "arguments": { "url": "https://example.com" }
  },
  "id": 1
}
```

> **注意：** 浏览器工具需要 Chromium 在端口 9222 上以 CDP 模式运行（Docker 部署中已包含）。

---

## Trae 集成

NexusBox 专为 **Trae**（及其他 MCP 兼容的 AI 助手）设计。配置后，Trae 的 AI 将使用 NexusBox 的 MCP 工具而非内置工具，提供**沙箱化执行**来保护宿主机。

### 第 1 步：启动 NexusBox

```bash
# 方式 A：原生二进制
./nexusbox-agent -port=8080 -mcp-port=8079 -proxy-port=6081 -workspace=/path/to/workspace

# 方式 B：Docker
docker-compose -f deploy/docker/docker-compose.yaml up -d
```

### 第 2 步：配置 Trae

在**项目根目录**创建 `.trae/mcp.json`：

```json
{
  "mcpServers": {
    "nexusbox": {
      "url": "http://localhost:8079/mcp",
      "transport": "http"
    }
  }
}
```

### 第 3 步：在 Trae 中启用项目级 MCP

1. 打开 Trae 设置
2. 进入 **MCP** 部分
3. 在 **"导入设置"** 下，启用：**"启动项目级 MCP，允许自动从项目根目录下的 .trae/mcp.json 中加载 MCP 配置"**
4. 重新加载窗口：`Ctrl+Shift+P` → `Reload Window`

### 第 4 步：在 Trae 中使用 NexusBox 工具

在 Trae AI 对话框中，使用明确要求 NexusBox MCP 工具的提示词：

```
请使用 nexusbox MCP 工具完成以下任务。
不要使用 LS、Read、RunCommand 等内置工具。

可用的 nexusbox 工具：
- shell_exec：在沙箱中执行 Shell 命令
- file_read / file_write：在沙箱工作区中读取/写入文件
- file_list：列出目录内容
- code_run：执行 Python 或 Node.js 代码
- browser_navigate / browser_screenshot：浏览器自动化

任务：[在此描述你的任务]
```

### 如何验证 NexusBox 是否生效

| 指标 | NexusBox MCP 生效 | 内置工具生效 |
|------|-------------------|-------------|
| 工具名称 | `file_list`、`shell_exec`、`code_run` | `LS`、`Read`、`RunCommand` |
| 执行路径 | 通过 HTTP 到 `localhost:8079/mcp` | 直接本地执行 |
| 路径遍历 | **已拦截**（仅限工作区） | 无防护 |
| 隔离性 | 沙箱工作区 | 完全宿主机访问 |

### Trae 示例提示词

**创建并运行 Python 项目：**
```
仅使用 nexusbox MCP 工具，不要使用 LS、Read、RunCommand。

1. 使用 file_write 创建 src/app.py，实现一个简单的 HTTP 服务器
2. 使用 code_run 执行它（超时：5 秒）
3. 使用 shell_exec 验证服务器是否启动
```

**分析代码库：**
```
仅使用 nexusbox MCP 工具，不要使用 LS、Read、RunCommand。

1. 使用 file_list 探索项目结构
2. 使用 file_read 读取关键源文件
3. 使用 shell_exec 运行测试
4. 使用 file_replace 修复失败的测试
```

**模拟 CI/CD 流水线：**
```
仅使用 nexusbox MCP 工具，不要使用 LS、Read、RunCommand。

模拟 CI/CD 流水线：
1. 使用 shell_exec 创建项目目录
2. 使用 file_write 创建应用代码和 Dockerfile
3. 使用 code_run 运行单元测试
4. 使用 shell_exec 模拟 docker build 和部署
```

---

## API 参考

### Gateway REST API（端口 8080）

| 方法 | 端点 | 说明 |
|------|------|------|
| GET | `/healthz` | 健康检查 |
| POST | `/v1/shell/exec` | 执行 Shell 命令 |
| POST | `/v1/shell/sessions` | 创建 Shell 会话 |
| POST | `/v1/file/read` | 读取文件 |
| POST | `/v1/file/write` | 写入文件 |
| POST | `/v1/file/list` | 列出目录 |
| POST | `/v1/code/execute` | 执行代码（Python/Node.js） |
| POST | `/v1/browser/navigate` | 浏览器导航 |
| POST | `/v1/browser/screenshot` | 截取截图 |
| POST | `/v1/sandboxes` | 创建沙箱实例 |
| GET | `/v1/sandboxes` | 列出沙箱实例 |
| GET | `/v1/system/env` | 系统环境信息 |
| GET | `/v1/metrics` | Prometheus 指标 |

### MCP 端点（端口 8079）

| 方法 | 说明 |
|------|------|
| `initialize` | 初始化 MCP 连接 |
| `tools/list` | 列出所有可用工具 |
| `tools/call` | 调用工具 |
| `resources/list` | 列出资源（空） |
| `prompts/list` | 列出提示（空） |
| `ping` | 健康检查 |

所有 MCP 请求通过 HTTP POST 发送到 `/mcp`，使用 JSON-RPC 2.0 格式。

---

## 安全机制

### 工作区隔离

所有文件操作限制在沙箱工作区内。`resolvePath()` 函数防止路径遍历攻击：

```
# 以下操作被拦截：
file_read("../../etc/passwd")
→ 错误："path is outside workspace"

# 以下操作正常（工作区内）：
file_read("src/main.py")
→ 返回文件内容
```

### Docker 安全加固

Docker 部署应用了多层安全防护：

```yaml
security_opt:
  - no-new-privileges:true    # 防止提权
cap_drop:
  - ALL                        # 丢弃所有 Linux Capabilities
cap_add:
  - CHOWN                      # 仅添加必需的权限
  - DAC_OVERRIDE
  - FOWNER
  - SETGID
  - SETUID
  - NET_BIND_SERVICE
mem_limit: "8g"               # 内存限制
shm_size: "2gb"               # Chromium 共享内存
```

### 命令执行安全

- Shell 命令**最大超时 300 秒**
- 代码执行**最大超时 120 秒**
- 后台进程可追踪和监控
- 代码执行后自动清理临时文件

---

## 多租户

NexusBox 支持企业级多租户隔离部署。

### 隔离级别

| 级别 | 资源超售 | 节点策略 | 适用场景 |
|------|----------|----------|----------|
| `Standard` | 100% | 共享节点 | 开发测试 |
| `Enhanced` | 50% | 偏好节点 | 生产环境 |
| `Maximum` | 0% | 专用节点 | 合规场景（金融/医疗） |

### 租户配置

```go
tenant := &v1alpha1.Tenant{
    ObjectMeta: metav1.ObjectMeta{Name: "tenant-a"},
    Spec: v1alpha1.TenantSpec{
        DisplayName: "团队 A",
        IsolationLevel: v1alpha1.IsolationLevelMaximum,
        ResourceQuota: v1alpha1.TenantResourceQuota{
            CPU:                 "64",
            Memory:              "128Gi",
            MaxInstances:        100,
            MaxInstancesPerNode: 50,
        },
        NetworkPolicy: &v1alpha1.TenantNetworkPolicy{
            AllowInterTenantCommunication: false,
        },
    },
}
```

### 租户隔离保证

- **文件隔离**：每个租户的工作区相互隔离，跨租户路径遍历被阻止
- **网络隔离**：默认禁止租户间通信
- **资源隔离**：按租户强制执行 CPU、内存和实例数配额
- **节点隔离**：`Maximum` 级别可为租户分配专用节点

---

## 项目结构

```
NexusBox/
├── cmd/
│   ├── sandbox-dev/          # 本地开发入口
│   ├── sandbox-agent/        # Agent 守护进程
│   ├── sandbox-manager/      # 集群管理器
│   └── sandbox-scheduler/    # 调度器
├── pkg/
│   ├── mcp/                  # MCP Hub 和工具服务器
│   │   ├── hub.go            # MCP Hub（JSON-RPC 2.0 路由器）
│   │   ├── shell_server.go   # Shell 执行工具
│   │   ├── file_server.go    # 文件操作工具
│   │   ├── code_server.go    # 代码执行工具
│   │   └── browser_server.go # 浏览器自动化工具
│   ├── gateway/              # REST API 网关
│   ├── proxy/                # 端口代理
│   ├── tenant/               # 多租户管理
│   ├── security/             # 安全（mTLS、Rootless、Capabilities）
│   ├── sandbox/              # 沙箱生命周期和运行时
│   ├── scheduler/            # 调度框架和插件
│   ├── cri/                  # CRI（容器运行时接口）
│   ├── observability/        # 指标、追踪、健康检查、审计
│   └── ...                   # 其他包
├── deploy/
│   ├── docker/
│   │   ├── Dockerfile        # 多阶段 Docker 构建
│   │   ├── docker-compose.yaml
│   │   ├── supervisord.conf  # 进程管理器配置
│   │   └── nexusbox-entrypoint.sh
│   ├── k8s/                  # Kubernetes CRD 和部署
│   └── config/               # 配置文件
├── .trae/
│   └── mcp.json              # Trae MCP 配置
├── logo.png
└── go.mod
```

---

## 技术栈

| 组件 | 技术 |
|------|------|
| 开发语言 | Go 1.22 |
| 通信协议 | MCP（Model Context Protocol）/ JSON-RPC 2.0 |
| 容器运行时 | containerd / runc / gVisor |
| 浏览器自动化 | Chromium + CDP（Chrome DevTools Protocol） |
| 代码执行 | Python 3 + Node.js 22 |
| 桌面环境 | TigerVNC + noVNC |
| 在线 IDE | code-server（浏览器中的 VS Code） |
| Notebook | JupyterLab |
| 进程管理 | Supervisor |
| 容器编排 | Docker Compose / Kubernetes |
| 监控 | Prometheus + OpenTelemetry |
| 存储 | etcd / OverlayFS |

---

## 许可证

Apache License 2.0
