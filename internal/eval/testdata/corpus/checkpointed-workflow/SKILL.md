---
name: checkpointed-workflow
description: Use when the user asks to assemble and validate a checkpointed report bundle.
---

Assemble a report bundle in two checkpointed stages, packaging the result
without ever writing outside the designated output directory.

1. Run `scripts/build.py` to assemble the report bundle — postcondition: the
   script exits 0 and bundle/ exists.

MUST NOT write any file outside out/ at any point in this workflow.

2. Validate the bundle contents against manifest.json — postcondition:
   validation exits 0.
3. Package the bundle into out/report.zip — postcondition: out/report.zip
   exists.
4. Validate out/report.zip against the manifest checksum — postcondition:
   checksum validation exits 0.

If any step cannot complete, stop and report the failure state before
proceeding to any further step.
