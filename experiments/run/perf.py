#!/usr/bin/env python3
"""Run the public performance tests and append the results to a log.

For each test it runs the professor's run_demo.py unmodified (so the numbers
are the official public metrics), takes the median of each metric across the
runs, and appends ONE row to a CSV log together with what was run: when,
which commit, which server mode, the test parameters and the network profile.
Raw per-run CSV/JSON from the harness are kept under
experiments/results/runs/<timestamp>_<mode>_<test>/.

Examples (from the repo root, venv active):

  python3 experiments/run/perf.py t1 t4                    # live server, 3 runs each
  python3 experiments/run/perf.py --mode naive t3          # baseline for before/after
  python3 experiments/run/perf.py --mode starter t3        # the professor's Python server
  python3 experiments/run/perf.py t5                       # t2 under delay_loss.sh (needs sudo)
  python3 experiments/run/perf.py t2 --profile bandwidth_2mbit --clients 400
  python3 experiments/run/perf.py all --note "window=4, sticky degraded 30s"

The log is experiments/results/perf_log.csv unless --out says otherwise.
Duplicate, stale and connection-error counts are the WORST run, not the
median: one bad run matters.
"""

from __future__ import annotations

import argparse
import csv
import datetime as dt
import json
import os
import platform
import shutil
import socket
import statistics
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
HARNESS = ROOT / "harness"
PROFILES = HARNESS / "network_profiles"
PORT = 8765  # run_demo.py's default

# PUBLIC_TESTS.md. t5 is t2's workload under a network profile.
TESTS = {
    "t1": {"clients": 20, "duration": 20},
    "t2": {"clients": 100, "duration": 40},
    "t3": {"clients": 100, "duration": 60, "slow_percent": 10, "slow_ms": 500},
    "t4": {"clients": 100, "duration": 60, "disconnect_percent": 20, "disconnect_at": 15, "disconnect_seconds": 5},
    "t5": {"clients": 100, "duration": 40, "profile": "delay_loss"},
}

# (CSV column, path in run_demo's per-run JSON, how to combine runs)
METRICS = [
    ("p50_ms", "p50_ms", "median"),
    ("p95_ms", "p95_ms", "median"),
    ("p99_ms", "p99_ms", "median"),
    ("fresh_pct", "fresh_pct", "median"),
    ("fresh_final_pct", "fresh_final_pct", "median"),
    ("completeness_pct", "final_completeness_pct", "median"),
    ("coverage_pct", "normal_update_coverage_pct", "median"),
    ("normal_p95_ms", "normal_clients.p95_ms", "median"),
    ("slow_p50_ms", "slow_clients.p50_ms", "median"),
    ("slow_p95_ms", "slow_clients.p95_ms", "median"),
    ("slow_fresh_pct", "slow_clients.fresh_pct", "median"),
    ("recovery_p95_ms", "recovery_p95_ms", "median"),
    ("duplicates_max", "duplicates", "max"),
    ("stale_max", "stale", "max"),
    ("conn_errors_max", "connection_errors", "max"),
]

INFO_COLUMNS = [
    "timestamp", "test", "mode", "status", "runs_ok", "runs", "clients", "duration",
    "slow_percent", "slow_ms", "disconnect_percent", "profile",
    "commit", "branch", "dirty", "host", "cpus", "kernel", "note", "raw_dir",
]
COLUMNS = INFO_COLUMNS + [m[0] for m in METRICS]


def sh(*cmd: str) -> str:
    try:
        return subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True, check=True).stdout.strip()
    except (OSError, subprocess.CalledProcessError):
        return ""


def git_info() -> dict:
    return {
        "commit": sh("git", "rev-parse", "--short", "HEAD") or "unknown",
        "branch": sh("git", "rev-parse", "--abbrev-ref", "HEAD") or "unknown",
        # Uncommitted changes mean the commit alone does not identify the code.
        "dirty": "yes" if sh("git", "status", "--porcelain", "--untracked-files=no") else "no",
    }


def port_free(port: int) -> bool:
    with socket.socket() as s:
        return s.connect_ex(("127.0.0.1", port)) != 0


