# NexusBox MCP end-to-end tool-call availability check.
# Drives real tool calls through the stdio MCP transport and asserts
# each tool category works against the live sandbox.
#
# Usage:  pwsh scripts/check_mcp_tools.ps1

$ErrorActionPreference = "Stop"
$exe = "D:\Code\NexusBox\nexusbox-mcp.exe"
$workspace = "D:\Code\NexusBox"
$script:pass = 0
$script:fail = 0
$script:id = 100

function Next-Id() { $script:id++; return $script:id }

function Send-Rpc($proc, $obj) {
    $line = ($obj | ConvertTo-Json -Compress -Depth 10)
    $proc.StandardInput.WriteLine($line)
    $proc.StandardInput.Flush()
    # Blocking read of one stdout line (stdio protocol guarantees one
    # response per request). Skip empty keepalive lines.
    $l = $null
    do {
        $l = $proc.StandardOutput.ReadLine()
    } while ($l -and $l.Trim() -eq "")
    if (-not $l) { return $null }
    return $l | ConvertFrom-Json
}

function Assert-($name, $cond, $detail) {
    if ($cond) { Write-Host "  [PASS] $name" -ForegroundColor Green; $script:pass++ }
    else { Write-Host "  [FAIL] $name -- $detail" -ForegroundColor Red; $script:fail++ }
}

$psi = New-Object System.Diagnostics.ProcessStartInfo
$psi.FileName = $exe
$psi.Arguments = "-workspace `"$workspace`" -log-level error"
$psi.UseShellExecute = $false
$psi.RedirectStandardInput = $true
$psi.RedirectStandardOutput = $true
$psi.RedirectStandardError = $true
$psi.CreateNoWindow = $true

Write-Host "=== NexusBox MCP Tool-Call Availability Check ===" -ForegroundColor Cyan

$proc = New-Object System.Diagnostics.Process
$proc.StartInfo = $psi
[void]$proc.Start()

# --- handshake ---
[void]$proc.StandardInput.WriteLine('{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"avail","version":"1.0"}}}')
Start-Sleep -Milliseconds 200
while ($proc.StandardOutput.Peek() -gt 0) { [void]$proc.StandardOutput.ReadLine() }
[void]$proc.StandardInput.WriteLine('{"jsonrpc":"2.0","method":"notifications/initialized"}')
Start-Sleep -Milliseconds 100
Write-Host "[setup] handshake done" -ForegroundColor Cyan

# --- 1. shell_exec ---
Write-Host "`n[1] shell_exec" -ForegroundColor Cyan
$r = Send-Rpc $proc @{ jsonrpc="2.0"; id=(Next-Id); method="tools/call"; params=@{ name="shell_exec"; arguments=@{ command="echo nexusbox-shell-ok"; workDir="." } } }
$exitOk = $r.result.isError -eq $false -or $r.result.isError -eq $null
Assert- "shell_exec returns result" ($r.result -ne $null) ($r.error | ConvertTo-Json -Compress)
$out = ($r.result.content | Where-Object { $_.type -eq "text" } | Select-Object -First 1).text
Assert- "shell_exec output contains marker" ($out -match "nexusbox-shell-ok") "got: $out"

# --- 2. file_write + file_read ---
Write-Host "`n[2] file_write + file_read" -ForegroundColor Cyan
$testFile = "mcp-avail-test.txt"
$testContent = "hello-from-nexusbox-mcp-$(Get-Date -Format 'HHmmss')"
$r = Send-Rpc $proc @{ jsonrpc="2.0"; id=(Next-Id); method="tools/call"; params=@{ name="file_write"; arguments=@{ path=$testFile; content=$testContent } } }
Assert- "file_write returns result" ($r.result -ne $null) ($r.error | ConvertTo-Json -Compress)
$r = Send-Rpc $proc @{ jsonrpc="2.0"; id=(Next-Id); method="tools/call"; params=@{ name="file_read"; arguments=@{ path=$testFile } } }
$read = ($r.result.content | Where-Object { $_.type -eq "text" } | Select-Object -First 1).text
Assert- "file_read returns written content" ($read -eq $testContent) "got: $read"

# --- 3. code_run (multi-language) ---
Write-Host "`n[3] code_run (Python / Node / Go)" -ForegroundColor Cyan
foreach ($lang in @(
    @{ name="python"; code="print('py-ok')" },
    @{ name="nodejs"; code="console.log('node-ok')" },
    @{ name="go"; code="package main`nimport `"fmt`"`nfunc main(){ fmt.Println(`"go-ok`") }" },
    @{ name="java"; code="public class Hello { public static void main(String[] a){ System.out.println(`"java-ok`"); } }" }
)) {
    $r = Send-Rpc $proc @{ jsonrpc="2.0"; id=(Next-Id); method="tools/call"; params=@{ name="code_run"; arguments=@{ language=$lang.name; code=$lang.code } } }
    if ($r.error) {
        Assert- "code_run [$($lang.name)] executes" $false ($r.error.message)
    } else {
        $co = ($r.result.content | Where-Object { $_.type -eq "text" } | Select-Object -First 1).text
        if ($lang.name -eq "python") { $marker = "py-ok" }
        elseif ($lang.name -eq "nodejs") { $marker = "node-ok" }
        elseif ($lang.name -eq "go") { $marker = "go-ok" }
        else { $marker = "java-ok" }
        Assert- "code_run [$($lang.name)] output matches" ($co -match $marker) "got: $co"
    }
}

# --- 4. path traversal guard ---
Write-Host "`n[4] path traversal guard" -ForegroundColor Cyan
$r = Send-Rpc $proc @{ jsonrpc="2.0"; id=(Next-Id); method="tools/call"; params=@{ name="file_read"; arguments=@{ path="../../../etc/passwd" } } }
$blocked = ($r.result.isError -eq $true) -or ($r.error -ne $null) -or (($r.result.content | Where-Object { $_.type -eq "text" } | Select-Object -First 1).text -match "outside|denied|escape|traversal")
Assert- "path traversal blocked" $blocked "request was NOT blocked"

# --- cleanup test file ---
[void](Send-Rpc $proc @{ jsonrpc="2.0"; id=(Next-Id); method="tools/call"; params=@{ name="file_delete"; arguments=@{ path=$testFile } } })

Write-Host "`n=== Summary ===" -ForegroundColor Cyan
Write-Host "PASS: $script:pass   FAIL: $script:fail" -ForegroundColor Yellow
$proc.StandardInput.Close()
Start-Sleep -Milliseconds 300
try { [void]$proc.Kill() } catch {}
exit $(if ($script:fail -gt 0) { 1 } else { 0 })
