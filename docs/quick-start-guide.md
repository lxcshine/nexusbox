# NexusBox Quick Start Guide — Trae MCP Integration

> Get NexusBox running and connected to Trae in 5 minutes.

---

## Prerequisites

- [Go 1.22+](https://go.dev/dl/) (for native binary) or Docker 20.10+ (for container deployment)
- [Trae](https://trae.ai/) installed and running
- NexusBox source code

---

## Step 1: Start NexusBox

### Option A: Native Binary (Recommended for Quick Testing)

```bash
# Clone the repository
git clone https://github.com/nexusbox/nexusbox.git
cd nexusbox

# Set Go proxy (China users)
export GOPROXY=https://goproxy.cn,direct

# Build
go build -o nexusbox-agent ./cmd/sandbox-dev

# Start the sandbox
./nexusbox-agent \
  -port=8080 \
  -mcp-port=8079 \
  -proxy-port=6081 \
  -workspace=/path/to/your/workspace \
  -log-level=info
```

### Option B: Docker (Full Environment)

```bash
cd nexusbox

# Build and start
docker-compose -f deploy/docker/docker-compose.yaml up --build -d

# Verify it's running
curl http://localhost:8080/healthz
# Expected: ok
```

### Verify NexusBox is Running

```bash
# Health check
curl http://localhost:8080/healthz

# MCP endpoint check
curl -X POST http://localhost:8079/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'

# Should return 18 tools including shell_exec, file_read, code_run, etc.
```

---

## Step 2: Create MCP Configuration File

In your **project root directory**, create the file `.trae/mcp.json`:

```
your-project/
├── .trae/
│   └── mcp.json      <-- Create this file
├── src/
└── ...
```

**File content:**

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

> **Important:** The `.trae/mcp.json` file must be in the **project root** that you open in Trae, not in the NexusBox source directory (unless you're developing NexusBox itself).

---

## Step 3: Enable MCP in Trae

1. Open **Trae**
2. Go to **Settings** (gear icon or `Ctrl+,`)
3. Navigate to the **MCP** section
4. Under **"Import Settings"**, enable:
   > **"Start project-level MCP, allow automatic loading of MCP configuration from .trae/mcp.json in the project root directory"**
5. Reload the window:
   - Press `Ctrl+Shift+P`
   - Type `Reload Window`
   - Press `Enter`

---

## Step 4: Verify the Integration

### Method 1: Check Tool Availability

In Trae AI chat, type:

```
What MCP tools are available from nexusbox?
```

If the integration is working, the AI should list tools like `shell_exec`, `file_read`, `file_write`, `code_run`, etc.

### Method 2: Run a Test Command

In Trae AI chat, type:

```
Please use the nexusbox MCP tool shell_exec to run the command "echo Hello from NexusBox && whoami"
Do NOT use the built-in RunCommand tool.
```

If NexusBox is working, you should see output like:
```
Hello from NexusBox
your-username
```

### Method 3: File Operation Test

```
Please use nexusbox MCP tools only (not LS, Read, or RunCommand):
1. Use file_write to create a file called "nexusbox-test.txt" with content "MCP integration works!"
2. Use file_read to read it back
```

---

## Step 5: Use NexusBox in Daily Work

### Key Concept: NexusBox vs Built-in Tools

| Aspect | NexusBox MCP Tools | Trae Built-in Tools |
|--------|--------------------|---------------------|
| **Tool names** | `file_list`, `shell_exec`, `code_run` | `LS`, `Read`, `RunCommand` |
| **Execution** | Via HTTP to sandbox (port 8079) | Direct on host machine |
| **Isolation** | Sandboxed workspace, path traversal blocked | Full host access |
| **Safety** | Dangerous commands are isolated | No protection |

### Prompt Template

To ensure the AI uses NexusBox tools instead of built-in tools, include this in your prompt:

```
Please use nexusbox MCP tools to complete the following task.
Do NOT use built-in tools like LS, Read, RunCommand, or Grep.

Available nexusbox tools:
- shell_exec: Execute shell commands in sandbox
- shell_background: Run long commands in background
- shell_check: Check background command status
- file_read: Read file content
- file_write: Write content to file
- file_list: List directory contents
- file_search: Search text in files
- file_replace: Find and replace in file
- file_delete: Delete file or directory
- file_move: Move or rename file
- code_run: Execute Python or Node.js code
- code_install: Install pip/npm packages
- browser_navigate: Open URL in browser
- browser_screenshot: Capture screenshot
- browser_click: Click element
- browser_type: Type text into element
- browser_eval: Execute JavaScript
- browser_get_text: Get page text

Task: [your task here]
```

---

## Common Use Cases

### 1. Create and Run a Python Project

```
Use nexusbox MCP tools only.

1. Use file_write to create src/app.py with this content:
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
2. Use code_run to execute it (timeout: 5 seconds)
3. Use shell_exec to verify the server started
```

### 2. Analyze a Codebase

```
Use nexusbox MCP tools only.

1. Use file_list to explore the project structure
2. Use file_read to read the main source files
3. Use shell_exec to run the test suite
4. Use file_replace to fix any failing tests
```

### 3. Simulate CI/CD Pipeline

```
Use nexusbox MCP tools only.

Simulate a CI/CD pipeline:
1. Use shell_exec to create project directories (src/, deploy/, tests/)
2. Use file_write to create application code, Dockerfile, and K8s manifest
3. Use code_run to run unit tests
4. Use shell_exec to simulate docker build and kubectl apply
5. Use shell_exec to verify the deployment
```

### 4. Web Scraping with Browser Automation

```
Use nexusbox MCP tools only.

1. Use browser_navigate to open https://example.com
2. Use browser_screenshot to capture the page
3. Use browser_get_text to extract the main content
4. Use file_write to save the results
```

---

## Troubleshooting

### NexusBox service won't start

```bash
# Check if ports are already in use
# Linux/macOS:
lsof -i :8079 -i :8080

# Windows:
netstat -ano | findstr "8079"
netstat -ano | findstr "8080"

# Kill the process using the port if needed
```

### MCP tools not showing in Trae

1. Verify `.trae/mcp.json` exists in the **project root** directory
2. Verify the file content is valid JSON
3. Verify NexusBox is running: `curl http://localhost:8079/mcp`
4. Reload Trae window: `Ctrl+Shift+P` → `Reload Window`
5. Check Trae's MCP settings — ensure "Start project-level MCP" is enabled

### AI still uses built-in tools instead of NexusBox

- Explicitly tell the AI to use nexusbox MCP tools in your prompt
- Include the prompt template from Step 5
- If the AI calls `LS`, `Read`, or `RunCommand`, it's using built-in tools
- If the AI calls `file_list`, `file_read`, or `shell_exec`, it's using NexusBox

### "Connection refused" on port 8079

```bash
# Check if NexusBox is running
curl http://localhost:8080/healthz

# If not running, start it:
./nexusbox-agent -port=8080 -mcp-port=8079 -proxy-port=6081 -workspace=/path/to/workspace

# If using Docker:
docker ps | grep nexusbox
docker logs nexusbox-sandbox
```

### Path traversal errors

This is **expected behavior** — NexusBox blocks access to files outside the workspace:

```
file_read("../../etc/passwd")
→ Error: "path is outside workspace"
```

Only files within the workspace directory are accessible.

---

## Architecture Overview

```
┌──────────────────┐     HTTP POST      ┌──────────────────┐
│                  │  ───────────────▶  │                  │
│   Trae AI Chat   │   JSON-RPC 2.0    │   MCP Hub        │
│                  │  ◀───────────────  │   (port 8079)    │
│                  │   Tool Results     │                  │
└──────────────────┘                    └────────┬─────────┘
                                                 │
                                                 ▼
                                        ┌──────────────────┐
                                        │  Sandbox Tools   │
                                        │                  │
                                        │  shell_exec      │
                                        │  file_read/write │
                                        │  code_run        │
                                        │  browser_*       │
                                        └────────┬─────────┘
                                                 │
                                                 ▼
                                        ┌──────────────────┐
                                        │  Isolated        │
                                        │  Workspace       │
                                        │  (/home/sandbox) │
                                        └──────────────────┘
```

---

## Next Steps

- Read the full [README.md](../README.md) for complete documentation
- Explore [Docker Deployment](../README.md#docker-deployment) for production setup
- Learn about [Multi-Tenancy](../README.md#multi-tenancy) for enterprise isolation
- Configure [Security](../README.md#security) hardening options
