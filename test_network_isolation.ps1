# NexusBox 沙箱网络隔离测试脚本
# 模拟恶意代码的各种网络访问行为

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "NexusBox Sandbox Network Isolation Test" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

$results = @()

# Test 1: HTTP outbound request to public internet
Write-Host "[Test 1] HTTP Request to example.com (port 80)..." -ForegroundColor Yellow
try {
    $response = Invoke-WebRequest -Uri "http://example.com" -TimeoutSec 5 -UseBasicParsing
    Write-Host "[Result] HTTP Request SUCCEEDED - Status: $($response.StatusCode)" -ForegroundColor Red
    $results += [PSCustomObject]@{Test="HTTP example.com:80"; Result="UNBLOCKED"; StatusCode=$response.StatusCode}
} catch {
    Write-Host "[Result] HTTP Request BLOCKED - Error: $($_.Exception.Message)" -ForegroundColor Green
    $results += [PSCustomObject]@{Test="HTTP example.com:80"; Result="BLOCKED"; Error=$_.Exception.Message}
}
Write-Host ""

# Test 2: HTTPS outbound request
Write-Host "[Test 2] HTTPS Request to github.com (port 443)..." -ForegroundColor Yellow
try {
    $response = Invoke-WebRequest -Uri "https://github.com" -TimeoutSec 5 -UseBasicParsing
    Write-Host "[Result] HTTPS Request SUCCEEDED - Status: $($response.StatusCode)" -ForegroundColor Red
    $results += [PSCustomObject]@{Test="HTTPS github.com:443"; Result="UNBLOCKED"; StatusCode=$response.StatusCode}
} catch {
    Write-Host "[Result] HTTPS Request BLOCKED - Error: $($_.Exception.Message)" -ForegroundColor Green
    $results += [PSCustomObject]@{Test="HTTPS github.com:443"; Result="BLOCKED"; Error=$_.Exception.Message}
}
Write-Host ""

# Test 3: TCP connection to external IP
Write-Host "[Test 3] TCP Connection to 1.1.1.1 (port 53 - DNS)..." -ForegroundColor Yellow
try {
    $tcp = New-Object System.Net.Sockets.TcpClient
    $connect = $tcp.BeginConnect("1.1.1.1", 53, $null, $null)
    $wait = $connect.AsyncWaitHandle.WaitOne(5000, $false)
    if ($wait -and $tcp.Connected) {
        Write-Host "[Result] TCP Connection SUCCEEDED" -ForegroundColor Red
        $results += [PSCustomObject]@{Test="TCP 1.1.1.1:53"; Result="UNBLOCKED"}
        $tcp.Close()
    } else {
        Write-Host "[Result] TCP Connection BLOCKED (timeout)" -ForegroundColor Green
        $results += [PSCustomObject]@{Test="TCP 1.1.1.1:53"; Result="BLOCKED"; Error="Timeout"}
    }
} catch {
    Write-Host "[Result] TCP Connection BLOCKED - Error: $($_.Exception.Message)" -ForegroundColor Green
    $results += [PSCustomObject]@{Test="TCP 1.1.1.1:53"; Result="BLOCKED"; Error=$_.Exception.Message}
}
Write-Host ""

# Test 4: DNS lookup test
Write-Host "[Test 4] DNS Lookup for malicious-domain.test..." -ForegroundColor Yellow
try {
    $dns = Resolve-DnsName -Name "example.com" -Type A -ErrorAction Stop -Timeout 5
    Write-Host "[Result] DNS Lookup SUCCEEDED - IP: $($dns.IPAddress -join ', ')" -ForegroundColor Red
    $results += [PSCustomObject]@{Test="DNS example.com"; Result="UNBLOCKED"; IP=$dns.IPAddress -join ', '}
} catch {
    Write-Host "[Result] DNS Lookup BLOCKED - Error: $($_.Exception.Message)" -ForegroundColor Green
    $results += [PSCustomObject]@{Test="DNS example.com"; Result="BLOCKED"; Error=$_.Exception.Message}
}
Write-Host ""

# Test 5: Ping test (ICMP)
Write-Host "[Test 5] ICMP Ping to 8.8.8.8..." -ForegroundColor Yellow
try {
    $ping = Test-Connection -ComputerName 8.8.8.8 -Count 2 -TimeoutSeconds 3 -ErrorAction Stop
    Write-Host "[Result] Ping SUCCEEDED - Response time: $($ping.ResponseTime)ms" -ForegroundColor Red
    $results += [PSCustomObject]@{Test="ICMP 8.8.8.8"; Result="UNBLOCKED"; ResponseTime=$ping.ResponseTime -join ', '}
} catch {
    Write-Host "[Result] Ping BLOCKED - Error: $($_.Exception.Message)" -ForegroundColor Green
    $results += [PSCustomObject]@{Test="ICMP 8.8.8.8"; Result="BLOCKED"; Error=$_.Exception.Message}
}
Write-Host ""

# Test 6: Check if we can list firewall rules (information gathering)
Write-Host "[Test 6] Information Gathering - List Firewall Rules..." -ForegroundColor Yellow
try {
    $rules = netsh advfirewall firewall show rule name=all 2>&1 | Select-Object -First 5
    Write-Host "[Result] Firewall enumeration may be accessible" -ForegroundColor DarkYellow
    $results += [PSCustomObject]@{Test="Firewall Enumeration"; Result="MAY WORK"; Note="Firewall blocks by program path, not visibility"}
} catch {
    $results += [PSCustomObject]@{Test="Firewall Enumeration"; Result="BLOCKED"; Error=$_.Exception.Message}
}
Write-Host ""

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Test Summary" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
$results | Format-Table -AutoSize

# Write results to file in working directory
$results | ConvertTo-Json | Out-File -FilePath "network_test_results.json" -Encoding UTF8
Write-Host "Results saved to network_test_results.json" -ForegroundColor Gray

# Final verdict
$unblocked = ($results | Where-Object { $_.Result -eq "UNBLOCKED" }).Count
if ($unblocked -gt 0) {
    Write-Host ""
    Write-Host "WARNING: $unblocked network tests succeeded - network may not be fully blocked!" -ForegroundColor Red
    exit 1
} else {
    Write-Host ""
    Write-Host "SUCCESS: All network access is BLOCKED - sandbox network isolation is working!" -ForegroundColor Green
    exit 0
}
