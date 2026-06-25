#!/usr/bin/env python3
"""
NexusBox Monitoring Dashboard

A real-time monitoring dashboard for the NexusBox sandbox scheduling system.
Provides visualization of system health, resource usage, and cost analysis.
"""

import os
import sys
import json
import time
import threading
import logging
from datetime import datetime, timedelta
from dataclasses import dataclass, field, asdict
from typing import Dict, List, Optional, Any
from collections import defaultdict

# Third-party imports
try:
    import requests
    from flask import Flask, jsonify, render_template_string, request
except ImportError:
    print("Required packages not found. Install with: pip install flask requests")
    sys.exit(1)

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger('nexusbox-dashboard')


# ============================================================================
# Data Models
# ============================================================================

@dataclass
class NodeStatus:
    """Status of a single node in the cluster."""
    name: str
    ip: str
    status: str = "unknown"
    cpu_capacity: int = 0
    cpu_used: int = 0
    memory_capacity: int = 0
    memory_used: int = 0
    gpu_count: int = 0
    sandbox_count: int = 0
    max_sandboxes: int = 0
    last_heartbeat: Optional[str] = None
    conditions: List[Dict[str, str]] = field(default_factory=list)
    supported_runtimes: List[str] = field(default_factory=list)

    @property
    def cpu_usage_percent(self) -> float:
        if self.cpu_capacity == 0:
            return 0.0
        return (self.cpu_used / self.cpu_capacity) * 100

    @property
    def memory_usage_percent(self) -> float:
        if self.memory_capacity == 0:
            return 0.0
        return (self.memory_used / self.memory_capacity) * 100

    @property
    def is_healthy(self) -> bool:
        return self.status == "Ready"


@dataclass
class SandboxStatus:
    """Status of a single sandbox."""
    name: str
    namespace: str
    tenant: str
    phase: str
    node: str = ""
    runtime: str = ""
    created_at: Optional[str] = None
    started_at: Optional[str] = None
    cpu_request: str = ""
    memory_request: str = ""
    retry_count: int = 0

    @property
    def key(self) -> str:
        return f"{self.namespace}/{self.name}"


@dataclass
class TenantInfo:
    """Information about a tenant."""
    name: str
    phase: str = "active"
    sandbox_count: int = 0
    cpu_used: int = 0
    cpu_limit: int = 0
    memory_used: int = 0
    memory_limit: int = 0
    cost: float = 0.0
    isolation_level: str = "standard"

    @property
    def cpu_usage_percent(self) -> float:
        if self.cpu_limit == 0:
            return 0.0
        return (self.cpu_used / self.cpu_limit) * 100

    @property
    def memory_usage_percent(self) -> float:
        if self.memory_limit == 0:
            return 0.0
        return (self.memory_used / self.memory_limit) * 100


@dataclass
class SchedulingMetrics:
    """Scheduling system metrics."""
    total_scheduled: int = 0
    total_failed: int = 0
    total_unschedulable: int = 0
    avg_scheduling_latency_ms: float = 0.0
    avg_binding_latency_ms: float = 0.0
    pending_count: int = 0
    active_nodes: int = 0

    @property
    def success_rate(self) -> float:
        total = self.total_scheduled + self.total_failed
        if total == 0:
            return 100.0
        return (self.total_scheduled / total) * 100


@dataclass
class CostReport:
    """Cost report for a tenant."""
    tenant_name: str
    total_cost: float = 0.0
    cpu_cost: float = 0.0
    memory_cost: float = 0.0
    gpu_cost: float = 0.0
    storage_cost: float = 0.0
    period_start: Optional[str] = None
    period_end: Optional[str] = None
    sandbox_count: int = 0


# ============================================================================
# Metrics Collector
# ============================================================================

