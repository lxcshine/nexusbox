#!/bin/bash
# ============================================================
# NexusBox Sandbox - Entrypoint Script
# ============================================================
# This script runs before any service starts. It validates
# environment variables, checks dependencies, and prints
# diagnostic information to help troubleshoot runtime issues.
# ============================================================

set -e

# Color codes for log output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info()  { echo -e "${GREEN}[INFO]${NC}  $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_debug() { echo -e "${BLUE}[DEBUG]${NC} $(date '+%Y-%m-%d %H:%M:%S') $*"; }

echo ""
echo "============================================================"
echo "  NexusBox Sandbox - Starting Up"
echo "  Version: 0.1.0"
echo "  Time: $(date -u '+%Y-%m-%d %H:%M:%S UTC')"
echo "============================================================"
echo ""

# ---- 1. Environment Variables ----
log_info "=== Environment Variables ==="

: "${WORKSPACE:=/home/sandbox}"
: "${PUBLIC_PORT:=8080}"
: "${MCP_HUB_PORT:=8079}"
: "${WEBSOCKET_PROXY_PORT:=6080}"
: "${JUPYTER_LAB_PORT:=8888}"
: "${CODE_SERVER_PORT:=8200}"
: "${BROWSER_REMOTE_DEBUGGING_PORT:=9222}"
: "${VNC_SERVER_PORT:=5900}"

log_info "WORKSPACE                    = ${WORKSPACE}"
log_info "PUBLIC_PORT                  = ${PUBLIC_PORT}"
log_info "MCP_HUB_PORT                 = ${MCP_HUB_PORT}"
log_info "WEBSOCKET_PROXY_PORT         = ${WEBSOCKET_PROXY_PORT}"
log_info "JUPYTER_LAB_PORT             = ${JUPYTER_LAB_PORT}"
log_info "CODE_SERVER_PORT             = ${CODE_SERVER_PORT}"
log_info "BROWSER_REMOTE_DEBUGGING_PORT= ${BROWSER_REMOTE_DEBUGGING_PORT}"
log_info "VNC_SERVER_PORT              = ${VNC_SERVER_PORT}"

if [ -n "${PROXY_SERVER}" ]; then
    log_info "PROXY_SERVER                 = ${PROXY_SERVER}"
else
    log_debug "PROXY_SERVER                 = (not set)"
fi

if [ -n "${JWT_PUBLIC_KEY}" ]; then
    log_info "JWT_PUBLIC_KEY               = (configured, ${#JWT_PUBLIC_KEY} bytes)"
else
    log_warn "JWT_PUBLIC_KEY               = (not set, auth disabled)"
fi

if [ -n "${DNS_OVER_HTTPS_TEMPLATES}" ]; then
    log_info "DNS_OVER_HTTPS_TEMPLATES     = ${DNS_OVER_HTTPS_TEMPLATES}"
fi

echo ""

# ---- 2. Binary Dependencies ----
log_info "=== Binary Dependencies ==="

check_binary() {
    local name="$1"
    local path
    path=$(command -v "$name" 2>/dev/null || true)
    if [ -n "$path" ]; then
        local version=""
        case "$name" in
            python3|python) version=$($name --version 2>&1 | head -1) ;;
            node) version=$($name --version 2>&1 | head -1) ;;
            go) version=$($name version 2>&1 | head -1) ;;
            chromium|chromium-browser) version=$($name --version 2>&1 | head -1) ;;
            jupyter) version=$($name --version 2>&1 | head -1) ;;
            code-server) version=$($name --version 2>&1 | head -1) ;;
            supervisord) version=$($name --version 2>&1 | head -1) ;;
            websockify) version=$($name --version 2>&1 | head -1) ;;
            curl) version=$($name --version 2>&1 | head -1) ;;
            *) version="" ;;
        esac
        log_info "  ${name}: OK (${path}) ${version}"
        return 0
    else
        log_warn "  ${name}: NOT FOUND"
        return 1
    fi
}

BINARIES="nexusbox-agent python3 node chromium jupyter code-server supervisord websockify curl"
for bin in $BINARIES; do
    check_binary "$bin" || true
done

echo ""

# ---- 3. Workspace Directory ----
log_info "=== Workspace ==="

if [ ! -d "${WORKSPACE}" ]; then
    log_warn "Workspace directory does not exist: ${WORKSPACE}"
    log_info "Creating workspace directory..."
    mkdir -p "${WORKSPACE}"
fi

log_info "Workspace: ${WORKSPACE}"
log_info "Workspace permissions: $(ls -ld "${WORKSPACE}" 2>/dev/null || echo 'N/A')"
log_info "Workspace owner: $(stat -c '%U:%G' "${WORKSPACE}" 2>/dev/null || echo 'N/A')"

echo ""

# ---- 4. Port Availability ----
log_info "=== Port Availability ==="

