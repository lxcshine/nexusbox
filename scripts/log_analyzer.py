#!/usr/bin/env python3
"""
NexusBox Log Analyzer

Provides fast log analysis using Python's optimized text processing.
This script is invoked by the Go logging package for:
- Log pattern detection (error spikes, warnings)
- Statistical analysis (requests/sec, error rate)
- Log compaction (merging rotated logs)

Usage:
    python log_analyzer.py analyze <log_file> [--level ERROR] [--pattern "timeout"]
    python log_analyzer.py stats <log_file>
    python log_analyzer.py compact <log_dir> --sandbox <sandbox_id> --output <output_file>
"""

import os
import sys
import json
import re
import argparse
from collections import Counter, defaultdict
from datetime import datetime, timedelta
from typing import Dict, List, Tuple, Optional


# Common log patterns.
LOG_PATTERNS = {
    "json": re.compile(r'^\{.*\}$'),
    "klog": re.compile(r'^([IWEF])(\d{4})\s+(\d{2}:\d{2}:\d{2}\.\d+)'),
    "syslog": re.compile(r'^(\w{3}\s+\d+\s+\d{2}:\d{2}:\d{2})'),
    "plain": re.compile(r'^.*$'),
}

LEVEL_MAP = {
    "I": "INFO",
    "W": "WARN",
    "E": "ERROR",
    "F": "FATAL",
}


def parse_line(line: str) -> Dict:
    """Parse a log line into a structured entry.

    Supports JSON, klog, syslog, and plain text formats.
    """
    line = line.strip()
    if not line:
        return None

    # JSON format.
    if LOG_PATTERNS["json"].match(line):
        try:
            entry = json.loads(line)
            entry["format"] = "json"
            return entry
        except json.JSONDecodeError:
            pass

    # klog format.
    match = LOG_PATTERNS["klog"].match(line)
    if match:
        level_char = match.group(1)
        return {
            "format": "klog",
            "level": LEVEL_MAP.get(level_char, "INFO"),
            "timestamp": match.group(2) + " " + match.group(3),
            "message": line,
        }

    # Plain text.
    return {
        "format": "plain",
        "level": "INFO",
        "message": line,
    }


def analyze_log(log_file: str, level: str = None, pattern: str = None) -> Dict:
    """Analyze a log file and return statistics.

    Args:
        log_file: Path to the log file.
        level: Filter by log level (INFO, WARN, ERROR, FATAL).
        pattern: Regex pattern to search for.

    Returns:
        Dict with analysis results.
    """
    total_lines = 0
    level_counts = Counter()
    pattern_matches = []
    error_lines = []
    timestamps = []

    pattern_re = re.compile(pattern) if pattern else None

    with open(log_file, "r", encoding="utf-8", errors="replace") as f:
        for line_num, line in enumerate(f, 1):
            entry = parse_line(line)
            if entry is None:
                continue

            total_lines += 1
            entry_level = entry.get("level", "INFO")
            level_counts[entry_level] += 1

            # Filter by level.
            if level and entry_level != level:
                continue

            # Filter by pattern.
            if pattern_re:
                if pattern_re.search(entry.get("message", "")):
                    pattern_matches.append({
                        "line": line_num,
                        "entry": entry,
                    })

            # Collect errors.
            if entry_level in ("ERROR", "FATAL"):
                error_lines.append({
                    "line": line_num,
                    "entry": entry,
                })

            # Collect timestamps.
            ts = entry.get("timestamp")
            if ts:
                timestamps.append(ts)

    # Calculate time range.
    time_range = None
    if timestamps:
        time_range = {
            "start": timestamps[0] if timestamps else None,
            "end": timestamps[-1] if timestamps else None,
            "count": len(timestamps),
        }

    return {
        "total_lines": total_lines,
        "level_counts": dict(level_counts),
        "error_count": len(error_lines),
        "pattern_matches": pattern_matches[:100],  # Limit to first 100.
        "error_lines": error_lines[:100],
        "time_range": time_range,
    }


def get_stats(log_file: str) -> Dict:
    """Get basic statistics for a log file."""
    stats = {
        "file_path": log_file,
        "file_size": os.path.getsize(log_file),
        "line_count": 0,
        "level_counts": Counter(),
        "error_rate": 0.0,
    }

    with open(log_file, "r", encoding="utf-8", errors="replace") as f:
        for line in f:
            entry = parse_line(line)
            if entry is None:
                continue
            stats["line_count"] += 1
            stats["level_counts"][entry.get("level", "INFO")] += 1

    if stats["line_count"] > 0:
        error_count = stats["level_counts"].get("ERROR", 0) + stats["level_counts"].get("FATAL", 0)
        stats["error_rate"] = (error_count / stats["line_count"]) * 100

    stats["level_counts"] = dict(stats["level_counts"])
    return stats