class MetricsCollector:
    """Collects metrics from the NexusBox API server."""

    def __init__(self, api_url: str = "http://localhost:8080", interval: int = 15):
        self.api_url = api_url.rstrip('/')
        self.interval = interval
        self.nodes: Dict[str, NodeStatus] = {}
        self.sandboxes: Dict[str, SandboxStatus] = {}
        self.tenants: Dict[str, TenantInfo] = {}
        self.scheduling_metrics = SchedulingMetrics()
        self.cost_reports: Dict[str, CostReport] = {}
        self.alerts: List[Dict[str, Any]] = []
        self._running = False
        self._lock = threading.Lock()

    def start(self):
        """Start the metrics collection loop."""
        self._running = True
        thread = threading.Thread(target=self._collect_loop, daemon=True)
        thread.start()
        logger.info("Metrics collector started")

    def stop(self):
        """Stop the metrics collection loop."""
        self._running = False
        logger.info("Metrics collector stopped")

    def _collect_loop(self):
        """Main collection loop."""
        while self._running:
            try:
                self._collect_all()
            except Exception as e:
                logger.error(f"Error collecting metrics: {e}")
            time.sleep(self.interval)

    def _collect_all(self):
        """Collect all metrics from the API."""
        self._collect_nodes()
        self._collect_sandboxes()
        self._collect_tenants()
        self._collect_scheduling_metrics()
        self._collect_cost_reports()
        self._check_alerts()

    def _collect_nodes(self):
        """Collect node status information."""
        try:
            resp = requests.get(f"{self.api_url}/api/v1/nodes", timeout=5)
            if resp.status_code == 200:
                data = resp.json()
                with self._lock:
                    for node_data in data.get('items', []):
                        name = node_data.get('name', '')
                        self.nodes[name] = NodeStatus(
                            name=name,
                            ip=node_data.get('ip', ''),
                            status=node_data.get('status', 'unknown'),
                            cpu_capacity=node_data.get('cpuCapacity', 0),
                            cpu_used=node_data.get('cpuUsed', 0),
                            memory_capacity=node_data.get('memoryCapacity', 0),
                            memory_used=node_data.get('memoryUsed', 0),
                            gpu_count=node_data.get('gpuCount', 0),
                            sandbox_count=node_data.get('sandboxCount', 0),
                            max_sandboxes=node_data.get('maxSandboxes', 0),
                            last_heartbeat=node_data.get('lastHeartbeat'),
                            conditions=node_data.get('conditions', []),
                            supported_runtimes=node_data.get('supportedRuntimes', []),
                        )
        except requests.RequestException as e:
            logger.warning(f"Failed to collect node metrics: {e}")

    def _collect_sandboxes(self):
        """Collect sandbox status information."""
        try:
            resp = requests.get(f"{self.api_url}/api/v1/sandboxes", timeout=5)
            if resp.status_code == 200:
                data = resp.json()
                with self._lock:
                    for sb_data in data.get('items', []):
                        key = f"{sb_data.get('namespace', '')}/{sb_data.get('name', '')}"
                        self.sandboxes[key] = SandboxStatus(
                            name=sb_data.get('name', ''),
                            namespace=sb_data.get('namespace', ''),
                            tenant=sb_data.get('tenant', ''),
                            phase=sb_data.get('phase', 'unknown'),
                            node=sb_data.get('nodeName', ''),
                            runtime=sb_data.get('runtime', ''),
                            created_at=sb_data.get('createdAt'),
                            started_at=sb_data.get('startedAt'),
                            cpu_request=sb_data.get('cpuRequest', ''),
                            memory_request=sb_data.get('memoryRequest', ''),
                            retry_count=sb_data.get('retryCount', 0),
                        )
        except requests.RequestException as e:
            logger.warning(f"Failed to collect sandbox metrics: {e}")

    def _collect_tenants(self):
        """Collect tenant information."""
        try:
            resp = requests.get(f"{self.api_url}/api/v1/tenants", timeout=5)
            if resp.status_code == 200:
                data = resp.json()
                with self._lock:
                    for t_data in data.get('items', []):
                        name = t_data.get('name', '')
                        self.tenants[name] = TenantInfo(
                            name=name,
                            phase=t_data.get('phase', 'unknown'),
                            sandbox_count=t_data.get('sandboxCount', 0),
                            cpu_used=t_data.get('cpuUsed', 0),
                            cpu_limit=t_data.get('cpuLimit', 0),
                            memory_used=t_data.get('memoryUsed', 0),
                            memory_limit=t_data.get('memoryLimit', 0),
                            cost=t_data.get('cost', 0.0),
                            isolation_level=t_data.get('isolationLevel', 'standard'),
                        )
        except requests.RequestException as e:
            logger.warning(f"Failed to collect tenant metrics: {e}")

    def _collect_scheduling_metrics(self):
        """Collect scheduling metrics."""
        try:
            resp = requests.get(f"{self.api_url}/api/v1/metrics/scheduling", timeout=5)
            if resp.status_code == 200:
                data = resp.json()
                with self._lock:
                    self.scheduling_metrics = SchedulingMetrics(
                        total_scheduled=data.get('totalScheduled', 0),
                        total_failed=data.get('totalFailed', 0),
                        total_unschedulable=data.get('totalUnschedulable', 0),
                        avg_scheduling_latency_ms=data.get('avgSchedulingLatencyMs', 0.0),
                        avg_binding_latency_ms=data.get('avgBindingLatencyMs', 0.0),
                        pending_count=data.get('pendingCount', 0),
                        active_nodes=data.get('activeNodes', 0),
                    )
        except requests.RequestException as e:
            logger.warning(f"Failed to collect scheduling metrics: {e}")

    def _collect_cost_reports(self):
        """Collect cost reports."""
        try:
            resp = requests.get(f"{self.api_url}/api/v1/costs", timeout=5)
            if resp.status_code == 200:
                data = resp.json()
                with self._lock:
                    for c_data in data.get('items', []):
                        tenant = c_data.get('tenantName', '')
                        self.cost_reports[tenant] = CostReport(
                            tenant_name=tenant,
                            total_cost=c_data.get('totalCost', 0.0),
                            cpu_cost=c_data.get('cpuCost', 0.0),
                            memory_cost=c_data.get('memoryCost', 0.0),
                            gpu_cost=c_data.get('gpuCost', 0.0),
                            storage_cost=c_data.get('storageCost', 0.0),
                            period_start=c_data.get('periodStart'),
                            period_end=c_data.get('periodEnd'),
                            sandbox_count=c_data.get('sandboxCount', 0),
                        )
        except requests.RequestException as e:
            logger.warning(f"Failed to collect cost reports: {e}")

    def _check_alerts(self):
        """Check for alert conditions."""
        new_alerts = []

        with self._lock:
            # Check node health
            for name, node in self.nodes.items():
                if not node.is_healthy:
                    new_alerts.append({
                        'type': 'NodeUnhealthy',
                        'severity': 'critical',
                        'message': f'Node {name} is not healthy (status: {node.status})',
                        'timestamp': datetime.now().isoformat(),
                    })

                # Check high resource usage
                if node.cpu_usage_percent > 90:
                    new_alerts.append({
                        'type': 'HighCPUUsage',
                        'severity': 'warning',
                        'message': f'Node {name} CPU usage is {node.cpu_usage_percent:.1f}%',
                        'timestamp': datetime.now().isoformat(),
                    })

                if node.memory_usage_percent > 90:
                    new_alerts.append({
                        'type': 'HighMemoryUsage',
                        'severity': 'warning',
                        'message': f'Node {name} memory usage is {node.memory_usage_percent:.1f}%',
                        'timestamp': datetime.now().isoformat(),
                    })

            # Check tenant quota usage
            for name, tenant in self.tenants.items():
                if tenant.cpu_usage_percent > 80:
                    new_alerts.append({
                        'type': 'TenantQuotaWarning',
                        'severity': 'warning',
                        'message': f'Tenant {name} CPU quota usage is {tenant.cpu_usage_percent:.1f}%',
                        'timestamp': datetime.now().isoformat(),
                    })

            # Check scheduling failures
            if self.scheduling_metrics.success_rate < 95:
                new_alerts.append({
                    'type': 'LowSchedulingSuccessRate',
                    'severity': 'warning',
                    'message': f'Scheduling success rate is {self.scheduling_metrics.success_rate:.1f}%',
                    'timestamp': datetime.now().isoformat(),
                })

        with self._lock:
            self.alerts = new_alerts[-100:]  # Keep last 100 alerts

    def get_summary(self) -> Dict[str, Any]:
        """Get a summary of all collected metrics."""
        with self._lock:
            sandbox_phases = defaultdict(int)
            for sb in self.sandboxes.values():
                sandbox_phases[sb.phase] += 1

            return {
                'timestamp': datetime.now().isoformat(),
                'nodes': {
                    'total': len(self.nodes),
                    'healthy': sum(1 for n in self.nodes.values() if n.is_healthy),
                    'unhealthy': sum(1 for n in self.nodes.values() if not n.is_healthy),
                },
                'sandboxes': {
                    'total': len(self.sandboxes),
                    'by_phase': dict(sandbox_phases),
                },
                'tenants': {
                    'total': len(self.tenants),
                    'active': sum(1 for t in self.tenants.values() if t.phase == 'active'),
                },
                'scheduling': asdict(self.scheduling_metrics),
                'costs': {
                    'total': sum(c.total_cost for c in self.cost_reports.values()),
                    'by_tenant': {k: v.total_cost for k, v in self.cost_reports.items()},
                },
                'alerts': {
                    'total': len(self.alerts),
                    'critical': sum(1 for a in self.alerts if a.get('severity') == 'critical'),
                    'warning': sum(1 for a in self.alerts if a.get('severity') == 'warning'),
                },
            }


