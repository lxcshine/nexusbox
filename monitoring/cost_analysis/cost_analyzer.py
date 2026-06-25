#!/usr/bin/env python3
"""
NexusBox Cost Analysis Tool

Provides detailed cost analysis, billing reports, and optimization
recommendations for the NexusBox sandbox scheduling system.
"""

import json
import argparse
import logging
from datetime import datetime, timedelta
from dataclasses import dataclass, field, asdict
from typing import Dict, List, Optional, Any
from collections import defaultdict

try:
    import requests
except ImportError:
    print("Required packages not found. Install with: pip install requests")
    exit(1)

logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger('nexusbox-cost')


# ============================================================================
# Pricing Models
# ============================================================================

@dataclass
class ResourcePricing:
    """Pricing configuration for resources."""
    cpu_per_core_hour: float = 0.02
    memory_per_gb_hour: float = 0.004
    gpu_per_hour: float = 0.50
    ephemeral_storage_per_gb_hour: float = 0.0001
    persistent_storage_per_gb_month: float = 0.10
    sandbox_base_price: float = 0.001
    network_egress_per_gb: float = 0.01

    # Tiered pricing for different runtime types
    runtime_premium: Dict[str, float] = field(default_factory=lambda: {
        'kata-containers': 1.5,  # 50% premium for strong isolation
        'gvisor': 1.2,           # 20% premium for gVisor
        'runc': 1.0,            # No premium for standard containers
    })

    # Tiered pricing for isolation levels
    isolation_premium: Dict[str, float] = field(default_factory=lambda: {
        'maximum': 2.0,   # 100% premium for maximum isolation
        'enhanced': 1.5,  # 50% premium for enhanced isolation
        'standard': 1.0,  # No premium for standard isolation
        'basic': 0.8,     # 20% discount for basic isolation
    })


@dataclass
class SandboxCostRecord:
    """Cost record for a single sandbox."""
    sandbox_name: str
    namespace: str
    tenant_name: str
    runtime_type: str
    node_name: str
    cpu_cores: float
    memory_gb: float
    gpu_count: int
    storage_gb: float
    duration_hours: float
    base_cost: float = 0.0
    runtime_cost: float = 0.0
    isolation_cost: float = 0.0
    total_cost: float = 0.0
    started_at: Optional[str] = None
    stopped_at: Optional[str] = None


@dataclass
class TenantCostSummary:
    """Cost summary for a tenant."""
    tenant_name: str
    total_cost: float = 0.0
    cpu_cost: float = 0.0
    memory_cost: float = 0.0
    gpu_cost: float = 0.0
    storage_cost: float = 0.0
    runtime_premium: float = 0.0
    isolation_premium: float = 0.0
    sandbox_count: int = 0
    avg_cost_per_sandbox: float = 0.0
    cost_trend: List[Dict[str, Any]] = field(default_factory=list)


@dataclass
class OptimizationRecommendation:
    """Cost optimization recommendation."""
    type: str
    severity: str
    message: str
    potential_savings: float
    details: Dict[str, Any] = field(default_factory=dict)


# ============================================================================
# Cost Calculator
# ============================================================================

