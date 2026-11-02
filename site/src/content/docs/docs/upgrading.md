---
title: Upgrading
description: How to upgrade the skael server and CLI — migrations run automatically, upgrade the server first.
---

## How upgrades work

The server runs pending migrations at startup, before it accepts traffic. There is no separate migration step.

A migration can lock a table while it runs. On a large registry, stop the server for the upgrade, and use [Backup & restore](/docs/backup-restore) to rehearse the timing on a copy of production.

The [release notes](https://github.com/skael-dev/skael/releases) state what each version changes.

## Procedure

### 1. Back up first

Back up both Postgres and the archive store. See [Backup & restore](/docs/backup-restore).

### 2. Stop the server

```bash
# Bare-metal / systemd
systemctl stop skael

# Docker Compose
docker compose stop server
```

### 3. Upgrade the server

**Docker image.** Pull the new image and replace the running container:

```bash
docker pull ghcr.io/skael-dev/skael:latest
docker compose up -d server
```

**Binary.** Download the `skael-server` archive for your platform and replace the binary. This example is for Linux on amd64:

```bash
VERSION=$(curl -fsSL https://api.github.com/repos/skael-dev/skael/releases/latest | sed -n 's/.*"tag_name": "v\([^"]*\)".*/\1/p')
curl -fsSL https://github.com/skael-dev/skael/releases/download/v${VERSION}/skael-server_${VERSION}_linux_amd64.tar.gz \
  | tar xz skael-server
sudo mv skael-server /usr/local/bin/skael-server
```

**From source.**

```bash
just build-server
```

### 4. Start and verify

```bash
# Bare-metal / systemd
systemctl start skael

# Docker Compose
docker compose up -d server
```

Confirm readiness:

```bash
curl -sf http://localhost:8080/api/health/ready
```

Expected response:

```json
{"status":"ready","checks":{"database":"ok","storage":"ok"}}
```

A 503 means a dependency check failed. Read the logs before you send traffic.

### 5. Upgrade the CLI

On macOS with Homebrew:

```bash
brew upgrade --cask skael-dev/skael/skael
```

With the install script:

```bash
curl -fsSL https://skael.dev/install.sh | sh
```

From source:

```bash
git clone https://github.com/skael-dev/skael.git
cd skael && go build -o skael ./cmd/skael
```

`go install <pkg>@latest` does not work for any binary in this repo, because `go.mod` has a `replace` directive.

Verify that the CLI reaches the server:

```bash
skael doctor
```

### 6. Install the hooks again

Run `skael hook install` on every machine after a CLI upgrade. A machine keeps running its old hook scripts until you do.

## Rollback

Restore the backup from step 1, then start the previous server version. See [Backup & restore](/docs/backup-restore).

Down-migrations (`just migrate-down`) are a development tool. Do not use them on production data, because they can lose data.

## Compatibility

The CLI does no version negotiation with the server, and mixed versions generally work. Upgrade the server before the CLI, because a newer CLI can call endpoints that an older server does not have.
