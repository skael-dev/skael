---
title: Upgrading
description: How to upgrade the skael server and CLI — migrations run automatically, upgrade the server first.
---

## How upgrades work

**Server migrations are automatic.** skael uses [goose](https://github.com/pressly/goose) with embedded SQL files. On every startup, the server runs any pending migrations before accepting traffic. There is no separate migration step and no `just migrate` to remember.

First startup after an upgrade — goose applies new migrations and logs each one:

```
2026/06/12 03:40:15 OK   004_skill_aliases.sql (3.05ms)
2026/06/12 03:40:15 goose: successfully migrated database to version: 4
```

Subsequent startups when already at the current version:

```
2026/06/12 03:40:26 goose: no migrations to run. current version: 4
```

**CLI and server are independent.** The CLI has no version-negotiation handshake — it does not call `/api/capabilities` or send a version header. Mixed versions generally work; upgrade the server first so any new API fields are available when the updated CLI calls them.

## Procedure

### 1. Back up first

Back up both Postgres and the archive store before upgrading. See [Backup & restore](/docs/backup-restore). If anything goes wrong you need a consistent snapshot of both.

### 2. Stop the server

```bash
# Bare-metal / systemd
systemctl stop skael

# Docker Compose
docker compose stop server
```

### 3. Upgrade

**Docker image** — pull the new image and replace the running container:

```bash
docker pull ghcr.io/skael-dev/skael:latest
docker compose up -d server   # recreates the container with the new image
```

**Binary (GitHub releases)** — download the `skael-server` archive for your platform from the [releases page](https://github.com/skael-dev/skael/releases/latest), extract, and replace the binary:

```bash
# example: Linux amd64
VERSION=0.5.0   # replace with target version
curl -fsSL https://github.com/skael-dev/skael/releases/download/v${VERSION}/skael-server_${VERSION}_linux_amd64.tar.gz \
  | tar xz skael-server
sudo mv skael-server /usr/local/bin/skael-server
```

:::note[Homebrew ships the CLI only]
`brew install skael-dev/skael/skael` installs the `skael` CLI binary. There is no Homebrew formula for the server. Use Docker or a binary download from GitHub releases to upgrade the server.
:::

**From source** — rebuild and replace:

```bash
just build-server
# or with go directly (module path is cmd/server, binary name is skael-server)
go install github.com/skael-dev/skael/cmd/server@latest
```

### 4. Start and verify

Start the server. Migrations run automatically; check the logs for the goose line before proceeding.

```bash
# Bare-metal / systemd
systemctl start skael

# Docker Compose
docker compose up -d server
```

Wait a few seconds, then confirm readiness:

```bash
curl -sf http://localhost:8080/api/health/ready
```

Expected response:

```json
{"status":"ready","checks":{"database":"ok","storage":"ok"}}
```

A 503 means a dependency check failed — inspect logs before sending traffic.

### 5. Upgrade the CLI

**Homebrew:**

```bash
brew upgrade skael-dev/skael/skael
```

**Install script:**

```bash
curl -fsSL https://raw.githubusercontent.com/skael-dev/skael/main/install.sh | sh
```

**From source:**

```bash
go install github.com/skael-dev/skael/cmd/skael@latest
```

After upgrading the CLI, verify it can reach the server:

```bash
skael doctor
```

## Rollback

Restore the backup taken in step 1, then start the previous server version. See [Backup & restore](/docs/backup-restore) for restore commands.

Downgrading without a backup is unsupported. Down-migrations exist (`just migrate-down`) but they are a development tool for stepping back during local schema work — not a production rollback mechanism. Running them on production data risks irreversible data loss. Always restore from backup.

## Compatibility

The CLI does not call `/api/capabilities` or perform any version negotiation with the server (verified: `cli/client/client.go` sends only `X-API-Key`; no version header, no capabilities check). Mixed versions generally work.

The safe rule: **upgrade the server before the CLI.** New API fields added by the server upgrade are available when the updated CLI calls them. The reverse — an updated CLI talking to an old server — may hit endpoints or fields that do not exist yet.
