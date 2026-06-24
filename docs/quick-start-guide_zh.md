# NexusBox 快速启动指南 — Trae MCP 集成配置

> 5 分钟内让 NexusBox 运行并连接到 Trae。

---

## 前置条件

- [Go 1.22+](https://go.dev/dl/)（原生二进制方式）或 Docker 20.10+（容器部署方式）
- [Trae](https://trae.ai/) 已安装并运行
- NexusBox 源代码

---

## 第 1 步：启动 NexusBox

### 方式 A：原生二进制（推荐用于快速测试）

```bash
# 克隆仓库
git clone https://github.com/nexusbox/nexusbox.git
cd nexusbox

# 设置 Go 代理（国内用户）
export GOPROXY=https://goproxy.cn,direct

# 编译
go build -o nexusbox-agent ./cmd/sandbox-dev

# 启动沙箱
./nexusbox-agent \
  -port=8080 \
  -mcp-port=8079 \
  -proxy-port=6081 \
  -workspace=/path/to/your/workspace \
  -log-level=info
```

### 方式 B：Docker（完整环境）

```bash
cd nexusbox

# 构建并启动
docker-compose -f deploy/docker/docker-compose.yaml up --build -d

# 验证是否运行
curl http://localhost:8080/healthz
# 预期输出：ok
```

### 验证 NexusBox 是否运行

```bash
# 健康检查
curl http://localhost:8080/healthz

# MCP 端点检查
curl -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'

# 应返回 18 个工具，包括 shell_exec、file_read、code_run 等
```

---

## 第 2 步：创建 MCP 配置文件

在你的**项目根目录**下创建文件 `.trae/mcp.json`：

```
your-project/
├── .trae/
│   └── mcp.json      <-- 创建此文件
├── src/
└── ...
```

**文件内容：**

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

> **重要：** `.trae/mcp.json` 文件必须位于你在 Trae 中打开的**项目根目录**下，而不是 NexusBox 源代码目录（除非你正在开发 NexusBox 本身）。

---

## 第 3 步：在 Trae 中启用 MCP

1. 打开 **Trae**
2. 进入 **设置**（齿轮图标或 `Ctrl+,`）
3. 找到 **MCP** 部分
4. 在 **"导入设置"** 下，启用：
   
   > **"启动项目级 MCP，允许自动从项目根目录下的 .trae/mcp.json 中加载 MCP 配置"**
5. 重新加载窗口：
   - 按 `Ctrl+Shift+P`
   - 输入 `Reload Window`
   - 按 `Enter`

---

## 第 4 步：验证集成是否成功

### 方法 1：检查工具可用性

在 Trae AI 对话框中输入：

```
nexusbox 提供了哪些 MCP 工具？
```

如果集成成功，AI 应列出 `shell_exec`、`file_read`、`file_write`、`code_run` 等工具。

### 方法 2：运行测试命令

在 Trae AI 对话框中输入：

```
请使用 nexusbox MCP 工具 shell_exec 运行命令 "echo Hello from NexusBox && whoami"
不要使用内置的 RunCommand 工具。
```

如果 NexusBox 正常工作，应看到类似输出：

```
Hello from NexusBox
your-username
```

### 方法 3：文件操作测试

```
请仅使用 nexusbox MCP 工具（不要使用 LS、Read、RunCommand）：
1. 使用 file_write 创建文件 "nexusbox-test.txt"，内容为 "MCP 集成成功！"
2. 使用 file_read 读回文件内容
```

---

## 第 5 步：在日常工作中使用 NexusBox

### 核心概念：NexusBox vs 内置工具

| 方面       | NexusBox MCP 工具                     | Trae 内置工具                |
| ---------- | ------------------------------------- | ---------------------------- |
| **工具名称** |`file_list`、`shell_exec`、`code_run`|  `LS`、`Read`、`RunCommand`  |
| **执行方式** | 通过 HTTP 到沙箱（端口 8079）         | 直接在宿主机执行              |
| **隔离性**  | 沙箱工作区，路径遍历被拦截             | 完全宿主机访问                |
| **安全性**  | 危险命令被隔离                        | 无防护                       |

### 提示词模板

为确保 AI 使用 NexusBox 工具而非内置工具，在提示词中包含以下内容：

```
请使用 nexusbox MCP 工具完成以下任务。
不要使用 LS、Read、RunCommand、Grep 等内置工具。

可用的 nexusbox 工具：
- shell_exec：在沙箱中执行 Shell 命令
- shell_background：后台运行长命令
- shell_check：检查后台命令状态
- file_read：读取文件内容
- file_write：写入内容到文件
- file_list：列出目录内容
- file_search：搜索文件中的文本
- file_replace：查找并替换文件中的文本
- file_delete：删除文件或目录
- file_move：移动或重命名文件
- code_run：执行 Python 或 Node.js 代码
- code_install：安装 pip/npm 包
- browser_navigate：在浏览器中打开 URL
- browser_screenshot：截取页面截图
- browser_click：点击页面元素
- browser_type：在元素中输入文本
- browser_eval：执行 JavaScript
- browser_get_text：获取页面文本

任务：[在此描述你的任务]
```

---

## 常见使用场景

### 1. 创建并运行 Python 项目

```
仅使用 nexusbox MCP 工具。

1. 使用 file_write 创建 src/app.py，内容如下：
   ```python
   from http.server import HTTPServer, BaseHTTPRequestHandler
   class Handler(BaseHTTPRequestHandler):
       def do_GET(self):
           self.send_response(200)
               self.send_header('Content-type', 'application/json')
               self.end_headers()
               self.wfile.write(b'{"status":"running"}')
   HTTPServer(('0.0.0.0', 8080), Handler).serve_forever()
```

2. 使用 code_run 执行它（超时：5 秒）
3. 使用 shell_exec 验证服务器是否启动
   
   ```
   
   ```

### 2. 分析代码库

```
仅使用 nexusbox MCP 工具。

1. 使用 file_list 探索项目结构
2. 使用 file_read 读取主要源文件
3. 使用 shell_exec 运行测试套件
4. 使用 file_replace 修复失败的测试
```

### 3. 模拟 CI/CD 流水线

```
仅使用 nexusbox MCP 工具。

模拟 CI/CD 流水线：
1. 使用 shell_exec 创建项目目录（src/、deploy/、tests/）
2. 使用 file_write 创建应用代码、Dockerfile 和 K8s 清单
3. 使用 code_run 运行单元测试
4. 使用 shell_exec 模拟 docker build 和 kubectl apply
5. 使用 shell_exec 验证部署结果
```

### 4. 使用浏览器自动化进行网页抓取

```
仅使用 nexusbox MCP 工具。

1. 使用 browser_navigate 打开 https://example.com
2. 使用 browser_screenshot 截取页面
3. 使用 browser_get_text 提取主要内容
4. 使用 file_write 保存结果
```

---

## 故障排除

### NexusBox 服务无法启动

```bash
# 检查端口是否已被占用
# Linux/macOS：
lsof -i :8079 -i :8080

# Windows：
netstat -ano | findstr "8079"
netstat -ano | findstr "8080"

# 如需释放端口，终止占用进程
```

### MCP 工具在 Trae 中不显示

1. 确认 `.trae/mcp.json` 存在于**项目根目录**下
2. 确认文件内容是有效的 JSON
3. 确认 NexusBox 正在运行：`curl http://localhost:8079/mcp`
4. 重新加载 Trae 窗口：`Ctrl+Shift+P` → `Reload Window`
5. 检查 Trae 的 MCP 设置 — 确保"启动项目级 MCP"已启用

### AI 仍使用内置工具而非 NexusBox

- 在提示词中明确告诉 AI 使用 nexusbox MCP 工具
- 包含第 5 步中的提示词模板
- 如果 AI 调用 `LS`、`Read`、`RunCommand`，说明在使用内置工具
- 如果 AI 调用 `file_list`、`file_read`、`shell_exec`，说明在使用 NexusBox

### 端口 8079 "Connection refused"

```bash
# 检查 NexusBox 是否在运行
curl http://localhost:8080/healthz

# 如果未运行，启动它：
./nexusbox-agent -port=8080 -mcp-port=8079 -proxy-port=6081 -workspace=/path/to/workspace

# 如果使用 Docker：
docker ps | grep nexusbox
docker logs nexusbox-sandbox
```

### 路径遍历错误

这是**预期行为** — NexusBox 会阻止访问工作区外的文件：

```
file_read("../../etc/passwd")
→ 错误："path is outside workspace"
```

只有工作区目录内的文件可以访问。

---

## 架构概览

```
┌──────────────────┐     HTTP POST      ┌──────────────────┐
│                  │  ───────────────▶  │                  │
│   Trae AI 对话   │   JSON-RPC 2.0    │   MCP Hub        │
│                  │  ◀───────────────  │   (端口 8079)    │
│                  │   工具执行结果      │                  │
└──────────────────┘                    └────────┬─────────┘
                                                 │
                                                 ▼
                                        ┌──────────────────┐
                                        │  沙箱工具         │
                                        │                  │
                                        │  shell_exec      │
                                        │  file_read/write │
                                        │  code_run        │
                                        │  browser_*       │
                                        └────────┬─────────┘
                                                 │
                                                 ▼
                                        ┌──────────────────┐
                                        │  隔离工作区       │
                                        │  (/home/sandbox) │
                                        └──────────────────┘
```

---

## 下一步

- 阅读完整 [README.md](../README_zh.md) 获取完整文档
- 了解 [Docker 部署](../README_zh.md#docker-部署) 用于生产环境
- 学习 [多租户](../README_zh.md#多租户) 企业级隔离
- 配置[安全](../README_zh.md#安全机制)加固选项