def compact_logs(log_dir: str, sandbox_id: str, output_file: str) -> Dict:
    """Merge rotated log files for a sandbox into a single compacted file.

    This removes duplicate entries and sorts by timestamp.
    """
    # Find all log files for this sandbox.
    pattern = f"{sandbox_id}.log"
    rotated_pattern = f"{sandbox_id}.log."

    log_files = []
    for entry in os.listdir(log_dir):
        if entry == pattern or entry.startswith(rotated_pattern):
            log_files.append(os.path.join(log_dir, entry))

    if not log_files:
        return {"error": "no log files found", "sandbox_id": sandbox_id}

    # Collect all entries with timestamps.
    all_entries = []
    for log_file in log_files:
        with open(log_file, "r", encoding="utf-8", errors="replace") as f:
            for line in f:
                entry = parse_line(line)
                if entry:
                    all_entries.append(entry)

    # Sort by timestamp (if available).
    def get_timestamp(entry):
        ts = entry.get("timestamp", "")
        if isinstance(ts, str):
            return ts
        return ""

    all_entries.sort(key=get_timestamp)

    # Remove duplicates (by message content).
    seen = set()
    unique_entries = []
    for entry in all_entries:
        msg = entry.get("message", "")
        if msg not in seen:
            seen.add(msg)
            unique_entries.append(entry)

    # Write compacted file.
    with open(output_file, "w", encoding="utf-8") as f:
        for entry in unique_entries:
            f.write(json.dumps(entry) + "\n")

    return {
        "input_files": len(log_files),
        "total_entries": len(all_entries),
        "unique_entries": len(unique_entries),
        "output_file": output_file,
        "duplicates_removed": len(all_entries) - len(unique_entries),
    }


def detect_anomalies(log_file: str) -> Dict:
    """Detect anomalies in log patterns (error spikes, unusual patterns)."""
    # Read all lines.
    entries = []
    with open(log_file, "r", encoding="utf-8", errors="replace") as f:
        for line in f:
            entry = parse_line(line)
            if entry:
                entries.append(entry)

    if not entries:
        return {"anomalies": [], "total_entries": 0}

    # Detect error spikes.
    error_count = sum(1 for e in entries if e.get("level") in ("ERROR", "FATAL"))
    error_rate = (error_count / len(entries)) * 100 if entries else 0

    anomalies = []

    if error_rate > 10:
        anomalies.append({
            "type": "high_error_rate",
            "severity": "high",
            "message": f"Error rate is {error_rate:.1f}% ({error_count}/{len(entries)} lines)",
        })

    # Detect repeated errors.
    error_messages = Counter()
    for entry in entries:
        if entry.get("level") in ("ERROR", "FATAL"):
            msg = entry.get("message", "")[:100]
            error_messages[msg] += 1

    for msg, count in error_messages.most_common(5):
        if count > 5:
            anomalies.append({
                "type": "repeated_error",
                "severity": "medium",
                "message": f"Error repeated {count} times: {msg[:80]}...",
                "count": count,
            })

    return {
        "anomalies": anomalies,
        "total_entries": len(entries),
        "error_count": error_count,
        "error_rate": error_rate,
    }


def main():
    parser = argparse.ArgumentParser(description="NexusBox Log Analyzer")
    subparsers = parser.add_subparsers(dest="command", help="Command to run")

    # analyze command
    analyze_parser = subparsers.add_parser("analyze", help="Analyze a log file")
    analyze_parser.add_argument("log_file", help="Path to log file")
    analyze_parser.add_argument("--level", help="Filter by log level")
    analyze_parser.add_argument("--pattern", help="Regex pattern to search for")

    # stats command
    stats_parser = subparsers.add_parser("stats", help="Get log file statistics")
    stats_parser.add_argument("log_file", help="Path to log file")

    # compact command
    compact_parser = subparsers.add_parser("compact", help="Merge rotated logs")
    compact_parser.add_argument("log_dir", help="Log directory")
    compact_parser.add_argument("--sandbox", required=True, help="Sandbox ID")
    compact_parser.add_argument("--output", required=True, help="Output file")

    # anomalies command
    anomalies_parser = subparsers.add_parser("anomalies", help="Detect anomalies")
    anomalies_parser.add_argument("log_file", help="Path to log file")

    args = parser.parse_args()

    if args.command == "analyze":
        result = analyze_log(args.log_file, args.level, args.pattern)
        print(json.dumps(result, indent=2))

    elif args.command == "stats":
        result = get_stats(args.log_file)
        print(json.dumps(result, indent=2))

    elif args.command == "compact":
        result = compact_logs(args.log_dir, args.sandbox, args.output)
        print(json.dumps(result, indent=2))

    elif args.command == "anomalies":
        result = detect_anomalies(args.log_file)
        print(json.dumps(result, indent=2))

    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
