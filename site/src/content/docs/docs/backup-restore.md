---
title: Backup & restore
description: What to back up, how, and how to get it back.
---

Losing the database while the archive store survives is as bad as the reverse: the two are coupled. This page explains what to back up, how to do it, and how to restore.

## What to back up

Two things, always as a pair:

**Postgres** — holds every skill record, version, user account, API key, and analytics event. Nothing in the archive store is queryable without it.

**Archive store** — holds the `.tar.gz` archives that `skael sync` downloads. Each archive is stored at `{name}/{checksum[:16]}.tar.gz` (content-addressed, not by version number). The database row references that exact path; if the file is missing, the DB row is useless.

Back up the database first, then the archive store. A snapshot that captures the DB after an archive was written but before the matching row was committed is safe to restore — the unused archive will be ignored. The reverse (DB row present, archive missing) breaks downloads for that version.

## Backing up

### Bare-metal / systemd

```bash
# Postgres — custom format, ~50 % smaller than plain SQL
pg_dump "$DATABASE_URL" --format=custom --file=skael-$(date +%Y%m%d).dump

# Archive store — STORAGE_PATH = /var/lib/skael/skills → parent is /var/lib/skael
tar -czf skills-$(date +%Y%m%d).tar.gz -C /var/lib/skael skills
```

Replace `/var/lib/skael` with the parent directory of your `STORAGE_PATH`. If `STORAGE_PATH=/data/skills`, the parent is `/data` and the argument to `-C` is `/data`.

### Docker Compose

The compose file uses two named volumes: `pg-data` for Postgres and `skill-data` for archives. Back up both from outside the containers:

```bash
# Postgres — exec into the db service, dump to stdout
docker compose exec -T db \
  pg_dump -U skael --format=custom skael > skael-$(date +%Y%m%d).dump

# Archive store — copy the named volume via a temporary container
docker run --rm \
  -v skael_skill-data:/data/skills:ro \
  -v "$(pwd)":/backup \
  alpine \
  tar -czf /backup/skills-$(date +%Y%m%d).tar.gz -C /data skills
```

:::note[Volume name prefix]
Docker prefixes volume names with the project directory name. If `docker compose` is run from a directory called `skael`, the skill volume is `skael_skill-data`. Confirm with `docker volume ls`.
:::

### S3 archive store

When `STORAGE_PATH=s3://…`, the archive store is object storage — durability is S3's responsibility. Enable [versioning](https://docs.aws.amazon.com/AmazonS3/latest/userguide/Versioning.html) on the bucket for point-in-time recovery. Back up Postgres only:

```bash
pg_dump "$DATABASE_URL" --format=custom --file=skael-$(date +%Y%m%d).dump
```

## Restoring

These steps apply to bare-metal and to Docker Compose. For compose, substitute `docker compose exec -T db psql ...` for direct `psql` / `pg_restore` calls.

### 1. Restore the database

```bash
pg_restore --clean --if-exists -d "$DATABASE_URL" skael-20240101.dump
```

`--clean` drops existing objects before recreating them. `--if-exists` suppresses errors if the target database is already empty (safe either way).

### 2. Restore the archive store

```bash
# Bare-metal — restore under /var/lib/skael so skills/ lands at STORAGE_PATH
tar -xzf skills-20240101.tar.gz -C /var/lib/skael

# Docker Compose — restore into the named volume
docker run --rm \
  -v skael_skill-data:/data/skills \
  -v "$(pwd)":/backup \
  alpine \
  tar -xzf /backup/skills-20240101.tar.gz -C /data
```

### 3. Start the server

```bash
# Bare-metal
./bin/skael-server

# Docker Compose
docker compose up -d
```

Migrations are a no-op when the database is already at the current schema version — verified: `goose: no migrations to run. current version: N`.

### 4. Verify

```bash
skael list          # should show all skills from before the backup
skael sync          # downloads and verifies checksums; should match
```

## What you can lose safely

**Orphaned archive files** (files in the store that have no corresponding DB row) are silently ignored. They don't appear in `skael list`, don't cause errors, and don't affect sync. You can safely drop extra files into the archive directory without consequence — verified by adding junk `.tar.gz` files to the store and confirming `skael list` and `skael sync` were unaffected.

**A missing archive** only breaks downloading or syncing that one version. Every other skill and version continues to work. Republishing the same skill content heals the store: content-addressing means an identical SKILL.md directory produces the same checksum and the same archive path.

**Sessions and API keys** are stored in Postgres and come back with the database restore. Users do not need to re-authenticate or regenerate keys.