check_port() {
    local port="$1"
    local name="$2"
    if ss -tlnp 2>/dev/null | grep -q ":${port} " || \
       netstat -tlnp 2>/dev/null | grep -q ":${port} "; then
        log_warn "  Port ${port} (${name}): IN USE"
    else
        log_info "  Port ${port} (${name}): AVAILABLE"
    fi
}

check_port "${PUBLIC_PORT}" "Gateway API"
check_port "${MCP_HUB_PORT}" "MCP Hub"
check_port "${WEBSOCKET_PROXY_PORT}" "WebSocket Proxy"
check_port "${JUPYTER_LAB_PORT}" "JupyterLab"
check_port "${CODE_SERVER_PORT}" "Code Server"
check_port "${BROWSER_REMOTE_DEBUGGING_PORT}" "Chromium CDP"
check_port "${VNC_SERVER_PORT}" "VNC"

echo ""

# ---- 5. System Resources ----
log_info "=== System Resources ==="

if [ -f /proc/meminfo ]; then
    MEM_TOTAL=$(awk '/MemTotal/ {printf "%.0f MB", $2/1024}' /proc/meminfo)
    MEM_AVAIL=$(awk '/MemAvailable/ {printf "%.0f MB", $2/1024}' /proc/meminfo)
    log_info "Memory: Total=${MEM_TOTAL}, Available=${MEM_AVAIL}"
else
    log_debug "Cannot read /proc/meminfo"
fi

if [ -f /proc/cpuinfo ]; then
    CPU_COUNT=$(grep -c ^processor /proc/cpuinfo)
    log_info "CPUs: ${CPU_COUNT}"
fi

if command -v df >/dev/null 2>&1; then
    DISK_INFO=$(df -h "${WORKSPACE}" 2>/dev/null | tail -1 | awk '{print "Total="$2", Used="$3", Avail="$4", Use%="$5}')
    log_info "Disk (${WORKSPACE}): ${DISK_INFO}"
fi

# Check /dev/shm for Chromium
SHM_SIZE=$(df -h /dev/shm 2>/dev/null | tail -1 | awk '{print $2}')
if [ -n "${SHM_SIZE}" ]; then
    log_info "/dev/shm size: ${SHM_SIZE}"
    # Chromium needs at least 64MB in /dev/shm
    SHM_KB=$(df -k /dev/shm 2>/dev/null | tail -1 | awk '{print $2}')
    if [ -n "${SHM_KB}" ] && [ "${SHM_KB}" -lt 65536 ]; then
        log_warn "/dev/shm is too small for Chromium (${SHM_SIZE}). Consider increasing with --shm-size"
    fi
fi

echo ""

# ---- 6. Network Configuration ----
log_info "=== Network Configuration ==="

if [ -f /etc/resolv.conf ]; then
    log_debug "DNS servers: $(grep nameserver /etc/resolv.conf 2>/dev/null | awk '{print $2}' | tr '\n' ', ')"
fi

if command -v ip >/dev/null 2>&1; then
    log_debug "Container IP: $(ip -4 addr show eth0 2>/dev/null | grep -oP '(?<=inet\s)\d+(\.\d+){3}' || echo 'N/A')"
fi

if [ -n "${HOSTNAME}" ]; then
    log_info "Hostname: ${HOSTNAME}"
fi

echo ""

# ---- 7. Security Configuration ----
log_info "=== Security Configuration ==="

if [ -f /proc/self/status ]; then
    SECCOMP=$(grep Seccomp /proc/self/status 2>/dev/null | awk '{print $2}')
    case "${SECCOMP}" in
        0) log_info "Seccomp: Disabled" ;;
        1) log_info "Seccomp: Strict" ;;
        2) log_info "Seccomp: Filter" ;;
        *) log_debug "Seccomp: Unknown (${SECCOMP})" ;;
    esac
fi

echo ""

# ---- 8. Startup Summary ----
log_info "=== Startup Summary ==="
log_info "All pre-flight checks completed."
log_info "Supervisor will now start the following services:"
log_info "  1. VNC Server          (port ${VNC_SERVER_PORT})"
log_info "  2. Gateway API         (port ${PUBLIC_PORT})"
log_info "  3. WebSocket Proxy     (port ${WEBSOCKET_PROXY_PORT})"
log_info "  4. MCP Hub             (port ${MCP_HUB_PORT})"
log_info "  5. JupyterLab          (port ${JUPYTER_LAB_PORT})"
log_info "  6. Code Server         (port ${CODE_SERVER_PORT})"
log_info "  7. Chromium + CDP      (port ${BROWSER_REMOTE_DEBUGGING_PORT})"
echo ""
log_info "Health check: curl http://localhost:${PUBLIC_PORT}/healthz"
log_info "API endpoints: http://localhost:${PUBLIC_PORT}/v1/"
log_info "MCP endpoint:  http://localhost:${MCP_HUB_PORT}/mcp"
echo ""
echo "============================================================"
echo "  NexusBox Sandbox - Ready"
echo "============================================================"
echo ""
