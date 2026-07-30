---
name: checkpointed-workflow
description: Use when the user asks to assemble and validate a checkpointed report bundle.
---

Assemble a report bundle in two checkpointed stages, packaging the result
without ever writing outside the designated output directory.

1. Run `scripts/build.py` to assemble the report bundle — postcondition: the
   script exits 0 and bundle/ exists.

MUST NOT write any file outside out/ at any point in this workflow.

2. Run `scripts/validate.py` — postcondition: validates the bundle
   contents against manifest.json and exits 0.
3. Run `scripts/package.py` — postcondition: packages the bundle into
   out/report.zip.
4. Run `scripts/verify.py` — postcondition: validates out/report.zip
   against the manifest checksum and exits 0.

If any step cannot complete, stop and report the failure state before
proceeding to any further step.