# ============================================================================
# Alert Manager
# ============================================================================

class AlertManager:
    """Manages alerting for the NexusBox system."""

    def __init__(self, config: Dict[str, Any] = None):
        self.config = config or {}
        self.rules: List[Dict[str, Any]] = []
        self.notification_channels: List[Dict[str, Any]] = []
        self.alert_history: List[Dict[str, Any]] = []
        self._load_default_rules()

    def _load_default_rules(self):
        """Load default alerting rules."""
        self.rules = [
            {
                'name': 'NodeDown',
                'condition': 'node_status != "Ready"',
                'severity': 'critical',
                'cooldown': 300,  # 5 minutes
                'message': 'Node {{ .NodeName }} is down',
            },
            {
                'name': 'HighCPUUsage',
                'condition': 'cpu_usage_percent > 90',
                'severity': 'warning',
                'cooldown': 600,
                'message': 'Node {{ .NodeName }} CPU usage is {{ .CPUUsage }}%',
            },
            {
                'name': 'HighMemoryUsage',
                'condition': 'memory_usage_percent > 90',
                'severity': 'warning',
                'cooldown': 600,
                'message': 'Node {{ .NodeName }} memory usage is {{ .MemoryUsage }}%',
            },
            {
                'name': 'SandboxFailed',
                'condition': 'sandbox_phase == "Failed"',
                'severity': 'warning',
                'cooldown': 60,
                'message': 'Sandbox {{ .SandboxName }} failed: {{ .Reason }}',
            },
            {
                'name': 'TenantQuotaExceeded',
                'condition': 'quota_usage_percent > 95',
                'severity': 'critical',
                'cooldown': 300,
                'message': 'Tenant {{ .TenantName }} exceeded quota',
            },
            {
                'name': 'SchedulingBacklog',
                'condition': 'pending_sandboxes > 100',
                'severity': 'warning',
                'cooldown': 300,
                'message': 'Scheduling backlog: {{ .PendingCount }} sandboxes pending',
            },
        ]

    def add_notification_channel(self, channel_type: str, config: Dict[str, Any]):
        """Add a notification channel."""
        self.notification_channels.append({
            'type': channel_type,
            'config': config,
        })

    def evaluate(self, metrics: Dict[str, Any]) -> List[Dict[str, Any]]:
        """Evaluate alert rules against current metrics."""
        fired_alerts = []

        for rule in self.rules:
            if self._evaluate_condition(rule['condition'], metrics):
                alert = {
                    'rule_name': rule['name'],
                    'severity': rule['severity'],
                    'message': rule['message'],
                    'timestamp': datetime.now().isoformat(),
                    'labels': metrics,
                }
                fired_alerts.append(alert)
                self.alert_history.append(alert)

        # Keep last 1000 alerts
        self.alert_history = self.alert_history[-1000:]

        return fired_alerts

    def _evaluate_condition(self, condition: str, metrics: Dict[str, Any]) -> bool:
        """Evaluate a simple condition against metrics."""
        # Simplified condition evaluation
        # In production, use a proper expression evaluator
        try:
            if 'node_status' in condition:
                return metrics.get('node_status', '') != 'Ready'
            if 'cpu_usage_percent' in condition:
                return metrics.get('cpu_usage_percent', 0) > 90
            if 'memory_usage_percent' in condition:
                return metrics.get('memory_usage_percent', 0) > 90
            if 'sandbox_phase' in condition:
                return metrics.get('sandbox_phase', '') == 'Failed'
            if 'quota_usage_percent' in condition:
                return metrics.get('quota_usage_percent', 0) > 95
            if 'pending_sandboxes' in condition:
                return metrics.get('pending_sandboxes', 0) > 100
        except Exception:
            pass
        return False

    def send_notification(self, alert: Dict[str, Any]):
        """Send alert notification through configured channels."""
        for channel in self.notification_channels:
            try:
                if channel['type'] == 'webhook':
                    self._send_webhook(channel['config'], alert)
                elif channel['type'] == 'email':
                    self._send_email(channel['config'], alert)
                elif channel['type'] == 'slack':
                    self._send_slack(channel['config'], alert)
            except Exception as e:
                logger.error(f"Failed to send notification: {e}")

    def _send_webhook(self, config: Dict[str, Any], alert: Dict[str, Any]):
        """Send alert via webhook."""
        url = config.get('url', '')
        if url:
            requests.post(url, json=alert, timeout=5)

    def _send_email(self, config: Dict[str, Any], alert: Dict[str, Any]):
        """Send alert via email."""
        # In production, use SMTP or an email service
        logger.info(f"Email alert: {alert['message']}")

    def _send_slack(self, config: Dict[str, Any], alert: Dict[str, Any]):
        """Send alert via Slack."""
        webhook_url = config.get('webhook_url', '')
        if webhook_url:
            payload = {
                'text': f"[{alert['severity'].upper()}] {alert['message']}",
                'attachments': [{
                    'color': 'danger' if alert['severity'] == 'critical' else 'warning',
                    'fields': [
                        {'title': 'Rule', 'value': alert['rule_name'], 'short': True},
                        {'title': 'Severity', 'value': alert['severity'], 'short': True},
                        {'title': 'Time', 'value': alert['timestamp'], 'short': False},
                    ],
                }],
            }
            requests.post(webhook_url, json=payload, timeout=5)


