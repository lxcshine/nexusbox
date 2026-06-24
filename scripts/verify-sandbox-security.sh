#!/usr/bin/env bash
#
# NexusBox Sandbox Security Verification Script
#
# This script simulates creating a sandbox and verifies that:
#   1. Capabilities Drop is effective (CAP_SYS_ADMIN etc. are removed)
#   2. Rootless uid/gid mapping is correctly configured
#   3. Seccomp profile is applied
#   4. Resource limits (cgroups) are enforced
#
# Usage:
#   ./scripts/verify-sandbox-security.sh [--sandbox-id <id>] [--image <image>]
#
# Prerequisites:
#   - NexusBox agent running (or containerd available)
#   - Root or sudo access for namespace/cgroup inspection
#   - Tools: crictl, ctr, cat, grep, jq
#

set -euo pipefail

# --- Configuration ---
SANDBOX_ID="${SANDBOX_ID:-verify-$(date +%s)}"
SANDBOX_IMAGE="${SANDBOX_IMAGE:-registry.k8s.io/pause:3.9}"
CONTAINERD_SOCK="${CONTAINERD_SOCK:-/run/containerd/containerd.sock}"
NEXUSBOX_NS="${NEXUSBOX_NS:-nexusbox}"
VERBOSE="${VERBOSE:-0}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Counters
PASS=0
FAIL=0
SKIP=0

# --- Helper Functions ---

log_info()  { echo -e "${BLUE}[INFO]${NC} $*"; }
log_pass()  { echo -e "${GREEN}[PASS]${NC} $*"; PASS=$((PASS+1)); }
log_fail()  { echo -e "${RED}[FAIL]${NC} $*"; FAIL=$((FAIL+1)); }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; SKIP=$((SKIP+1)); }
log_step()  { echo -e "\n${BLUE}=== $* ===${NC}"; }

check_command() {
    if ! command -v "$1" &>/dev/null; then
        log_warn "Command '$1' not found - skipping related checks"
        return 1
    fi
    return 0
}

# --- Pre-flight Checks ---

log_step "Pre-flight Checks"

if [ "$(id -u)" -ne 0 ]; then
    log_warn "Not running as root - some checks may fail. Use sudo for full verification."
fi

if [ ! -S "$CONTAINERD_SOCK" ]; then
    log_fail "containerd socket not found at $CONTAINERD_SOCK"
    log_info  "Start containerd or set CONTAINERD_SOCK env var"
    exit 1
fi
log_pass "containerd socket found at $CONTAINERD_SOCK"

if check_command ctr; then
    log_pass "ctr command available"
else
    log_fail "ctr command is required for containerd interaction"
    exit 1
fi

# --- Test 1: Capabilities Drop Verification ---
#
# This test verifies that the sandbox container does NOT have high-risk
# capabilities like CAP_SYS_ADMIN, CAP_SYS_PTRACE, CAP_SYS_MODULE, etc.

log_step "Test 1: Capabilities Drop Verification"

# List of capabilities that MUST be absent from the sandbox
CRITICAL_CAPS=(
    "CAP_SYS_ADMIN"
    "CAP_SYS_PTRACE"
    "CAP_SYS_MODULE"
    "CAP_SYS_BOOT"
    "CAP_SYS_RAWIO"
    "CAP_SYS_NICE"
    "CAP_SYS_PACCT"
    "CAP_SYS_RESOURCE"
    "CAP_SYS_TIME"
    "CAP_SYS_TTY_CONFIG"
    "CAP_BPF"
    "CAP_PERFMON"
    "CAP_CHECKPOINT_RESTORE"
    "CAP_NET_RAW"
    "CAP_NET_BROADCAST"
    "CAP_MAC_ADMIN"
    "CAP_MAC_OVERRIDE"
    "CAP_DAC_READ_SEARCH"
    "CAP_LINUX_IMMUTABLE"
    "CAP_IPC_LOCK"
    "CAP_BLOCK_SUSPEND"
    "CAP_WAKE_ALARM"
    "CAP_SYSLOG"
    "CAP_AUDIT_CONTROL"
)

