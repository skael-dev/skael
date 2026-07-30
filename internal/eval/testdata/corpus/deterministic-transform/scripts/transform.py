#!/usr/bin/env python3
"""Deterministically transform an input JSON file into an output JSON file.

Usage: transform.py <input.json> <output.json>
"""
import json
import sys


def transform(record):
    return {
        "id": record.get("id"),
        "value": record.get("value", 0) * 2,
    }


def main():
    if len(sys.argv) != 3:
        print("usage: transform.py <input.json> <output.json>", file=sys.stderr)
        return 1

    with open(sys.argv[1], "r", encoding="utf-8") as f:
        records = json.load(f)

    out = [transform(r) for r in records]

    with open(sys.argv[2], "w", encoding="utf-8") as f:
        json.dump(out, f, indent=2, sort_keys=True)

    return 0


if __name__ == "__main__":
    sys.exit(main())
