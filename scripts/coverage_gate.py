#!/usr/bin/env python3
"""Enforce the coverage gates from docs/01 §5.3.

Two different gates, because the requirement is two different things:

  NFR-Q1  the rules engine -- 95% of lines, 90% of branches
  NFR-Q2  everything else  -- 80%

An earlier version of this script applied the engine's gate to the whole module.
That is stricter than the requirement, and it quietly pushed people into writing
thin tests for unreachable error branches to make a number move. The gate should
encode the requirement, not a number someone picked.
"""
import argparse
import json
import os
import pathlib
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
GENERATED = ("Zobrist+Table.swift", "zobrist_table.go")

ENGINE_LINE_GATE = 95.0
ENGINE_BRANCH_GATE = 90.0
OVERALL_GATE = 80.0


def check_swift():
    """GoKit is the rules engine, so the whole package is held to NFR-Q1."""
    package = ROOT / "sente-ios" / "Packages" / "GoKit"
    subprocess.run(["swift", "test", "--enable-code-coverage"], cwd=package, check=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    path = subprocess.run(["swift", "test", "--show-codecov-path"], cwd=package,
                          check=True, capture_output=True, text=True).stdout.strip()
    report = json.load(open(path))

    rows, total = [], 0
    for entry in report["data"][0]["files"]:
        name = os.path.basename(entry["filename"])
        if "Tests" in entry["filename"] or name in GENERATED:
            continue
        summary = entry["summary"]
        count = summary["lines"]["count"]
        rows.append((count, summary["lines"]["percent"], summary["regions"]["percent"]))
        total += count

    lines = sum(c * p for c, p, _ in rows) / total
    regions = sum(c * r for c, _, r in rows) / total
    return [
        ("GoKit engine (Swift)", "line", lines, ENGINE_LINE_GATE),
        ("GoKit engine (Swift)", "branch", regions, ENGINE_BRANCH_GATE),
    ]


def go_profile():
    module = ROOT / "sente-server"
    profile = module / ".coverage.out"
    # -coverpkg counts code exercised from another package, such as the rules
    # engine driven by the game state machine.
    subprocess.run(["go", "test", "./...", "-coverpkg=./internal/...",
                    f"-coverprofile={profile}"], cwd=module, check=True, stdout=subprocess.DEVNULL)
    text = profile.read_text()
    profile.unlink(missing_ok=True)
    return text


def check_go():
    """Splits the profile by package: the engine gets NFR-Q1, the rest NFR-Q2."""
    # With -coverpkg every test binary reports every package, so each block appears
    # once per binary. Merge by taking the highest count, the way `go tool cover`
    # does; summing the duplicates instead counts the misses several times over.
    blocks = {}
    for line in go_profile().splitlines():
        # "path/file.go:12.34,15.6 3 1" -- the path has no spaces but does have
        # colons, so the location is split from the right.
        parts = line.split()
        if len(parts) != 3 or not parts[1].isdigit() or not parts[2].isdigit():
            continue
        location, statements, hits = parts[0], int(parts[1]), int(parts[2])
        previous = blocks.get(location)
        if previous is None or hits > previous[1]:
            blocks[location] = (statements, hits)

    engine_hit = engine_total = other_hit = other_total = 0
    for location, (statements, hits) in blocks.items():
        path = location.rsplit(":", 1)[0]
        if any(g in path for g in GENERATED):
            continue
        if "/internal/rules/" in path:
            engine_total += statements
            engine_hit += statements if hits > 0 else 0
        else:
            other_total += statements
            other_hit += statements if hits > 0 else 0

    results = []
    if engine_total:
        results.append(("rules engine (Go)", "statement", engine_hit / engine_total * 100,
                        ENGINE_LINE_GATE))
    if other_total:
        results.append(("server, rest (Go)", "statement", other_hit / other_total * 100,
                        OVERALL_GATE))
    combined = (engine_hit + other_hit) / (engine_total + other_total) * 100
    results.append(("server, all (Go)", "statement", combined, OVERALL_GATE))
    return results


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("target", choices=["swift", "go", "all"])
    args = parser.parse_args()

    results = []
    if args.target in ("swift", "all"):
        results.extend(check_swift())
    if args.target in ("go", "all"):
        results.extend(check_go())

    failed = False
    for label, kind, value, gate in results:
        ok = value >= gate
        failed |= not ok
        print(f"{'PASS' if ok else 'FAIL'}  {label:<22} {kind:<10} {value:5.1f}%  (gate {gate:.0f}%)")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