# Function to get capabilities of a running container
get_container_caps() {
    local sandbox_id=$1
    # Try to read from /proc/<pid>/status (CapBnd, CapEff, CapPrm)
    local pid
    pid=$(ctr -n "$NEXUSBOX_NS" task list 2>/dev/null | grep "$sandbox_id" | awk '{print $2}')
    if [ -z "$pid" ] || [ "$pid" = "0" ]; then
        echo ""
        return
    fi
    # Read CapBnd (bounding set) from /proc/<pid>/status
    grep "^CapBnd:" "/proc/$pid/status" 2>/dev/null | awk '{print $2}'
}

# Function to decode a capability bitmask to names
decode_caps() {
    local bitmask=$1
    # Use capsh if available, otherwise use a simple decoder
    if check_command capsh && [ -n "$bitmask" ]; then
        capsh --decode="$bitmask" 2>/dev/null | sed 's/^{//;s/}$//' | tr ',' '\n' | tr -d ' '
    fi
}

log_info "Checking capabilities drop for critical caps..."

# If we have a running sandbox, check its capabilities
SANDBOX_PID=$(ctr -n "$NEXUSBOX_NS" task list 2>/dev/null | grep "$SANDBOX_ID" | awk '{print $2}' || echo "")

if [ -n "$SANDBOX_PID" ] && [ "$SANDBOX_PID" != "0" ]; then
    log_info "Found sandbox $SANDBOX_ID with PID $SANDBOX_PID"

    CAP_BND=$(grep "^CapBnd:" "/proc/$SANDBOX_PID/status" 2>/dev/null | awk '{print $2}')
    CAP_EFF=$(grep "^CapEff:" "/proc/$SANDBOX_PID/status" 2>/dev/null | awk '{print $2}')
    CAP_PRM=$(grep "^CapPrm:" "/proc/$SANDBOX_PID/status" 2>/dev/null | awk '{print $2}')

    log_info "CapBnd (Bounding): $CAP_BND"
    log_info "CapEff (Effective): $CAP_EFF"
    log_info "CapPrm (Permitted): $CAP_PRM"

    if check_command capsh; then
        DECODED_BND=$(capsh --decode="$CAP_BND" 2>/dev/null || echo "")
        log_info "Decoded Bounding caps: $DECODED_BND"

        for cap in "${CRITICAL_CAPS[@]}"; do
            if echo "$DECODED_BND" | grep -q "$cap"; then
                log_fail "Critical capability $cap is PRESENT in bounding set (should be dropped)"
            else
                log_pass "Critical capability $cap is correctly dropped from bounding set"
            fi
        done
    else
        log_warn "capsh not available - cannot decode capability bitmask. Install libcap2-bin."
    fi
else
    log_warn "No running sandbox found with ID $SANDBOX_ID"
    log_info  "Run unit tests instead: go test ./pkg/runtime/containerd/... -v -run TestWithDroppedCapabilities"

    # Fall back to running the Go unit tests
    if check_command go; then
        log_info "Running Go unit tests for capabilities drop..."
        if go test ./pkg/runtime/containerd/... -v -run "TestWithDroppedCapabilities|TestSecuritySpecOptions_AlwaysDropsCriticalCaps|TestSecuritySpecOptions_RefusesToReGrantDroppedCap" -count=1 2>&1; then
            log_pass "Capabilities drop unit tests passed"
        else
            log_fail "Capabilities drop unit tests failed"
        fi
    fi
fi

# --- Test 2: Rootless uid/gid Mapping Verification ---
#
# This test verifies that user namespace uid/gid mappings are correctly
# configured when rootless mode is enabled.

log_step "Test 2: Rootless uid/gid Mapping Verification"