# ============================================================================
# Cost Analyzer
# ============================================================================

class CostAnalyzer:
    """Analyzes costs and provides optimization recommendations."""

    def __init__(self, metrics_collector: MetricsCollector):
        self.collector = metrics_collector
        self.pricing = {
            'cpu_per_core_hour': 0.02,
            'memory_per_gb_hour': 0.004,
            'gpu_per_hour': 0.50,
            'storage_per_gb_month': 0.10,
            'sandbox_base': 0.001,
        }

    def analyze(self) -> Dict[str, Any]:
        """Run cost analysis."""
        with self.collector._lock:
            total_cost = sum(r.total_cost for r in self.collector.cost_reports.values())
            tenant_costs = {k: asdict(v) for k, v in self.collector.cost_reports.items()}

        # Calculate cost breakdown
        breakdown = {
            'cpu': sum(r.cpu_cost for r in self.collector.cost_reports.values()),
            'memory': sum(r.memory_cost for r in self.collector.cost_reports.values()),
            'gpu': sum(r.gpu_cost for r in self.collector.cost_reports.values()),
            'storage': sum(r.storage_cost for r in self.collector.cost_reports.values()),
        }

        # Generate recommendations
        recommendations = self._generate_recommendations()

        return {
            'total_cost': total_cost,
            'breakdown': breakdown,
            'tenant_costs': tenant_costs,
            'recommendations': recommendations,
            'timestamp': datetime.now().isoformat(),
        }

    def _generate_recommendations(self) -> List[Dict[str, Any]]:
        """Generate cost optimization recommendations."""
        recommendations = []

        with self.collector._lock:
            # Check for underutilized nodes
            for name, node in self.collector.nodes.items():
                if node.cpu_usage_percent < 20 and node.sandbox_count == 0:
                    recommendations.append({
                        'type': 'IdleNode',
                        'severity': 'info',
                        'message': f'Node {name} is idle (CPU: {node.cpu_usage_percent:.1f}%, Sandboxes: {node.sandbox_count})',
                        'potential_savings': self.pricing['cpu_per_core_hour'] * (node.cpu_capacity / 1000) * 24 * 30,
                    })

            # Check for over-provisioned tenants
            for name, tenant in self.collector.tenants.items():
                if tenant.cpu_usage_percent < 30 and tenant.cpu_limit > 0:
                    recommendations.append({
                        'type': 'OverProvisionedTenant',
                        'severity': 'info',
                        'message': f'Tenant {name} is using {tenant.cpu_usage_percent:.1f}% of CPU quota',
                        'potential_savings': self.pricing['cpu_per_core_hour'] * (tenant.cpu_limit - tenant.cpu_used) / 1000 * 24 * 30 * 0.5,
                    })

        return recommendations

    def estimate_monthly_cost(self) -> Dict[str, float]:
        """Estimate monthly cost based on current usage."""
        with self.collector._lock:
            daily_cost = sum(r.total_cost for r in self.collector.cost_reports.values())
            # Extrapolate to monthly
            monthly_estimate = daily_cost * 30

        return {
            'daily_cost': daily_cost,
            'monthly_estimate': monthly_estimate,
            'quarterly_estimate': monthly_estimate * 3,
        }


