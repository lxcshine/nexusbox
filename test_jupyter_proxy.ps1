# NexusBox JupyterLab DevTool Proxy E2E Test
# This script verifies that JupyterLab can be started and accessed
# through the NexusBox DevTool proxy.

$ErrorActionPreference = "Continue"
$baseUrl = "http://localhost:8080"

Write-Host ""
Write-Host "============================================================"
Write-Host "  NexusBox JupyterLab DevTool Proxy E2E Test"
Write-Host "============================================================"
Write-Host ""

# Step 1: Health check
Write-Host "[1/6] Checking NexusBox Gateway health..."
try {
    $health = Invoke-RestMethod -Uri "$baseUrl/healthz" -Method Get -TimeoutSec 5
    Write-Host "  OK: Gateway is healthy" -ForegroundColor Green
} catch {
    Write-Host "  FAIL: Gateway is not responding: $_" -ForegroundColor Red
    exit 1
}

# Step 2: Create a sandbox
Write-Host ""
Write-Host "[2/6] Creating NexusBox sandbox..."
$workspaceDir = Join-Path $env:TEMP "nexusbox-jupyter-test"
if (!(Test-Path $workspaceDir)) { New-Item -ItemType Directory -Force -Path $workspaceDir | Out-Null }

$sandboxBody = @{
    apiVersion = "nexusbox.io/v1alpha1"
    kind = "Sandbox"
    metadata = @{
        name = "jupyter-test-sb"
        namespace = "default"
    }
    spec = @{
        runtime = "runc"
        tenantRef = @{
            name = "default"
        }
        command = @("cmd", "/c", "timeout", "300")
        workingDir = $workspaceDir
    }
} | ConvertTo-Json -Depth 5

try {
    $sbResp = Invoke-RestMethod -Uri "$baseUrl/v1/sandboxes" -Method Post -Body $sandboxBody -ContentType "application/json" -TimeoutSec 15
    Write-Host "  OK: Sandbox created" -ForegroundColor Green
    $sbResp | ConvertTo-Json -Depth 3 | ForEach-Object { Write-Host "  $_" }
} catch {
    Write-Host "  FAIL: Failed to create sandbox: $_" -ForegroundColor Red
    Write-Host "  Response: $($_.Exception.Response.StatusCode)"
    exit 1
}

# Step 3: Start JupyterLab via DevTool API
Write-Host ""
Write-Host "[3/6] Starting JupyterLab via DevTool API..."
$devtoolBody = @{
    sandboxId = "jupyter-test-sb"
    workingDir = $workspaceDir
    config = @{
        type = "jupyter"
        enabled = $true
        auth = @{
            allowNone = $true
        }
    }
} | ConvertTo-Json -Depth 4

try {
    $dtResp = Invoke-RestMethod -Uri "$baseUrl/v1/devtools" -Method Post -Body $devtoolBody -ContentType "application/json" -TimeoutSec 35
    Write-Host "  OK: JupyterLab dev tool started" -ForegroundColor Green
    Write-Host "  Instance ID: $($dtResp.id)"
    Write-Host "  Port: $($dtResp.port)"
    Write-Host "  PID: $($dtResp.pid)"
    Write-Host "  Status: $($dtResp.status)"
    $instanceId = $dtResp.id
    $jupyterPort = $dtResp.port
} catch {
    Write-Host "  FAIL: Failed to start JupyterLab: $_" -ForegroundColor Red
    Write-Host "  Response: $($_.Exception.Response.StatusCode)"
    exit 1
}

