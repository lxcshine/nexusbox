# Simple network test for sandbox
Write-Host "=== Network Isolation Test ==="
Write-Host "Working directory: $(Get-Location)"
Write-Host ""

$httpOk = $false
$httpsOk = $false

# Test HTTP
Write-Host "Testing HTTP (example.com:80)..."
try {
    $r = Invoke-WebRequest -Uri "http://example.com" -TimeoutSec 5 -UseBasicParsing -ErrorAction Stop
    Write-Host "HTTP SUCCESS: Status $($r.StatusCode)"
    $httpOk = $true
} catch {
    Write-Host "HTTP BLOCKED: $($_.Exception.Message)"
}

# Test HTTPS
Write-Host "Testing HTTPS (github.com:443)..."
try {
    $r = Invoke-WebRequest -Uri "https://github.com" -TimeoutSec 5 -UseBasicParsing -ErrorAction Stop
    Write-Host "HTTPS SUCCESS: Status $($r.StatusCode)"
    $httpsOk = $true
} catch {
    Write-Host "HTTPS BLOCKED: $($_.Exception.Message)"
}

Write-Host ""
Write-Host "=== Results ==="
Write-Host "HTTP accessible: $httpOk"
Write-Host "HTTPS accessible: $httpsOk"

# Write results
@"
{
  "http_accessible": $($httpOk.ToString().ToLower()),
  "https_accessible": $($httpsOk.ToString().ToLower()),
  "timestamp": "$(Get-Date -Format 'o')"
}
"@ | Out-File -FilePath "results.json" -Encoding ASCII

if ($httpOk -or $httpsOk) {
    Write-Host "RESULT: NETWORK NOT BLOCKED" -ForegroundColor Red
    exit 1
} else {
    Write-Host "RESULT: NETWORK BLOCKED - isolation working!" -ForegroundColor Green
    exit 0
}
