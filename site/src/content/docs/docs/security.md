---
title: Security constraints
description: The security properties skael enforces, and why each one exists.
---


These exist for good reasons — don't weaken them without understanding why:

- `storage.Write/Read/Delete` validate paths stay within `BasePath` (path traversal prevention)
- `Unpack` rejects symlinks, hardlinks, unknown tar entry types, files >1MiB, and total extraction >50MB
- `MaxBytesReader` middleware caps request bodies at 10MB (must be < `maxUnpackSize`)
- Scanner runs on publish — `critical` and `warn` (high severity) block publishing
- Exfiltration and secret class findings are unappealable — no evaluation, no admin override, no suppression clears them. They return 422 and create no version row at all. Every other blocking finding holds the version for review instead, where a verified score at or above `QUALITY_FLOOR` or an owner/admin `skael review --approve` releases it
- API key hashes are compared via `crypto/subtle.ConstantTimeCompare` (after SHA-256 + hex.DecodeString) to prevent timing attacks
- `X-Forwarded-For` / `X-Real-IP` are read only when the connecting peer is inside `TRUSTED_PROXIES`; otherwise the socket address wins. Both headers are plain client input, and believing them unconditionally (as chi's deprecated `RealIP` does) lets one attacker present a fresh source address per request, defeating every per-IP limit
- Hook scripts read credentials from `~/.skael/config.json` at runtime — never embedded in agent config files
- Sync verifies downloaded archive checksums against the manifest before extracting
- File permissions are masked to `0o777` during extraction (no setuid/setgid)
- `GET /api/users/search` is open to any authenticated user and returns `{id, name, email}` only — no role, no timestamps. Minimum 2 characters and a hard cap of 20 results, so it is a lookup rather than a directory export. Restricting it to admins would make delegated ownership unusable, which is the trade being made
- `ownership.CanManage` is the entire escalation surface of ownership. It permits narrowing only; a delegate can never widen their scope. The property test in `internal/ownership/manage_test.go` is the guard — three individually-correct clauses can compose into a widening path that no per-clause test can see