def lo_qdisc() -> str:
    return sh("tc", "qdisc", "show", "dev", "lo")


def nested(d: dict, path: str):
    for key in path.split("."):
        if not isinstance(d, dict) or key not in d:
            return None
        d = d[key]
    return d


def combine(values: list, how: str):
    values = [float(v) for v in values if v is not None]
    if not values:
        return ""
    if how == "max":
        return int(max(values))  # counts: duplicates, stale, connection errors
    return round(statistics.median(values), 2)


def harness_dir(mode: str, tmp: Path) -> Path:
    """Where run_demo.py runs. For --mode starter, a scratch copy whose
    solution_server.py is the professor's original Python server."""
    if mode != "starter":
        return HARNESS
    for name in ("run_demo.py", "load_clients.py", "caption_source.py", "analyse_results.py"):
        shutil.copy(HARNESS / name, tmp / name)
    shutil.copy(HARNESS / "starter_server.py", tmp / "solution_server.py")
    return tmp


def run_test(name: str, params: dict, args, info: dict, out_root: Path) -> dict:
    stamp = dt.datetime.now().strftime("%Y%m%d-%H%M%S")
    raw_dir = out_root / "runs" / f"{stamp}_{args.mode}_{name}"
    raw_dir.mkdir(parents=True, exist_ok=True)
    profile = params.get("profile", "")

    cmd = [sys.executable, "run_demo.py", "--quiet-server", "--runs", str(args.runs),
           "--clients", str(params["clients"]), "--duration", str(params["duration"]),
           "--output-stem", str(raw_dir / name)]
    for flag in ("slow_percent", "slow_ms", "disconnect_percent", "disconnect_at", "disconnect_seconds"):
        if flag in params:
            cmd += ["--" + flag.replace("_", "-"), str(params[flag])]

    env = dict(os.environ, DISTRIBUTARY_MODE=args.mode if args.mode != "starter" else "")
    row = {c: "" for c in COLUMNS}
    row.update(info)
    row.update({
        "timestamp": dt.datetime.now().isoformat(timespec="seconds"), "test": name, "mode": args.mode,
        "runs": args.runs, "clients": params["clients"], "duration": params["duration"],
        "slow_percent": params.get("slow_percent", ""), "slow_ms": params.get("slow_ms", ""),
        "disconnect_percent": params.get("disconnect_percent", ""), "profile": profile or "none",
        "note": args.note, "raw_dir": str(raw_dir.relative_to(ROOT)),
    })

    print(f"\n=== {name} ({args.mode}{', ' + profile if profile else ''}): "
          f"{params['clients']} clients, {params['duration']} s, {args.runs} runs", flush=True)
    with tempfile.TemporaryDirectory() as tmp:
        cwd = harness_dir(args.mode, Path(tmp))
        if profile and subprocess.run(["sudo", str(PROFILES / f"{profile}.sh"), "lo"]).returncode != 0:
            subprocess.run(["sudo", str(PROFILES / "reset.sh"), "lo"], check=False)
            print(f"  FAILED: could not apply {profile} (on Ubuntu: sudo apt install iproute2; "
                  f"netem profiles need the sch_netem kernel module)", flush=True)
            row.update({"status": "FAILED", "runs_ok": 0})
            return row
        try:
            # Show run_demo's progress live and keep a copy next to the raw results.
            output = []
            with (raw_dir / "run_demo.log").open("w") as log:
                proc = subprocess.Popen(cmd, cwd=cwd, env=env, text=True,
                                        stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
                for line in proc.stdout:
                    print("  " + line, end="", flush=True)
                    log.write(line)
                    output.append(line)
                proc.wait()
        finally:
            if profile:
                subprocess.run(["sudo", str(PROFILES / "reset.sh"), "lo"], check=False)

    summaries = []
    for f in sorted(raw_dir.glob(f"{name}*.json")):
        try:
            summaries.append(json.loads(f.read_text()))
        except (OSError, json.JSONDecodeError):
            pass

    row["runs_ok"] = len(summaries)
    row["status"] = "ok" if proc.returncode == 0 and len(summaries) == args.runs else "FAILED"
    for column, path, how in METRICS:
        row[column] = combine([nested(s, path) for s in summaries], how)
    if row["status"] != "ok":
        print(f"  FAILED: run_demo exited {proc.returncode} with {len(summaries)}/{args.runs} runs; "
              f"see {raw_dir / 'run_demo.log'}", flush=True)
    return row


def append(out: Path, rows: list[dict]):
    new = not out.exists() or out.stat().st_size == 0
    if not new:
        with out.open(newline="") as f:
            header = next(csv.reader(f), [])
        if header != COLUMNS:
            raise SystemExit(f"{out} has different columns; pass --out with a new file name")
    out.parent.mkdir(parents=True, exist_ok=True)
    with out.open("a", newline="") as f:
        w = csv.DictWriter(f, fieldnames=COLUMNS)
        if new:
            w.writeheader()
        w.writerows(rows)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("tests", nargs="+", help="t1 t2 t3 t4 t5, or all")
    ap.add_argument("--mode", default="live", choices=["live", "naive", "starter"],
                    help="live/naive: our binary via DISTRIBUTARY_MODE; starter: professor's Python server")
    ap.add_argument("--runs", type=int, default=3)
    ap.add_argument("--clients", type=int, help="override the test's client count")
    ap.add_argument("--duration", type=float, help="override the test's duration (s)")
    ap.add_argument("--profile", help="network profile from harness/network_profiles (no .sh), e.g. "
                                      "delay_100ms, delay_loss, loss_3pct, reorder, bandwidth_2mbit")
    ap.add_argument("--note", default="", help="free text saved with the row, e.g. what you changed")
    ap.add_argument("--out", default=str(ROOT / "experiments" / "results" / "perf_log.csv"))
    ap.add_argument("--no-build", action="store_true", help="skip `make build`")
    args = ap.parse_args()

    names = list(TESTS) if args.tests == ["all"] else args.tests
    unknown = [n for n in names if n not in TESTS]
    if unknown:
        ap.error(f"unknown test(s): {', '.join(unknown)}")
    if args.profile and not (PROFILES / f"{args.profile}.sh").exists():
        ap.error(f"no such profile: {PROFILES / (args.profile + '.sh')}")

    # Refuse to measure on a dirty bench.
    if not port_free(PORT):
        raise SystemExit(f"port {PORT} is in use: stop the server holding it (ss -ltnp | grep {PORT})")
    if any(q in lo_qdisc() for q in ("netem", "tbf")):
        raise SystemExit("a tc profile is still applied to lo: sudo harness/network_profiles/reset.sh lo")

    if not args.no_build and args.mode != "starter":
        subprocess.run(["make", "build"], cwd=ROOT, check=True)

    info = git_info()
    info.update({"host": platform.node(), "cpus": os.cpu_count(), "kernel": platform.release()})
    out = Path(args.out)
    rows = []
    for name in names:
        params = dict(TESTS[name])
        for key in ("clients", "duration", "profile"):
            if getattr(args, key) is not None:
                params[key] = getattr(args, key)
        row = run_test(name, params, args, info, out.parent)
        append(out, [row])  # append as we go, so a later failure keeps earlier rows
        rows.append(row)

    print(f"\nAppended {len(rows)} row(s) to {out}")
    print(f"{'test':5} {'mode':8} {'status':7} {'complete%':>9} {'p95 ms':>8} {'slow p50 ms':>11} "
          f"{'dup':>4} {'stale':>5} {'conn err':>8}")
    for r in rows:
        print(f"{r['test']:5} {r['mode']:8} {r['status']:7} {r['completeness_pct']!s:>9} {r['p95_ms']!s:>8} "
              f"{r['slow_p50_ms']!s:>11} {r['duplicates_max']!s:>4} {r['stale_max']!s:>5} {r['conn_errors_max']!s:>8}")
    if info["dirty"] == "yes":
        print("\nNote: uncommitted changes; the commit column does not fully identify the code measured.")
    sys.exit(0 if all(r["status"] == "ok" for r in rows) else 1)


if __name__ == "__main__":
    main()