class CostCalculator:
    """Calculates costs for sandbox resources."""

    def __init__(self, pricing: ResourcePricing = None):
        self.pricing = pricing or ResourcePricing()

    def calculate_sandbox_cost(self, record: SandboxCostRecord) -> SandboxCostRecord:
        """Calculate the cost for a single sandbox."""
        # Base resource costs
        cpu_cost = record.cpu_cores * self.pricing.cpu_per_core_hour * record.duration_hours
        memory_cost = record.memory_gb * self.pricing.memory_per_gb_hour * record.duration_hours
        gpu_cost = record.gpu_count * self.pricing.gpu_per_hour * record.duration_hours
        storage_cost = record.storage_gb * self.pricing.ephemeral_storage_per_gb_hour * record.duration_hours

        base_cost = cpu_cost + memory_cost + gpu_cost + storage_cost + self.pricing.sandbox_base_price

        # Apply runtime premium
        runtime_multiplier = self.pricing.runtime_premium.get(record.runtime_type, 1.0)
        runtime_cost = base_cost * (runtime_multiplier - 1.0)

        # Apply isolation premium (based on runtime type)
        isolation_level = self._get_isolation_level(record.runtime_type)
        isolation_multiplier = self.pricing.isolation_premium.get(isolation_level, 1.0)
        isolation_cost = base_cost * (isolation_multiplier - 1.0)

        record.base_cost = round(base_cost, 6)
        record.runtime_cost = round(runtime_cost, 6)
        record.isolation_cost = round(isolation_cost, 6)
        record.total_cost = round(base_cost + runtime_cost + isolation_cost, 6)

        return record

    def _get_isolation_level(self, runtime_type: str) -> str:
        """Get isolation level from runtime type."""
        if runtime_type == 'kata-containers':
            return 'maximum'
        elif runtime_type == 'gvisor':
            return 'enhanced'
        else:
            return 'standard'

    def calculate_tenant_summary(self, records: List[SandboxCostRecord], tenant_name: str) -> TenantCostSummary:
        """Calculate cost summary for a tenant."""
        tenant_records = [r for r in records if r.tenant_name == tenant_name]

        summary = TenantCostSummary(
            tenant_name=tenant_name,
            sandbox_count=len(tenant_records),
        )

        for record in tenant_records:
            summary.cpu_cost += record.base_cost * (record.cpu_cores / max(record.cpu_cores + record.memory_gb + record.gpu_count + record.storage_gb, 0.001))
            summary.memory_cost += record.base_cost * (record.memory_gb / max(record.cpu_cores + record.memory_gb + record.gpu_count + record.storage_gb, 0.001))
            summary.gpu_cost += record.base_cost * (record.gpu_count / max(record.cpu_cores + record.memory_gb + record.gpu_count + record.storage_gb, 0.001))
            summary.storage_cost += record.base_cost * (record.storage_gb / max(record.cpu_cores + record.memory_gb + record.gpu_count + record.storage_gb, 0.001))
            summary.runtime_premium += record.runtime_cost
            summary.isolation_premium += record.isolation_cost
            summary.total_cost += record.total_cost

        if summary.sandbox_count > 0:
            summary.avg_cost_per_sandbox = summary.total_cost / summary.sandbox_count

        return summary


# ============================================================================
# Cost Analyzer
# ============================================================================

