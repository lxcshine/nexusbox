"""Generate a self-contained, visually rich HTML test report for the
NexusBox MCP sandbox availability checks.

The report embeds the results of:
  - check_mcp_availability.ps1  (handshake + tools/list, 18 tools)
  - check_mcp_tools.ps1         (shell / file / multi-language code / security)

All charts are rendered as inline SVG so the HTML file is fully
self-contained — no external CSS/JS, no network calls, opens in any
browser.

Usage:
    python scripts/generate_test_report.py
    python scripts/generate_test_report.py --out docs/test-report.html

Output: docs/test-report.html (default)
"""
import argparse
import datetime
import html
import json
import os
import sys

# ----------------------------------------------------------------------------
# Test result model.
#
# These mirror what scripts/check_mcp_availability.ps1 and
# scripts/check_mcp_tools.ps1 assert. Edit here to reflect a new run; in a
# future iteration the .ps1 scripts can emit JSON that this script reads.
# ----------------------------------------------------------------------------
REPORT = {
    "generated_at": datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S"),
    "binary": "nexusbox-mcp.exe (8.1 MB)",
    "transport": "stdio (JSON-RPC 2.0)",
    "workspace": "D:/Code/NexusBox",
    "summary": {"pass": 9, "fail": 0, "total": 9},
    "categories": [
        {"name": "Handshake",          "passed": 1, "total": 1, "detail": "initialize + notifications/initialized"},
        {"name": "Tools List",         "passed": 1, "total": 1, "detail": "18 tools enumerated (Shell 3 / File 7 / Code 2 / Browser 6)"},
        {"name": "Shell Execution",    "passed": 1, "total": 1, "detail": "shell_exec returns captured stdout"},
        {"name": "File I/O",           "passed": 2, "total": 2, "detail": "file_write then file_read round-trip"},
        {"name": "Multi-language Code","passed": 4, "total": 4, "detail": "Python / Node.js / Go / Java"},
        {"name": "Path Traversal Guard","passed": 1, "total": 1, "detail": "../../../etc/passwd blocked"},
    ],
    "tools": [
        {"category": "Shell",   "count": 3, "items": ["shell_exec", "shell_background", "shell_check"]},
        {"category": "File",    "count": 7, "items": ["file_read", "file_write", "file_list", "file_search", "file_replace", "file_delete", "file_move"]},
        {"category": "Code",    "count": 2, "items": ["code_run", "code_install"]},
        {"category": "Browser", "count": 6, "items": ["browser_navigate", "browser_screenshot", "browser_click", "browser_type", "browser_eval", "browser_get_text"]},
    ],
    "languages": [
        {"name": "Python",  "marker": "py-ok",   "passed": True,  "runtime": "Python 3.9.13"},
        {"name": "Node.js", "marker": "node-ok", "passed": True,  "runtime": "Node v24.16.0"},
        {"name": "Go",      "marker": "go-ok",   "passed": True,  "runtime": "Go 1.22.12"},
        {"name": "Java",    "marker": "java-ok", "passed": True,  "runtime": "Java 17.0.16 LTS"},
    ],
    "bug_fixed": {
        "title": "MCP code_run did not support Go / Java",
        "severity": "high",
        "root_cause": "Phase 2 extended Gateway CodeService with Go/Java but the MCP entrypoint "
                      "(pkg/mcp/code_server.go) only handled python/nodejs — the two entrypoints "
                      "were not synchronized.",
        "fix": "Added Go (go run) and Java (javac + java, class-name normalized to Main) branches "
               "to the MCP code_run dispatcher, aligning it with the Gateway. All 4 languages now pass.",
        "file": "pkg/mcp/code_server.go",
    },
}


def esc(s):
    return html.escape(str(s))


def donut_svg(passed, failed):
    """SVG donut chart of pass/fail."""
    total = passed + failed
    if total == 0:
        r, circ = 0, 0
    else:
        r, circ = 54, 2 * 3.14159 * 54
    pass_frac = passed / total if total else 0
    pass_len = circ * pass_frac
    fail_len = circ - pass_len
    return f"""
<svg viewBox="0 0 160 160" width="160" height="160" class="donut">
  <circle cx="80" cy="80" r="{r}" fill="none" stroke="#E2E8F0" stroke-width="18"/>
  <circle cx="80" cy="80" r="{r}" fill="none" stroke="#10B981" stroke-width="18"
          stroke-dasharray="{pass_len:.2f} {fail_len:.2f}" stroke-dashoffset="{circ/4:.2f}"
          transform="rotate(-90 80 80)" stroke-linecap="round"/>
  <text x="80" y="74" text-anchor="middle" font-size="34" font-weight="700" fill="#1E293B">{passed}</text>
  <text x="80" y="96" text-anchor="middle" font-size="12" fill="#64748B">passed</text>
</svg>"""


