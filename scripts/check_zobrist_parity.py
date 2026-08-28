#!/usr/bin/env python3
"""Assert both engines carry byte-identical Zobrist constants.

If these ever diverge, board hashes stop comparing and every desync check in the
system silently becomes a no-op. Cheap to check, catastrophic to miss.
"""
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]
SOURCES = {
    "swift": ROOT / "sente-ios/Packages/GoKit/Sources/GoKit/Zobrist+Table.swift",
    "go": ROOT / "sente-server/internal/rules/zobrist_table.go",
    "json": ROOT / "rules-spec/zobrist_table.json",
}


def constants(path):
    return re.findall(r"0x([0-9A-F]{16})", path.read_text())


def main():
    tables = {name: constants(path) for name, path in SOURCES.items()}
    sizes = {name: len(values) for name, values in tables.items()}
    if len(set(sizes.values())) != 1:
        print(f"constant counts differ: {sizes}")
        return 1
    if len(set(map(tuple, tables.values()))) != 1:
        print("Zobrist constants differ between engines -- board hashes cannot be compared.")
        return 1
    print(f"zobrist parity: {sizes['json']} constants identical across json, swift and go")
    return 0


if __name__ == "__main__":
    sys.exit(main())