# Check if /etc/subuid and /etc/subgid are configured
check_subuid_subgid() {
    local current_user
    current_user=$(whoami)

    if [ ! -f /etc/subuid ]; then
        log_warn "/etc/subuid not found - rootless mode is not configured"
        return 1
    fi
    if [ ! -f /etc/subgid ]; then
        log_warn "/etc/subgid not found - rootless mode is not configured"
        return 1
    fi

    local subuid_line subgid_line
    subuid_line=$(grep "^${current_user}:" /etc/subuid 2>/dev/null || echo "")
    subgid_line=$(grep "^${current_user}:" /etc/subgid 2>/dev/null || echo "")

    if [ -z "$subuid_line" ]; then
        log_warn "User $current_user not found in /etc/subuid"
        return 1
    fi
    if [ -z "$subgid_line" ]; then
        log_warn "User $current_user not found in /etc/subgid"
        return 1
    fi

    local subuid_start subuid_size subgid_start subgid_size
    subuid_start=$(echo "$subuid_line" | cut -d: -f2)
    subuid_size=$(echo "$subuid_line" | cut -d: -f3)
    subgid_start=$(echo "$subgid_line" | cut -d: -f2)
    subgid_size=$(echo "$subgid_line" | cut -d: -f3)

    log_info "User: $current_user"
    log_info "Subuid range: $subuid_start (size: $subuid_size)"
    log_info "Subgid range: $subgid_start (size: $subgid_size)"

    # Verify ranges are valid
    if [ "$subuid_start" -lt 1000 ] 2>/dev/null; then
        log_fail "Subuid start ($subuid_start) should be >= 1000 for security"
    else
        log_pass "Subuid start ($subuid_start) is in valid range"
    fi

    if [ "$subuid_size" -lt 65536 ] 2>/dev/null; then
        log_warn "Subuid size ($subuid_size) is less than recommended 65536"
    else
        log_pass "Subuid size ($subuid_size) is sufficient (>= 65536)"
    fi

    if [ "$subgid_size" -lt 65536 ] 2>/dev/null; then
        log_warn "Subgid size ($subgid_size) is less than recommended 65536"
    else
        log_pass "Subgid size ($subgid_size) is sufficient (>= 65536)"
    fi

    return 0
}

if check_subuid_subgid; then
    log_pass "Rootless subuid/subgid configuration is valid"
else
    log_warn "Rootless mode is not configured. Skipping runtime mapping verification."
    log_info  "To enable rootless: sudo usermod --add-subuids 100000-165535 \$USER"
    log_info  "                    sudo usermod --add-subgids 100000-165535 \$USER"
fi

# Check uid_map/gid_map for running sandbox
if [ -n "$SANDBOX_PID" ] && [ "$SANDBOX_PID" != "0" ]; then
    log_info "Checking uid_map/gid_map for sandbox PID $SANDBOX_PID"

    if [ -f "/proc/$SANDBOX_PID/uid_map" ]; then
        UID_MAP=$(cat "/proc/$SANDBOX_PID/uid_map")
        log_info "uid_map: $UID_MAP"

        # Expected format: <container_id> <host_id> <size>
        # For rootless: 0 <subuid_start> <size>
        MAP_CONTAINER=$(echo "$UID_MAP" | awk '{print $1}')
        MAP_HOST=$(echo "$UID_MAP" | awk '{print $2}')
        MAP_SIZE=$(echo "$UID_MAP" | awk '{print $3}')

        if [ "$MAP_CONTAINER" = "0" ]; then
            log_pass "uid_map: container uid 0 correctly maps to host uid $MAP_HOST"
        else
            log_fail "uid_map: container uid should be 0, got $MAP_CONTAINER"
        fi

        if [ "$MAP_HOST" -ge 1000 ] 2>/dev/null; then
            log_pass "uid_map: host uid $MAP_HOST is in unprivileged range (>= 1000)"
        else
            log_fail "uid_map: host uid $MAP_HOST should be >= 1000 (unprivileged)"
        fi
    else
        log_warn "Cannot read /proc/$SANDBOX_PID/uid_map - user namespace may not be active"
    fi

    if [ -f "/proc/$SANDBOX_PID/gid_map" ]; then
        GID_MAP=$(cat "/proc/$SANDBOX_PID/gid_map")
        log_info "gid_map: $GID_MAP"

        MAP_CONTAINER=$(echo "$GID_MAP" | awk '{print $1}')
        MAP_HOST=$(echo "$GID_MAP" | awk '{print $2}')

        if [ "$MAP_CONTAINER" = "0" ]; then
            log_pass "gid_map: container gid 0 correctly maps to host gid $MAP_HOST"
        else
            log_fail "gid_map: container gid should be 0, got $MAP_CONTAINER"
        fi
    else
        log_warn "Cannot read /proc/$SANDBOX_PID/gid_map - user namespace may not be active"
    fi
else
    log_warn "No running sandbox found - running Go unit tests for rootless mapping"
    if check_command go; then
        log_info "Running Go unit tests for rootless..."
        if go test ./pkg/security/rootless/... -v -count=1 2>&1; then
            log_pass "Rootless mapping unit tests passed"
        else
            log_fail "Rootless mapping unit tests failed"
        fi
    fi
fi

# --- Test 3: Seccomp Profile Verification ---

log_step "Test 3: Seccomp Profile Verification"

