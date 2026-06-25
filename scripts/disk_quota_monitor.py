#!/usr/bin/env python3
"""
NexusBox Disk Quota Monitor

Monitors disk usage for sandbox workspaces and enforces quotas.
This script is invoked by the Go resource manager for fast disk usage
calculation (Python's os.walk is typically faster than Go's filepath.Walk
for large directory trees due to optimized C implementation).

Usage:
    python disk_quota_monitor.py check <workspace_path> <quota_bytes>
    python disk_quota_monitor.py monitor <workspace_path> <quota_bytes> [--interval 5]
    python disk_quota_monitor.py cleanup <workspace_path> [--max-age 3600]
"""

import os
import sys
import time
import json
import shutil
import argparse
import signal
from pathlib import Path
from typing import Dict, List, Tuple, Optional


def get_dir_size(path: str) -> int:
    """Calculate total size of a directory tree in bytes.

    Uses os.walk with symlink skipping for safety.
    """
    total = 0
    try:
        for dirpath, dirnames, filenames in os.walk(path, followlinks=False):
            # Skip symlinked directories to prevent infinite loops.
            dirnames[:] = [d for d in dirnames if not os.path.islink(os.path.join(dirpath, d))]

            for f in filenames:
                fp = os.path.join(dirpath, f)
                try:
                    if not os.path.islink(fp):
                        total += os.path.getsize(fp)
                except (OSError, PermissionError):
                    pass
    except (OSError, PermissionError):
        pass
    return total


def get_file_count(path: str) -> int:
    """Count files in a directory tree."""
    count = 0
    try:
        for dirpath, dirnames, filenames in os.walk(path, followlinks=False):
            dirnames[:] = [d for d in dirnames if not os.path.islink(os.path.join(dirpath, d))]
            count += len(filenames)
    except (OSError, PermissionError):
        pass
    return count


def get_largest_files(path: str, top_n: int = 10) -> List[Tuple[str, int]]:
    """Find the largest files in a directory tree."""
    files = []
    try:
        for dirpath, dirnames, filenames in os.walk(path, followlinks=False):
            dirnames[:] = [d for d in dirnames if not os.path.islink(os.path.join(dirpath, d))]
            for f in filenames:
                fp = os.path.join(dirpath, f)
                try:
                    if not os.path.islink(fp):
                        size = os.path.getsize(fp)
                        files.append((fp, size))
                except (OSError, PermissionError):
                    pass
    except (OSError, PermissionError):
        pass

    files.sort(key=lambda x: x[1], reverse=True)
    return files[:top_n]


def check_quota(workspace_path: str, quota_bytes: int) -> Dict:
    """Check if workspace exceeds disk quota.

    Returns a dict with:
        - exceeded: bool
        - current_usage: int (bytes)
        - quota: int (bytes)
        - file_count: int
        - largest_files: list of [path, size] pairs
    """
    usage = get_dir_size(workspace_path)
    count = get_file_count(workspace_path)
    largest = get_largest_files(workspace_path, 10)

    return {
        "exceeded": usage > quota_bytes,
        "current_usage": usage,
        "quota": quota_bytes,
        "file_count": count,
        "largest_files": [[f, s] for f, s in largest],
        "timestamp": time.time(),
    }


def monitor_loop(workspace_path: str, quota_bytes: int, interval: int = 5) -> None:
    """Continuously monitor disk usage and print JSON status.

    Runs until interrupted with Ctrl+C or SIGTERM.
    """
    running = True

    def signal_handler(sig, frame):
        nonlocal running
        running = False

    signal.signal(signal.SIGINT, signal_handler)
    signal.signal(signal.SIGTERM, signal_handler)

    print(json.dumps({"status": "started", "workspace": workspace_path}))
    sys.stdout.flush()

    while running:
        result = check_quota(workspace_path, quota_bytes)

        if result["exceeded"]:
            result["status"] = "quota_exceeded"
            print(json.dumps(result))
            sys.stdout.flush()
            # Enforce quota by removing oldest temp files.
            enforce_quota(workspace_path, quota_bytes)
        else:
            result["status"] = "ok"
            print(json.dumps(result))
            sys.stdout.flush()

        time.sleep(interval)

    print(json.dumps({"status": "stopped"}))
    sys.stdout.flush()


