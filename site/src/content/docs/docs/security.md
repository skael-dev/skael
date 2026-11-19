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
- A rescan of a stored version runs the same scan steps as a publish. An owner of the skill or an admin starts one from the Security tab, `POST /api/skills/{name}/versions/{version}/scan` or `skael rescan`. An admin rescans every released version from Instance settings after a scanner change. A new exfiltration or secret finding withdraws a released version: the server never serves it again, `latest_version` falls back to the newest released version below it, and `skael sync` removes it from each machine. The owners get an email when a mail server is set, the audit log gets an entry with the prior report, and `skill.version.withdrawn` fires. Only a new version releases the skill again. Any other new finding stays a flag on the Security tab, because a guess from the shape of a line must not pull a released skill
- API key hashes are compared via `crypto/subtle.ConstantTimeCompare` (after SHA-256 + hex.DecodeString) to prevent timing attacks
- `X-Forwarded-For` / `X-Real-IP` are read only when the connecting peer is inside `TRUSTED_PROXIES`; otherwise the socket address wins. Both headers are plain client input, and believing them unconditionally (as chi's deprecated `RealIP` does) lets one attacker present a fresh source address per request, defeating every per-IP limit
- Hook scripts read credentials from `~/.skael/config.json` at runtime — never embedded in agent config files
- Sync verifies downloaded archive checksums against the manifest before extracting
- File permissions are masked to `0o777` during extraction (no setuid/setgid)
- `GET /api/users/search` is open to any authenticated user and returns `{id, name, email}` only — no role, no timestamps. Minimum 2 characters and a hard cap of 20 results, so it is a lookup rather than a directory export. Restricting it to admins would make delegated ownership unusable, which is the trade being made
- `ownership.CanManage` is the entire escalation surface of ownership. It permits narrowing only; a delegate can never widen their scope. The property test in `internal/ownership/manage_test.go` is the guard — three individually-correct clauses can compose into a widening path that no per-clause test can see
- A webhook delivery is the one request the server sends to an address a user chose. It needs `https`, and it refuses private, loopback, link-local and shared (`100.64.0.0/10`) addresses. The check runs on the resolved IP at connection time and on each redirect, so a DNS change or a redirect cannot reach a metadata service. `WEBHOOK_ALLOW_PRIVATE=true` opens private addresses, loopback and `http` for a receiver on the install's own network, and link-local stays refused. A delivery ignores proxy variables, because a proxy would hide the target from the check. See [Webhooks](/docs/webhooks#address-rule)