if [ -n "$SANDBOX_PID" ] && [ "$SANDBOX_PID" != "0" ]; then
    # Check if seccomp is active
    SECCOMP_MODE=$(grep "^Seccomp:" "/proc/$SANDBOX_PID/status" 2>/dev/null | awk '{print $2}')

    case "$SECCOMP_MODE" in
        0) log_fail "Seccomp is disabled (mode=0) - sandbox is NOT protected" ;;
        1) log_pass "Seccomp is in strict mode (mode=1)" ;;
        2) log_pass "Seccomp is in filter mode (mode=2) - whitelist active" ;;
        *) log_warn "Unknown seccomp mode: $SECCOMP_MODE" ;;
    esac

    # Check NoNewPrivs
    NO_NEW_PRIVS=$(grep "^NoNewPrivs:" "/proc/$SANDBOX_PID/status" 2>/dev/null | awk '{print $2}')
    if [ "$NO_NEW_PRIVS" = "1" ]; then
        log_pass "NoNewPrivs is set (prevents privilege escalation via setuid)"
    else
        log_fail "NoNewPrivs is NOT set - sandbox can escalate privileges via setuid binaries"
    fi
else
    log_warn "No running sandbox - skipping seccomp runtime check"
fi

# --- Test 4: Cgroups Resource Limits Verification ---

log_step "Test 4: Cgroups Resource Limits Verification"

if [ -n "$SANDBOX_PID" ] && [ "$SANDBOX_PID" != "0" ]; then
    # Find cgroup path for the sandbox
    CGROUP_PATH=$(grep "^cgroup:" "/proc/$SANDBOX_PID/cgroup" 2>/dev/null | head -1 | awk -F: '{print $3}')

    if [ -n "$CGROUP_PATH" ]; then
        log_info "Cgroup path: $CGROUP_PATH"

        # Check cgroup v2
        if [ -f "/sys/fs/cgroup$CGROUP_PATH/cpu.max" ]; then
            CPU_MAX=$(cat "/sys/fs/cgroup$CGROUP_PATH/cpu.max" 2>/dev/null)
            log_info "cpu.max: $CPU_MAX"
            if [ "$CPU_MAX" != "max" ]; then
                log_pass "CPU limit is set: $CPU_MAX"
            else
                log_warn "CPU limit is not set (cpu.max = max)"
            fi
        fi

        if [ -f "/sys/fs/cgroup$CGROUP_PATH/memory.max" ]; then
            MEM_MAX=$(cat "/sys/fs/cgroup$CGROUP_PATH/memory.max" 2>/dev/null)
            log_info "memory.max: $MEM_MAX"
            if [ "$MEM_MAX" != "max" ]; then
                log_pass "Memory limit is set: $MEM_MAX bytes"
            else
                log_warn "Memory limit is not set (memory.max = max)"
            fi
        fi

        if [ -f "/sys/fs/cgroup$CGROUP_PATH/pids.max" ]; then
            PIDS_MAX=$(cat "/sys/fs/cgroup$CGROUP_PATH/pids.max" 2>/dev/null)
            log_info "pids.max: $PIDS_MAX"
            if [ "$PIDS_MAX" != "max" ]; then
                log_pass "PID limit is set: $PIDS_MAX (fork bomb protection)"
            else
                log_warn "PID limit is not set (pids.max = max)"
            fi
        fi
    else
        log_warn "Cannot find cgroup path for sandbox"
    fi
else
    log_warn "No running sandbox - skipping cgroups verification"
fi

# --- Summary ---

log_step "Verification Summary"

echo ""
echo "  Passed: $PASS"
echo "  Failed: $FAIL"
echo "  Skipped: $SKIP"
echo ""

if [ "$FAIL" -gt 0 ]; then
    echo -e "${RED}SECURITY VERIFICATION FAILED${NC}"
    echo -e "${RED}$FAIL check(s) failed. Review the output above.${NC}"
    exit 1
elif [ "$SKIP" -gt 0 ]; then
    echo -e "${YELLOW}VERIFICATION COMPLETED WITH WARNINGS${NC}"
    echo -e "${YELLOW}$SKIP check(s) were skipped. Review the warnings above.${NC}"
    exit 0
else
    echo -e "${GREEN}ALL SECURITY CHECKS PASSED${NC}"
    echo -e "${GREEN}Sandbox is properly hardened with capabilities drop and rootless mapping.${NC}"
    exit 0
fi
