#!/usr/bin/env python3
"""Assemble the report bundle from the source files in bundle/."""
import os
import sys


def main():
    os.makedirs("bundle", exist_ok=True)
    with open(os.path.join("bundle", "manifest.json"), "w", encoding="utf-8") as f:
        f.write("{}\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