# Step 4: Wait for JupyterLab to be ready
Write-Host ""
Write-Host "[4/6] Waiting for JupyterLab to become ready..."
$maxWait = 40
$waited = 0
$ready = $false
while ($waited -lt $maxWait) {
    Start-Sleep -Seconds 2
    $waited += 2
    try {
        $healthResp = Invoke-RestMethod -Uri "$baseUrl/v1/devtools/$instanceId/health" -Method Get -TimeoutSec 5
        if ($healthResp.healthy) {
            Write-Host "  OK: JupyterLab is healthy (waited ${waited}s)" -ForegroundColor Green
            $ready = $true
            break
        }
        Write-Host "  ... waiting (${waited}s, status: not yet healthy)"
    } catch {
        Write-Host "  ... waiting (${waited}s, health check pending)"
    }
}

if (-not $ready) {
    Write-Host "  WARN: JupyterLab not fully healthy after ${maxWait}s, trying proxy anyway..." -ForegroundColor Yellow
}

# Step 5: Test proxy access
Write-Host ""
Write-Host "[5/6] Testing JupyterLab access through DevTool proxy..."
$proxyUrl = "$baseUrl/v1/devtools/proxy/jupyter/jupyter-test-sb/"
Write-Host "  Proxy URL: $proxyUrl"

try {
    $proxyResp = Invoke-WebRequest -Uri $proxyUrl -Method Get -TimeoutSec 10 -UseBasicParsing
    Write-Host "  HTTP Status: $($proxyResp.StatusCode)" -ForegroundColor Green
    $contentLen = $proxyResp.Content.Length
    Write-Host "  Content Length: $contentLen bytes"

    # Check if response contains JupyterLab markers
    if ($proxyResp.Content -match "Jupyter|jupyter-config|notebook") {
        Write-Host "  OK: Response contains JupyterLab content!" -ForegroundColor Green
    } else {
        Write-Host "  WARN: Response does not contain expected JupyterLab markers" -ForegroundColor Yellow
        Write-Host "  First 200 chars: $($proxyResp.Content.Substring(0, [Math]::Min(200, $contentLen)))"
    }
} catch {
    Write-Host "  FAIL: Proxy request failed: $_" -ForegroundColor Red
    Write-Host "  Trying direct port access to verify Jupyter is running..."
    try {
        $directResp = Invoke-WebRequest -Uri "http://127.0.0.1:$jupyterPort/" -Method Get -TimeoutSec 5 -UseBasicParsing
        Write-Host "  Direct access works (status: $($directResp.StatusCode)) - proxy may need debugging" -ForegroundColor Yellow
    } catch {
        Write-Host "  Direct access also failed: $_" -ForegroundColor Red
    }
}

# Step 6: List dev tools and cleanup
Write-Host ""
Write-Host "[6/6] Listing all dev tool instances..."
try {
    $listResp = Invoke-RestMethod -Uri "$baseUrl/v1/devtools" -Method Get -TimeoutSec 5
    Write-Host "  Found $($listResp.Count) dev tool instance(s):"
    foreach ($inst in $listResp) {
        Write-Host "    - ID: $($inst.id), Type: $($inst.type), Port: $($inst.port), Status: $($inst.status)"
    }
} catch {
    Write-Host "  FAIL: Failed to list dev tools: $_" -ForegroundColor Red
}

# Cleanup: stop the dev tool
Write-Host ""
Write-Host "Cleaning up: stopping JupyterLab instance..."
try {
    $stopResp = Invoke-RestMethod -Uri "$baseUrl/v1/devtools/$instanceId" -Method Delete -TimeoutSec 10
    Write-Host "  OK: Dev tool stopped" -ForegroundColor Green
} catch {
    Write-Host "  WARN: Failed to stop dev tool via API: $_" -ForegroundColor Yellow
}

# Cleanup: delete sandbox
Write-Host "Cleaning up: deleting sandbox..."
try {
    Invoke-RestMethod -Uri "$baseUrl/v1/sandboxes/jupyter-test-sb" -Method Delete -TimeoutSec 10 | Out-Null
    Write-Host "  OK: Sandbox deleted" -ForegroundColor Green
} catch {
    Write-Host "  WARN: Failed to delete sandbox: $_" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "============================================================"
Write-Host "  Test Complete"
Write-Host "============================================================"