class CostAnalyzer:
    """Analyzes costs and generates reports."""

    def __init__(self, api_url: str = "http://localhost:8080", pricing: ResourcePricing = None):
        self.api_url = api_url.rstrip('/')
        self.calculator = CostCalculator(pricing)
        self.pricing = pricing or ResourcePricing()

    def fetch_sandbox_records(self) -> List[SandboxCostRecord]:
        """Fetch sandbox records from the API."""
        records = []
        try:
            resp = requests.get(f"{self.api_url}/api/v1/sandboxes", timeout=10)
            if resp.status_code == 200:
                data = resp.json()
                for item in data.get('items', []):
                    # Calculate duration
                    started_at = item.get('startedAt')
                    if started_at:
                        start = datetime.fromisoformat(started_at.replace('Z', '+00:00'))
                        duration = (datetime.now(start.tzinfo) - start).total_seconds() / 3600
                    else:
                        duration = 0

                    record = SandboxCostRecord(
                        sandbox_name=item.get('name', ''),
                        namespace=item.get('namespace', ''),
                        tenant_name=item.get('tenant', ''),
                        runtime_type=item.get('runtime', 'runc'),
                        node_name=item.get('nodeName', ''),
                        cpu_cores=self._parse_cpu(item.get('cpuRequest', '0')),
                        memory_gb=self._parse_memory(item.get('memoryRequest', '0')),
                        gpu_count=int(item.get('gpuRequest', '0')),
                        storage_gb=self._parse_storage(item.get('storageRequest', '0')),
                        duration_hours=duration,
                        started_at=started_at,
                    )
                    records.append(self.calculator.calculate_sandbox_cost(record))
        except requests.RequestException as e:
            logger.error(f"Failed to fetch sandbox records: {e}")

        return records

    def generate_billing_report(self, tenant_name: str = None) -> Dict[str, Any]:
        """Generate a billing report."""
        records = self.fetch_sandbox_records()

        if tenant_name:
            records = [r for r in records if r.tenant_name == tenant_name]

        # Group by tenant
        tenant_records = defaultdict(list)
        for record in records:
            tenant_records[record.tenant_name].append(record)

        # Generate summaries
        summaries = {}
        for tenant, tenant_recs in tenant_records.items():
            summaries[tenant] = asdict(self.calculator.calculate_tenant_summary(tenant_recs, tenant))

        # Calculate totals
        total_cost = sum(s['total_cost'] for s in summaries.values())
        total_sandboxes = len(records)

        # Cost by runtime type
        cost_by_runtime = defaultdict(float)
        for record in records:
            cost_by_runtime[record.runtime_type] += record.total_cost

        # Cost by isolation level
        cost_by_isolation = defaultdict(float)
        for record in records:
            level = self.calculator._get_isolation_level(record.runtime_type)
            cost_by_isolation[level] += record.total_cost

        return {
            'generated_at': datetime.now().isoformat(),
            'period': {
                'start': (datetime.now() - timedelta(days=30)).isoformat(),
                'end': datetime.now().isoformat(),
            },
            'total_cost': round(total_cost, 2),
            'total_sandboxes': total_sandboxes,
            'tenant_summaries': summaries,
            'cost_by_runtime': dict(cost_by_runtime),
            'cost_by_isolation': dict(cost_by_isolation),
            'avg_cost_per_sandbox': round(total_cost / max(total_sandboxes, 1), 4),
        }

    def generate_recommendations(self) -> List[OptimizationRecommendation]:
        """Generate cost optimization recommendations."""
        records = self.fetch_sandbox_records()
        recommendations = []

        # Group by tenant
        tenant_records = defaultdict(list)
        for record in records:
            tenant_records[record.tenant_name].append(record)

        for tenant, tenant_recs in tenant_records.items():
            # Check for expensive runtimes
            kata_records = [r for r in tenant_recs if r.runtime_type == 'kata-containers']
            if kata_records:
                kata_cost = sum(r.total_cost for r in kata_records)
                gvisor_savings = sum(
                    r.total_cost * (1 - self.pricing.runtime_premium['gvisor'] / self.pricing.runtime_premium['kata-containers'])
                    for r in kata_records
                )
                if gvisor_savings > 1.0:  # Only recommend if savings > $1
                    recommendations.append(OptimizationRecommendation(
                        type='RuntimeDowngrade',
                        severity='info',
                        message=f'Tenant {tenant} could save ${gvisor_savings:.2f}/month by using gVisor instead of Kata Containers for {len(kata_records)} sandboxes',
                        potential_savings=round(gvisor_savings, 2),
                        details={'tenant': tenant, 'current_runtime': 'kata-containers', 'suggested_runtime': 'gvisor'},
                    ))

            # Check for idle sandboxes
            idle_records = [r for r in tenant_recs if r.duration_hours > 24]
            if len(idle_records) > 5:
                recommendations.append(OptimizationRecommendation(
                    type='IdleSandboxes',
                    severity='warning',
                    message=f'Tenant {tenant} has {len(idle_records)} long-running sandboxes (>24h). Consider setting idle timeouts.',
                    potential_savings=0,
                    details={'tenant': tenant, 'idle_count': len(idle_records)},
                ))

        # Check for underutilized nodes
        try:
            resp = requests.get(f"{self.api_url}/api/v1/nodes", timeout=10)
            if resp.status_code == 200:
                for node_data in resp.json().get('items', []):
                    cpu_usage = node_data.get('cpuUsagePercent', 0)
                    if cpu_usage < 20 and node_data.get('sandboxCount', 0) == 0:
                        savings = self.pricing.cpu_per_core_hour * (node_data.get('cpuCapacity', 0) / 1000) * 24 * 30
                        recommendations.append(OptimizationRecommendation(
                            type='IdleNode',
                            severity='info',
                            message=f"Node {node_data.get('name')} is idle. Consider scaling down.",
                            potential_savings=round(savings, 2),
                            details={'node': node_data.get('name'), 'cpu_usage': cpu_usage},
                        ))
        except requests.RequestException:
            pass

        return recommendations

    def estimate_monthly_cost(self) -> Dict[str, float]:
        """Estimate monthly cost based on current usage."""
        records = self.fetch_sandbox_records()

        # Calculate daily cost
        daily_cost = sum(r.total_cost for r in records)

        # Extrapolate
        monthly_estimate = daily_cost * 30

        return {
            'current_daily_cost': round(daily_cost, 2),
            'monthly_estimate': round(monthly_estimate, 2),
            'quarterly_estimate': round(monthly_estimate * 3, 2),
            'yearly_estimate': round(monthly_estimate * 12, 2),
        }

    def _parse_cpu(self, cpu_str: str) -> float:
        """Parse CPU string to cores."""
        try:
            if cpu_str.endswith('m'):
                return float(cpu_str[:-1]) / 1000
            return float(cpu_str)
        except (ValueError, AttributeError):
            return 0.0

    def _parse_memory(self, mem_str: str) -> float:
        """Parse memory string to GB."""
        try:
            if mem_str.endswith('Gi'):
                return float(mem_str[:-2])
            elif mem_str.endswith('Mi'):
                return float(mem_str[:-2]) / 1024
            elif mem_str.endswith('Ki'):
                return float(mem_str[:-2]) / (1024 * 1024)
            return float(mem_str) / (1024 ** 3)
        except (ValueError, AttributeError):
            return 0.0

    def _parse_storage(self, storage_str: str) -> float:
        """Parse storage string to GB."""
        return self._parse_memory(storage_str)


