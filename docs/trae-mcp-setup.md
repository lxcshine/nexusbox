# NexusBox MCP Server - Trae IDE 配置指南

本文档介绍如何在 Trae IDE 中接入 NexusBox MCP Server，让 AI 编程助手通过 NexusBox 完成所有 shell、文件、代码执行等操作，而不使用 Trae 的内置工具。

## 前置条件

1. 已编译 `nexusbox-mcp` 可执行文件：

```bash
go build -o nexusbox-mcp.exe ./cmd/nexusbox-mcp/
```

Linux/macOS：

```bash
go build -o nexusbox-mcp ./cmd/nexusbox-mcp/
```

2. 可执行文件已放置在项目根目录或 PATH 中。

## 配置方式

### 方式 1：项目级配置（推荐）

在项目根目录创建 `.trae/mcp.json` 文件（如果已存在请备份后合并）：

```json
{
  "mcpServers": {
    "nexusbox": {
      "command": "${workspaceFolder}/nexusbox-mcp.exe",
      "args": ["-workspace", "${workspaceFolder}"],
      "env": {
        "START_MCP_TIMEOUT_MS": "60000",
        "RUN_MCP_TIMEOUT_MS": "300000"
      }
    }
  }
}
```

Linux/macOS 把 `nexusbox-mcp.exe` 改成 `nexusbox-mcp`。

然后在 Trae 中：**设置 → MCP → 启用项目级 MCP**（首次启用会弹出确认窗口）。

### 方式 2：全局手动配置

在 Trae 中：**设置 → MCP → 添加 → 手动添加**，填入相同的 JSON 配置。

### 方式 3：HTTP 传输（用于共享或多客户端场景）

如果更倾向于让 NexusBox 作为常驻服务，可用 `sandbox-dev` 命令启动 HTTP 模式的 MCP Hub：

```bash
go run ./cmd/sandbox-dev/main.go -port=8080 -mcp-port=8079 -workspace="$PWD"
```

然后在 Trae 中添加 HTTP 类型 MCP：

```json
{
  "mcpServers": {
    "nexusbox": {
      "url": "http://localhost:8079/mcp",
      "headers": {
        "START_MCP_TIMEOUT_MS": "60000",
        "RUN_MCP_TIMEOUT_MS": "300000"
      }
    }
  }
}
```

## 传输方式选择

| 场景 | 推荐方式 | 说明 |
|------|---------|------|
| 个人开发 | stdio（方式 1） | 零网络配置，Trae 自动启动进程 |
| 多人/共享 | HTTP（方式 3） | 单实例服务多客户端 |
| 调试排查 | stdio + stderr 日志 | 日志写入 stderr，不污染 stdout |

## 可用工具列表

NexusBox MCP Hub 注册了 4 个内置 server，共 18 个工具：

### shell（3 个）
- `shell_exec` — 同步执行 shell 命令
- `shell_background` — 启动后台进程
- `shell_check` — 查询后台进程状态

### file（7 个）
- `file_read` / `file_write` — 读写文件
- `file_list` — 列出目录
- `file_search` — 搜索文件内容
- `file_edit` — 替换文件内容
- `file_delete` / `file_move` — 删除/移动

### code（2 个）
- `code_run` — 执行 Python / Node.js 代码
- `code_install` — 安装 pip / npm 依赖

### browser（6 个）
- `browser_navigate` / `browser_screenshot` — 导航和截图
- `browser_click` / `browser_type` — 点击和输入
- `browser_eval` — 执行 JS
- `browser_get_text` — 获取页面文本

## 禁用 Trae 内置工具（可选）

为让 AI 完全通过 NexusBox 完成操作，可在 `.trae/project_rules.md` 中约束：

```markdown
## 工具使用规则
- 所有 shell 命令必须通过 `shell_exec` 工具执行，禁止使用 IDE 内置终端
- 所有文件读写必须通过 `file_read` / `file_write` 工具
- 所有代码执行必须通过 `code_run` 工具
- 浏览器自动化必须通过 `browser_*` 工具
```

## 验证配置

在 Trae 中打开 AI 对话，输入：

```
列出当前工作区的所有 Go 文件
```

如果配置正确，AI 应调用 `file_list` 工具返回结果，而不是使用内置工具。

## 故障排查

### 启动失败：command not found
确认可执行文件路径正确，或使用绝对路径：

```json
"command": "D:/Code/NexusBox/nexusbox-mcp.exe"
```

### 工具调用超时
增大 `RUN_MCP_TIMEOUT_MS`（默认 60 秒，长任务建议 300 秒）。

### 查看日志
stdio 模式的日志写入 stderr，可在 Trae 的输出面板查看。
HTTP 模式的日志在 `sandbox-dev` 启动的终端中。

## 跨平台扩展性

当前实现已为未来 Linux 平台升级预留接口：

- **传输层抽象**：`Transport` 接口（hub.go）支持 stdio/HTTP/SSE
- **Server 接口**：所有 MCP server 实现 `Server` interface，便于扩展
- **路径处理**：workspace 用 `filepath.Abs` 解析，跨平台兼容
- **未来 P2 方案**：Windows 上用 Job Object 隔离，Linux 上用 namespace+cgroup，通过 `Runtime` 接口切换后端