# ============================================================================
# Dashboard Application
# ============================================================================

DASHBOARD_HTML = """
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>NexusBox Dashboard</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
               background: #1a1a2e; color: #e0e0e0; }
        .header { background: #16213e; padding: 20px; border-bottom: 2px solid #0f3460; }
        .header h1 { color: #e94560; font-size: 24px; }
        .header .status { float: right; color: #4ecca3; }
        .container { padding: 20px; display: grid; grid-template-columns: repeat(3, 1fr); gap: 20px; }
        .card { background: #16213e; border-radius: 8px; padding: 20px; border: 1px solid #0f3460; }
        .card h2 { color: #e94560; margin-bottom: 15px; font-size: 16px; }
        .metric { display: flex; justify-content: space-between; padding: 8px 0;
                  border-bottom: 1px solid #0f3460; }
        .metric .label { color: #a0a0a0; }
        .metric .value { color: #4ecca3; font-weight: bold; }
        .metric .value.warning { color: #ffd93d; }
        .metric .value.critical { color: #e94560; }
        .progress-bar { background: #0f3460; border-radius: 4px; height: 8px; margin-top: 5px; }
        .progress-bar .fill { height: 100%; border-radius: 4px; transition: width 0.3s; }
        .fill.green { background: #4ecca3; }
        .fill.yellow { background: #ffd93d; }
        .fill.red { background: #e94560; }
        .alerts { grid-column: span 3; }
        .alert { padding: 10px; margin: 5px 0; border-radius: 4px; }
        .alert.critical { background: rgba(233, 69, 96, 0.2); border-left: 3px solid #e94560; }
        .alert.warning { background: rgba(255, 217, 61, 0.2); border-left: 3px solid #ffd93d; }
        .alert.info { background: rgba(78, 204, 163, 0.2); border-left: 3px solid #4ecca3; }
        .refresh-info { text-align: center; color: #666; padding: 10px; font-size: 12px; }
    </style>
</head>
<body>
    <div class="header">
        <h1>NexusBox Sandbox Dashboard</h1>
        <span class="status" id="status">Connecting...</span>
    </div>
    <div class="container" id="dashboard">
        <div class="card">
            <h2>Cluster Overview</h2>
            <div id="cluster-metrics"></div>
        </div>
        <div class="card">
            <h2>Scheduling Metrics</h2>
            <div id="scheduling-metrics"></div>
        </div>
        <div class="card">
            <h2>Cost Summary</h2>
            <div id="cost-metrics"></div>
        </div>
        <div class="card">
            <h2>Nodes</h2>
            <div id="node-list"></div>
        </div>
        <div class="card">
            <h2>Tenants</h2>
            <div id="tenant-list"></div>
        </div>
        <div class="card">
            <h2>Sandboxes by Phase</h2>
            <div id="sandbox-phases"></div>
        </div>
        <div class="alerts card">
            <h2>Active Alerts</h2>
            <div id="alert-list"></div>
        </div>
    </div>
    <div class="refresh-info" id="refresh-info">Last updated: Never</div>
    <script>
        function formatBytes(bytes) {
            if (bytes === 0) return '0 B';
            const k = 1024;
            const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
            const i = Math.floor(Math.log(bytes) / Math.log(k));
            return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
        }

        function formatCost(cost) {
            return '$' + cost.toFixed(2);
        }

        function getUsageClass(percent) {
            if (percent > 90) return 'critical';
            if (percent > 70) return 'warning';
            return '';
        }

        function getBarClass(percent) {
            if (percent > 90) return 'red';
            if (percent > 70) return 'yellow';
            return 'green';
        }

        async function refresh() {
            try {
                const resp = await fetch('/api/dashboard/summary');
                const data = await resp.json();

                // Update status
                document.getElementById('status').textContent =
                    `Connected | ${data.nodes.total} Nodes | ${data.sandboxes.total} Sandboxes`;

                // Cluster metrics
                document.getElementById('cluster-metrics').innerHTML = `
                    <div class="metric"><span class="label">Nodes</span>
                        <span class="value">${data.nodes.healthy}/${data.nodes.total} healthy</span></div>
                    <div class="metric"><span class="label">Sandboxes</span>
                        <span class="value">${data.sandboxes.total}</span></div>
                    <div class="metric"><span class="label">Tenants</span>
                        <span class="value">${data.tenants.active}/${data.tenants.total} active</span></div>
                    <div class="metric"><span class="label">Alerts</span>
                        <span class="value ${data.alerts.critical > 0 ? 'critical' : ''}">${data.alerts.critical} critical, ${data.alerts.warning} warnings</span></div>
                `;

                // Scheduling metrics
                const sched = data.scheduling;
                document.getElementById('scheduling-metrics').innerHTML = `
                    <div class="metric"><span class="label">Success Rate</span>
                        <span class="value">${sched.success_rate.toFixed(1)}%</span></div>
                    <div class="metric"><span class="label">Scheduled</span>
                        <span class="value">${sched.total_scheduled}</span></div>
                    <div class="metric"><span class="label">Failed</span>
                        <span class="value ${sched.total_failed > 0 ? 'warning' : ''}">${sched.total_failed}</span></div>
                    <div class="metric"><span class="label">Pending</span>
                        <span class="value">${sched.pending_count}</span></div>
                    <div class="metric"><span class="label">Avg Latency</span>
                        <span class="value">${sched.avg_scheduling_latency_ms.toFixed(1)}ms</span></div>
                `;

                // Cost metrics
                document.getElementById('cost-metrics').innerHTML = `
                    <div class="metric"><span class="label">Total Cost</span>
                        <span class="value">${formatCost(data.costs.total)}</span></div>
                `;

                // Sandbox phases
                const phases = data.sandboxes.by_phase;
                let phaseHtml = '';
                for (const [phase, count] of Object.entries(phases)) {
                    phaseHtml += `<div class="metric"><span class="label">${phase}</span>
                        <span class="value">${count}</span></div>`;
                }
                document.getElementById('sandbox-phases').innerHTML = phaseHtml;

                // Alerts
                let alertHtml = '';
                if (data.alerts && data.alerts.total > 0) {
                    // Show recent alerts
                    alertHtml = `<div class="metric"><span class="label">Total Alerts</span>
                        <span class="value">${data.alerts.total}</span></div>`;
                } else {
                    alertHtml = '<div class="metric"><span class="label">No active alerts</span></div>';
                }
                document.getElementById('alert-list').innerHTML = alertHtml;

                document.getElementById('refresh-info').textContent =
                    'Last updated: ' + new Date().toLocaleTimeString();

            } catch (e) {
                document.getElementById('status').textContent = 'Connection Error';
                console.error('Failed to refresh:', e);
            }
        }

        refresh();
        setInterval(refresh, 15000);
    </script>
</body>
</html>
"""


