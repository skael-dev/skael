---
name: code-review
description: Perform thorough code reviews focusing on correctness, security, and maintainability.
license: Apache-2.0
metadata:
  author: skael-dev
  tags: [review, quality, security]
  version: "1.0.0"
compatibility:
  - claude-code
  - codex
---

# code-review

## When to use
Use this skill when asked to review code changes, pull requests, or files for quality issues.

## Process
1. Read the changed files completely before commenting
2. Check for correctness bugs first
3. Check for security issues (injection, auth bypass, data exposure)
4. Check for maintainability (naming, structure, complexity)
5. Provide specific, actionable feedback with line references

## Principles
- Be specific: "line 42: this SQL query is vulnerable to injection" not "watch out for SQL injection"
- Suggest fixes, don't just point out problems
- Acknowledge good patterns when you see them
- Prioritize: blocking issues first, then improvements, then style
