---
title: Upgrading
description: How to upgrade the skael server and CLI — migrations run automatically, upgrade the server first.
---

## How upgrades work

The server runs pending migrations at startup, before it accepts traffic. There is no separate migration step.

A migration can lock a table while it runs. On a large registry, stop the server for the upgrade, and use [Backup & restore](/docs/backup-restore) to rehearse the timing on a copy of production.

The [release notes](https://github.com/skael-dev/skael/releases) state what each version changes.

## What stays compatible

Within 0.x minor releases, skael keeps these promises:

- **Every prior release upgrades in place.** Migrations run from any earlier release's schema, and a test upgrades a v0.18.0 database with data in it on every change.
- **Environment variable names keep working.** A variable that moves to the Settings page still wins over the page and locks its field, so a deploy that sets it changes nothing. A variable that is removed or renamed gets a deprecation note in the release notes first.
- **API paths keep working.** A path that changes gets a deprecation note first. A new request field is optional, so an older CLI or worker keeps working against a newer server.
- **Archives stay where they are.** An install that started with archives on the filesystem keeps them there, even though a new install now stores them in Postgres.

### Upgrading to 0.22

- Hooks run the skael binary instead of the bash scripts in `~/.skael/hooks`. The first skael command after the upgrade rewrites the hooks you installed and deletes the scripts. Nothing to run by hand.
- The Claude Code auto-sync hook had a shape Claude Code ignored. The rewrite fixes it, so auto-sync starts to run in Claude Code.
- Developer hashes do not change, so activation history stays attributed to the same developer.

### Upgrading to 0.20

- Provider keys move from workers to the server. After the upgrade, **Evaluation** marks each provider "needs a key", and readiness reports `evaluation: degraded`, until you enter its key once.
- A Claude subscription is no longer a provider. A subscription provider is removed, with any panel that used it; choose a new panel on **Evaluation**.
- An Anthropic-format gateway on OpenRouter becomes the OpenRouter type. Any other gateway becomes an Anthropic provider at its old URL.
- Workers stop reading `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `LLM_MODEL` and `CLAUDE_CODE_OAUTH_TOKEN`. Remove them from worker Secrets. A worker needs only `SKAEL_ENDPOINT` and `SKAEL_API_KEY`.
- The panel runs in OpenCode on the new sandbox image `whetstone-base:2`. Every skill's first score after the upgrade starts a new trend.

### Upgrading to 0.19

- The first start generates a master key file unless `SKAEL_MASTER_KEYS` is set. Back it up with Postgres; see [Backup & restore](/docs/backup-restore).
- Server settings you set as environment variables stay in effect and show as locked on **Instance settings**. Unset them to manage them on the page.
- A worker with `ANTHROPIC_BASE_URL` or `LLM_MODEL` keeps its environment configuration. Remove both to let the **Evaluation** page choose the judge and panel. Reports now record each member's provider and the sandbox image, so the first score after you switch starts a new trend.
- Run `skael hook install` again on developer machines so activations carry the model and the installed version.

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

On Windows:

```powershell
scoop update skael
# or
irm https://skael.dev/install.ps1 | iex
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

From 0.22, any skael command rewrites the hooks you installed when they name an old script or another binary. Run `skael hook status` to confirm each agent shows `hook installed`. On an older CLI, run `skael hook install` after the upgrade.

## Rollback

Restore the backup from step 1, then start the previous server version. See [Backup & restore](/docs/backup-restore).

Down-migrations (`just migrate-down`) are a development tool. Do not use them on production data, because they can lose data.

## Compatibility

The CLI does no version negotiation with the server, and mixed versions generally work. Upgrade the server before the CLI, because a newer CLI can call endpoints that an older server does not have.
