---
name: document-formatter
description: Use when the user asks to format a document against the house style guide.
---

Format a draft document into the house style, using a fixed style guide and
report template.

1. Read the [style guide](references/style-guide.md) — postcondition: the style
   rules load.
2. Apply the style rules and fill assets/report.tmpl — postcondition: the
   rendered report exits 0 from the template linter.
3. Write the rendered report to out/report.md — postcondition: it exists.

MUST NOT write any file outside out/.
