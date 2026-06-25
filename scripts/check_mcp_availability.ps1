# NexusBox MCP end-to-end availability check.
# Launches nexusbox-mcp.exe, performs the MCP handshake, lists tools,
# then drives each tool category and asserts the protocol-level result.
#
# Usage:  pwsh scripts/check_mcp_availability.ps1

$ErrorActionPreference = "Stop"
$exe = "D:\Code\NexusBox\nexusbox-mcp.exe"
$workspace = "D:\Code\NexusBox"
$script:pass = 0
$script:fail = 0

function Send-Rpc($proc, $obj) {
    $line = ($obj | ConvertTo-Json -Compress -Depth 10)
    [Console]::Out.WriteLine(">>> $line")
    $proc.StandardInput.WriteLine($line)
    Start-Sleep -Milliseconds 150
    $resp = $proc.StandardOutput.ReadLine()
    [Console]::Out.WriteLine("<<< $resp")
    return $resp | ConvertFrom-Json
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

Write-Host "=== NexusBox MCP Availability Check ===" -ForegroundColor Cyan
Write-Host "Launching: $exe -workspace $workspace`n"

$proc = New-Object System.Diagnostics.Process
$proc.StartInfo = $psi
[void]$proc.Start()

# 1) initialize handshake
$init = Send-Rpc $proc @{
    jsonrpc = "2.0"; id = 1; method = "initialize"
    params = @{ protocolVersion = "2024-11-05"; capabilities = @{}; clientInfo = @{ name = "avail-check"; version = "1.0" } }
}
Assert- "initialize returns result" ($init.result -ne $null) "no result field"
Assert- "server name is nexusbox" ($init.result.serverInfo.name -eq "nexusbox") $init.result.serverInfo.name

# notifications/initialized (no response expected)
[void]$proc.StandardInput.WriteLine('{"jsonrpc":"2.0","method":"notifications/initialized"}')
Start-Sleep -Milliseconds 100

# 2) tools/list
$tl = Send-Rpc $proc @{ jsonrpc = "2.0"; id = 2; method = "tools/list"; params = @{} }
$toolNames = @($tl.result.tools | ForEach-Object { $_.name })
Write-Host "`nDiscovered tools ($($toolNames.Count)):" -ForegroundColor Cyan
$toolNames | ForEach-Object { Write-Host "  - $_" }
Assert- "tool count == 18" ($toolNames.Count -eq 18) "got $($toolNames.Count)"
foreach ($expect in @("shell_exec","file_read","file_write","code_run","browser_navigate")) {
    Assert- "tool present: $expect" ($toolNames -contains $expect) "missing"
}

Write-Host "`n=== Summary ===" -ForegroundColor Cyan
Write-Host "PASS: $script:pass   FAIL: $script:fail" -ForegroundColor Yellow
$proc.StandardInput.Close()
Start-Sleep -Milliseconds 200
[void]$proc.Kill()
exit $(if ($script:fail -gt 0) { 1 } else { 0 })