def create_app(api_url: str = "http://localhost:8080") -> Flask:
    """Create the Flask dashboard application."""
    app = Flask(__name__)

    # Initialize components
    collector = MetricsCollector(api_url=api_url)
    alert_manager = AlertManager()
    cost_analyzer = CostAnalyzer(collector)

    # Start metrics collection
    collector.start()

    @app.route('/')
    def index():
        return render_template_string(DASHBOARD_HTML)

    @app.route('/api/dashboard/summary')
    def dashboard_summary():
        return jsonify(collector.get_summary())

    @app.route('/api/dashboard/nodes')
    def dashboard_nodes():
        with collector._lock:
            return jsonify({k: asdict(v) for k, v in collector.nodes.items()})

    @app.route('/api/dashboard/sandboxes')
    def dashboard_sandboxes():
        with collector._lock:
            return jsonify({k: asdict(v) for k, v in collector.sandboxes.items()})

    @app.route('/api/dashboard/tenants')
    def dashboard_tenants():
        with collector._lock:
            return jsonify({k: asdict(v) for k, v in collector.tenants.items()})

    @app.route('/api/dashboard/costs')
    def dashboard_costs():
        return jsonify(cost_analyzer.analyze())

    @app.route('/api/dashboard/alerts')
    def dashboard_alerts():
        with collector._lock:
            return jsonify(collector.alerts)

    @app.route('/api/dashboard/recommendations')
    def dashboard_recommendations():
        analysis = cost_analyzer.analyze()
        return jsonify(analysis.get('recommendations', []))

    @app.route('/api/dashboard/health')
    def dashboard_health():
        return jsonify({'status': 'ok', 'timestamp': datetime.now().isoformat()})

    return app


# ============================================================================
# Main Entry Point
# ============================================================================

def main():
    """Main entry point for the dashboard."""
    import argparse

    parser = argparse.ArgumentParser(description='NexusBox Monitoring Dashboard')
    parser.add_argument('--host', default='0.0.0.0', help='Host to bind to')
    parser.add_argument('--port', type=int, default=3000, help='Port to listen on')
    parser.add_argument('--api-url', default='http://localhost:8080', help='NexusBox API URL')
    parser.add_argument('--debug', action='store_true', help='Enable debug mode')
    args = parser.parse_args()

    logger.info(f"Starting NexusBox Dashboard on {args.host}:{args.port}")
    logger.info(f"API URL: {args.api_url}")

    app = create_app(api_url=args.api_url)
    app.run(host=args.host, port=args.port, debug=args.debug)


if __name__ == '__main__':
    main()