def enforce_quota(workspace_path: str, quota_bytes: int) -> None:
    """Attempt to enforce disk quota by cleaning up old temp files.

    This is a best-effort cleanup: it removes files matching common
    temp patterns (*.tmp, *.log, __pycache__, node_modules/.cache)
    that are older than 1 hour.
    """
    current_usage = get_dir_size(workspace_path)
    if current_usage <= quota_bytes:
        return

    # Patterns to clean up.
    cleanup_patterns = [
        "*.tmp",
        "*.temp",
        "*.log",
        "*.bak",
        "*.swp",
    ]
    cleanup_dirs = [
        "__pycache__",
        ".pytest_cache",
        "node_modules/.cache",
        ".gradle",
        ".tmp",
        "tmp",
    ]

    one_hour_ago = time.time() - 3600
    freed = 0

    # Clean up old temp files.
    for root, dirs, files in os.walk(workspace_path, followlinks=False):
        for f in files:
            fp = os.path.join(root, f)
            try:
                stat = os.stat(fp)
                if stat.st_mtime < one_hour_ago:
                    for pattern in cleanup_patterns:
                        if Path(fp).match(pattern):
                            size = stat.st_size
                            os.remove(fp)
                            freed += size
                            break
            except (OSError, PermissionError):
                pass

    # Clean up old cache directories.
    for root, dirs, _ in os.walk(workspace_path, followlinks=False):
        for d in dirs:
            if d in cleanup_dirs:
                dp = os.path.join(root, d)
                try:
                    stat = os.stat(dp)
                    if stat.st_mtime < one_hour_ago:
                        dir_size = get_dir_size(dp)
                        shutil.rmtree(dp, ignore_errors=True)
                        freed += dir_size
                except (OSError, PermissionError):
                pass

    if freed > 0:
        print(json.dumps({
            "status": "cleanup_performed",
            "bytes_freed": freed,
            "remaining_usage": get_dir_size(workspace_path),
        }))
        sys.stdout.flush()


def cleanup_old_files(workspace_path: str, max_age_seconds: int = 3600) -> Dict:
    """Remove files older than max_age_seconds.

    Returns a dict with cleanup statistics.
    """
    cutoff = time.time() - max_age_seconds
    removed_count = 0
    freed_bytes = 0

    for root, dirs, files in os.walk(workspace_path, followlinks=False):
        dirs[:] = [d for d in dirs if not os.path.islink(os.path.join(root, d))]

        for f in files:
            fp = os.path.join(root, f)
            try:
                stat = os.stat(fp)
                if stat.st_mtime < cutoff and not os.path.islink(fp):
                    size = stat.st_size
                    os.remove(fp)
                    removed_count += 1
                    freed_bytes += size
            except (OSError, PermissionError):
                pass

    return {
        "removed_count": removed_count,
        "freed_bytes": freed_bytes,
        "cutoff_age_seconds": max_age_seconds,
    }


def main():
    parser = argparse.ArgumentParser(description="NexusBox Disk Quota Monitor")
    subparsers = parser.add_subparsers(dest="command", help="Command to run")

    # check command
    check_parser = subparsers.add_parser("check", help="Check disk usage against quota")
    check_parser.add_argument("workspace", help="Workspace path")
    check_parser.add_argument("quota", type=int, help="Quota in bytes")

    # monitor command
    monitor_parser = subparsers.add_parser("monitor", help="Continuously monitor disk usage")
    monitor_parser.add_argument("workspace", help="Workspace path")
    monitor_parser.add_argument("quota", type=int, help="Quota in bytes")
    monitor_parser.add_argument("--interval", type=int, default=5, help="Check interval in seconds")

    # cleanup command
    cleanup_parser = subparsers.add_parser("cleanup", help="Remove old files")
    cleanup_parser.add_argument("workspace", help="Workspace path")
    cleanup_parser.add_argument("--max-age", type=int, default=3600, help="Max file age in seconds")

    args = parser.parse_args()

    if args.command == "check":
        result = check_quota(args.workspace, args.quota)
        print(json.dumps(result, indent=2))
        sys.exit(1 if result["exceeded"] else 0)

    elif args.command == "monitor":
        monitor_loop(args.workspace, args.quota, args.interval)

    elif args.command == "cleanup":
        result = cleanup_old_files(args.workspace, args.max_age)
        print(json.dumps(result, indent=2))

    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