# ============================================================================
# Report Generator
# ============================================================================

class ReportGenerator:
    """Generates formatted reports."""

    @staticmethod
    def format_billing_report(report: Dict[str, Any]) -> str:
        """Format a billing report as a readable string."""
        lines = [
            "=" * 60,
            "NexusBox Billing Report",
            "=" * 60,
            f"Generated: {report['generated_at']}",
            f"Period: {report['period']['start']} - {report['period']['end']}",
            "",
            f"Total Cost: ${report['total_cost']:.2f}",
            f"Total Sandboxes: {report['total_sandboxes']}",
            f"Avg Cost per Sandbox: ${report['avg_cost_per_sandbox']:.4f}",
            "",
            "-" * 40,
            "Cost by Runtime Type:",
        ]

        for runtime, cost in report.get('cost_by_runtime', {}).items():
            lines.append(f"  {runtime}: ${cost:.2f}")

        lines.append("")
        lines.append("-" * 40)
        lines.append("Cost by Isolation Level:")

        for level, cost in report.get('cost_by_isolation', {}).items():
            lines.append(f"  {level}: ${cost:.2f}")

        lines.append("")
        lines.append("-" * 40)
        lines.append("Tenant Summaries:")

        for tenant, summary in report.get('tenant_summaries', {}).items():
            lines.append(f"")
            lines.append(f"  Tenant: {tenant}")
            lines.append(f"    Total Cost: ${summary['total_cost']:.2f}")
            lines.append(f"    Sandboxes: {summary['sandbox_count']}")
            lines.append(f"    Avg Cost/Sandbox: ${summary['avg_cost_per_sandbox']:.4f}")
            lines.append(f"    Runtime Premium: ${summary['runtime_premium']:.2f}")
            lines.append(f"    Isolation Premium: ${summary['isolation_premium']:.2f}")

        lines.append("")
        lines.append("=" * 60)
        return "\n".join(lines)

    @staticmethod
    def format_recommendations(recommendations: List[OptimizationRecommendation]) -> str:
        """Format recommendations as a readable string."""
        lines = [
            "=" * 60,
            "NexusBox Cost Optimization Recommendations",
            "=" * 60,
            "",
        ]

        if not recommendations:
            lines.append("No recommendations at this time.")
        else:
            total_savings = sum(r.potential_savings for r in recommendations)
            lines.append(f"Total Potential Savings: ${total_savings:.2f}/month")
            lines.append("")

            for i, rec in enumerate(recommendations, 1):
                lines.append(f"{i}. [{rec.severity.upper()}] {rec.message}")
                if rec.potential_savings > 0:
                    lines.append(f"   Potential Savings: ${rec.potential_savings:.2f}/month")
                if rec.details:
                    lines.append(f"   Details: {json.dumps(rec.details, indent=2)}")
                lines.append("")

        lines.append("=" * 60)
        return "\n".join(lines)


# ============================================================================
# Main Entry Point
# ============================================================================

def main():
    parser = argparse.ArgumentParser(description='NexusBox Cost Analysis Tool')
    parser.add_argument('--api-url', default='http://localhost:8080', help='NexusBox API URL')
    parser.add_argument('--tenant', help='Generate report for specific tenant')
    parser.add_argument('--recommendations', action='store_true', help='Generate optimization recommendations')
    parser.add_argument('--estimate', action='store_true', help='Estimate monthly cost')
    parser.add_argument('--json', action='store_true', help='Output as JSON')
    args = parser.parse_args()

    analyzer = CostAnalyzer(api_url=args.api_url)

    if args.estimate:
        result = analyzer.estimate_monthly_cost()
        if args.json:
            print(json.dumps(result, indent=2))
        else:
            print(f"Daily Cost: ${result['current_daily_cost']:.2f}")
            print(f"Monthly Estimate: ${result['monthly_estimate']:.2f}")
            print(f"Quarterly Estimate: ${result['quarterly_estimate']:.2f}")
            print(f"Yearly Estimate: ${result['yearly_estimate']:.2f}")
        return

    if args.recommendations:
        recs = analyzer.generate_recommendations()
        if args.json:
            print(json.dumps([asdict(r) for r in recs], indent=2))
        else:
            print(ReportGenerator.format_recommendations(recs))
        return

    # Default: generate billing report
    report = analyzer.generate_billing_report(tenant_name=args.tenant)
    if args.json:
        print(json.dumps(report, indent=2))
    else:
        print(ReportGenerator.format_billing_report(report))


if __name__ == '__main__':
    main()
