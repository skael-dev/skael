---
name: deterministic-transform
description: Use when the user asks to transform a JSON data file deterministically.
---

Transform an input JSON file into a validated output file using a fixed script,
with no model judgment involved in the transformation itself.

1. Run `scripts/transform.py input.json output.json` — postcondition: the
   script exits 0 and output.json exists.
2. Validate output.json against schema.json — postcondition: validation
   exits 0.
3. Write a row-count summary to out/summary.txt — postcondition: summary.txt
   exists and lists the row count.

If any step cannot complete, stop and report the failure state before
proceeding to any further step.