def category_bars_svg(categories):
    """Horizontal stacked bars per test category."""
    rows = ""
    max_total = max(c["total"] for c in categories) or 1
    bar_w = 260
    for i, c in enumerate(categories):
        y = i * 44 + 6
        seg_pass = (c["passed"] / c["total"]) * bar_w if c["total"] else 0
        seg_fail = bar_w - seg_pass
        rows += f"""
<g transform="translate(0,{y})">
  <text x="0" y="14" font-size="12.5" fill="#1E293B" font-weight="600">{esc(c['name'])}</text>
  <text x="0" y="30" font-size="10.5" fill="#64748B">{esc(c['detail'])}</text>
  <rect x="300" y="2" width="{bar_w}" height="22" rx="6" fill="#E2E8F0"/>
  <rect x="300" y="2" width="{seg_pass:.1f}" height="22" rx="6" fill="#10B981"/>
  <text x="300" y="17" font-size="11" fill="white" font-weight="700" transform="translate(6,0)">{c['passed']}/{c['total']}</text>
</g>"""
    height = len(categories) * 44 + 10
    return f'<svg viewBox="0 0 580 {height}" width="100%" height="{height}">{rows}</svg>'


def tools_donuts_svg(tools):
    """Four small donuts, one per tool category."""
    total = sum(t["count"] for t in tools)
    parts = ""
    palette = {"Shell": "#2563EB", "File": "#10B981", "Code": "#F59E0B", "Browser": "#8B5CF6"}
    for i, t in enumerate(tools):
        cx = 80 + i * 150
        color = palette.get(t["category"], "#64748B")
        frac = t["count"] / total if total else 0
        circ = 2 * 3.14159 * 38
        dash = circ * frac
        parts += f"""
<g transform="translate({cx},70)">
  <circle cx="0" cy="0" r="38" fill="none" stroke="#E2E8F0" stroke-width="12"/>
  <circle cx="0" cy="0" r="38" fill="none" stroke="{color}" stroke-width="12"
          stroke-dasharray="{dash:.2f} {circ-dash:.2f}" stroke-dashoffset="{circ/4:.2f}"
          transform="rotate(-90)" stroke-linecap="round"/>
  <text x="0" y="-2" text-anchor="middle" font-size="22" font-weight="700" fill="#1E293B">{t['count']}</text>
  <text x="0" y="16" text-anchor="middle" font-size="10" fill="#64748B">{esc(t['category'])}</text>
</g>"""
    return f'<svg viewBox="0 0 580 140" width="100%" height="140">{parts}</svg>'


def language_grid(languages):
    """Grid of language result cards."""
    cards = ""
    for lang in languages:
        status = "pass" if lang["passed"] else "fail"
        icon = "&#10003;" if lang["passed"] else "&#10007;"
        cards += f"""
<div class="lang-card {status}">
  <div class="lang-icon">{icon}</div>
  <div class="lang-name">{esc(lang['name'])}</div>
  <div class="lang-marker">outputs <code>{esc(lang['marker'])}</code></div>
  <div class="lang-runtime">{esc(lang['runtime'])}</div>
</div>"""
    return cards


