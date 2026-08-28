#!/usr/bin/env python3
"""Fail if a generated file was edited by hand.

The Zobrist tables and the conformance vectors are generated. Editing one
directly -- to make a failing test pass, say -- would silently break the
contract between the two engines. This regenerates into a scratch directory and
compares, so it needs no version control to work.
"""
import filecmp
import pathlib
import shutil
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parents[2]

GENERATED = [
    "rules-spec/zobrist_table.json",
    "rules-spec/vectors",
    "sente-ios/Packages/GoKit/Sources/GoKit/Zobrist+Table.swift",
    "sente-server/internal/rules/zobrist_table.go",
]


def snapshot(destination):
    for relative in GENERATED:
        source = ROOT / relative
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        if source.is_dir():
            shutil.copytree(source, target)
        else:
            shutil.copy2(source, target)


def differences(left, right, relative):
    a, b = left / relative, right / relative
    if a.is_dir():
        found = []
        for path in sorted(a.rglob("*")):
            if not path.is_file():
                continue
            other = b / path.relative_to(a)
            if not other.exists():
                found.append(f"{relative}/{path.relative_to(a)} (removed by regeneration)")
            elif not filecmp.cmp(path, other, shallow=False):
                found.append(f"{relative}/{path.relative_to(a)}")
        for path in sorted(b.rglob("*")):
            if path.is_file() and not (a / path.relative_to(b)).exists():
                found.append(f"{relative}/{path.relative_to(b)} (missing on disk)")
        return found
    return [] if filecmp.cmp(a, b, shallow=False) else [relative]


def main():
    with tempfile.TemporaryDirectory() as tmp:
        before = pathlib.Path(tmp) / "before"
        before.mkdir()
        snapshot(before)

        for tool in ("gen_zobrist.py", "gen_vectors.py"):
            subprocess.run([sys.executable, str(ROOT / "rules-spec" / "tools" / tool)],
                           check=True, stdout=subprocess.DEVNULL)

        drifted = []
        for relative in GENERATED:
            drifted.extend(differences(before, ROOT, relative))

        # Put the working tree back the way it was found.
        for relative in GENERATED:
            source, target = before / relative, ROOT / relative
            if source.is_dir():
                shutil.rmtree(target)
                shutil.copytree(source, target)
            else:
                shutil.copy2(source, target)

    if drifted:
        print("Generated files do not match their generators:")
        for path in drifted:
            print(f"  {path}")
        print("\nEdit rules-spec/tools/*.py and run `make spec` instead of editing these by hand.")
        return 1
    print(f"no drift: {len(GENERATED)} generated paths match their generators")
    return 0


if __name__ == "__main__":
    sys.exit(main())