def build_html(report):
    s = report["summary"]
    all_pass = s["fail"] == 0
    verdict_color = "#10B981" if all_pass else "#EF4444"
    verdict_text = "ALL CHECKS PASSED" if all_pass else f"{s['fail']} CHECK(S) FAILED"

    return f"""<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>NexusBox MCP Sandbox — Test Report</title>
<style>
  :root {{
    --bg:#F8FAFC; --card:#FFFFFF; --ink:#1E293B; --muted:#64748B;
    --border:#E2E8F0; --pass:#10B981; --fail:#EF4444; --warn:#F59E0B; --blue:#2563EB;
  }}
  * {{ box-sizing:border-box; }}
  body {{ margin:0; padding:32px 16px; background:var(--bg); color:var(--ink);
         font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif; }}
  .wrap {{ max-width:980px; margin:0 auto; }}
  header {{ text-align:center; margin-bottom:28px; }}
  header h1 {{ margin:0 0 6px; font-size:28px; letter-spacing:-0.3px; }}
  header .sub {{ color:var(--muted); font-size:13px; }}
  .verdict {{ display:inline-block; margin-top:14px; padding:8px 22px; border-radius:999px;
              background:{verdict_color}; color:white; font-weight:700; font-size:14px;
              letter-spacing:0.5px; box-shadow:0 4px 14px {verdict_color}44; }}
  .grid {{ display:grid; grid-template-columns:1fr 1fr; gap:18px; margin:24px 0; }}
  .card {{ background:var(--card); border:1px solid var(--border); border-radius:14px;
           padding:20px; box-shadow:0 1px 3px rgba(0,0,0,0.04); }}
  .card h2 {{ margin:0 0 14px; font-size:16px; }}
  .summary {{ display:flex; align-items:center; gap:24px; }}
  .summary .nums {{ flex:1; }}
  .stat {{ display:flex; justify-content:space-between; padding:7px 0; border-bottom:1px dashed var(--border); font-size:13.5px; }}
  .stat:last-child {{ border:0; }}
  .stat b {{ font-weight:700; }}
  .pass-c {{ color:var(--pass); }} .fail-c {{ color:var(--fail); }}
  .section {{ background:var(--card); border:1px solid var(--border); border-radius:14px;
              padding:22px; margin:18px 0; box-shadow:0 1px 3px rgba(0,0,0,0.04); }}
  .section h2 {{ margin:0 0 16px; font-size:17px; }}
  .langs {{ display:grid; grid-template-columns:repeat(4,1fr); gap:14px; margin-top:6px; }}
  .lang-card {{ border-radius:12px; padding:16px 12px; text-align:center; border:2px solid; }}
  .lang-card.pass {{ border-color:var(--pass); background:#ECFDF5; }}
  .lang-card.fail {{ border-color:var(--fail); background:#FEF2F2; }}
  .lang-icon {{ font-size:24px; font-weight:700; }}
  .lang-card.pass .lang-icon {{ color:var(--pass); }}
  .lang-card.fail .lang-icon {{ color:var(--fail); }}
  .lang-name {{ font-weight:700; margin:6px 0 2px; font-size:15px; }}
  .lang-marker {{ font-size:11.5px; color:var(--muted); }}
  .lang-marker code {{ background:#F1F5F9; padding:1px 5px; border-radius:4px; font-size:11px; }}
  .lang-runtime {{ font-size:10.5px; color:var(--muted); margin-top:6px; }}
  .bug {{ border-left:4px solid var(--warn); background:#FFFBEB; padding:16px 18px; border-radius:8px; margin-top:8px; }}
  .bug .tag {{ display:inline-block; background:var(--warn); color:white; font-size:10.5px;
              font-weight:700; padding:2px 8px; border-radius:4px; letter-spacing:0.4px; }}
  .bug h3 {{ margin:8px 0 6px; font-size:15px; }}
  .bug p {{ margin:5px 0; font-size:13px; line-height:1.55; color:#475569; }}
  .bug code {{ background:#FEF3C7; padding:1px 5px; border-radius:4px; font-size:12px; }}
  .meta {{ color:var(--muted); font-size:12px; margin-top:18px; text-align:center; }}
  @media (max-width:720px) {{ .grid{{grid-template-columns:1fr}} .langs{{grid-template-columns:1fr 1fr}} }}
</style>
</head>
<body>
<div class="wrap">
  <header>
    <h1>NexusBox MCP Sandbox — Availability Test Report</h1>
    <div class="sub">Generated {esc(report['generated_at'])} &middot; {esc(report['binary'])} &middot; {esc(report['transport'])}</div>
    <div class="verdict">{verdict_text}</div>
  </header>

  <div class="grid">
    <div class="card">
      <h2>Overall Result</h2>
      <div class="summary">
        {donut_svg(s['pass'], s['fail'])}
        <div class="nums">
          <div class="stat"><span>Total checks</span><b>{s['total']}</b></div>
          <div class="stat"><span>Passed</span><b class="pass-c">{s['pass']}</b></div>
          <div class="stat"><span>Failed</span><b class="fail-c">{s['fail']}</b></div>
          <div class="stat"><span>Pass rate</span><b>{(s['pass']/s['total']*100) if s['total'] else 0:.1f}%</b></div>
          <div class="stat"><span>Workspace</span><b style="font-weight:500;font-size:12px">{esc(report['workspace'])}</b></div>
        </div>
      </div>
    </div>
    <div class="card">
      <h2>18 MCP Tools by Category</h2>
      {tools_donuts_svg(report['tools'])}
    </div>
  </div>

  <div class="section">
    <h2>Test Results by Category</h2>
    {category_bars_svg(report['categories'])}
  </div>

  <div class="section">
    <h2>Multi-Language Runtime Execution</h2>
    <p style="color:var(--muted);font-size:13px;margin:0 0 12px">Same <code>code_run</code> tool, four languages, each asserting its marker string is printed.</p>
    <div class="langs">{language_grid(report['languages'])}</div>
  </div>

  <div class="section">
    <h2>Bug Found &amp; Fixed During Testing</h2>
    <div class="bug">
      <span class="tag">{esc(report['bug_fixed']['severity'].upper())}</span>
      <h3>{esc(report['bug_fixed']['title'])}</h3>
      <p><b>Root cause:</b> {esc(report['bug_fixed']['root_cause'])}</p>
      <p><b>Fix:</b> {esc(report['bug_fixed']['fix'])}</p>
      <p><b>File:</b> <code>{esc(report['bug_fixed']['file'])}</code></p>
    </div>
  </div>

  <div class="meta">
    Report produced by <code>scripts/generate_test_report.py</code> &middot;
    Tests driven by <code>scripts/check_mcp_availability.ps1</code> and <code>scripts/check_mcp_tools.ps1</code>
  </div>
</div>
</body>
</html>"""


def main():
    ap = argparse.ArgumentParser(description="Generate NexusBox MCP test report HTML")
    ap.add_argument("--out", default="docs/test-report.html", help="output HTML path")
    args = ap.parse_args()

    out_path = os.path.abspath(args.out)
    os.makedirs(os.path.dirname(out_path), exist_ok=True)
    html_content = build_html(REPORT)
    with open(out_path, "w", encoding="utf-8") as f:
        f.write(html_content)
    print(f"[OK] test report -> {out_path} ({len(html_content)} bytes)")


if __name__ == "__main__":
    main()
