---
title: Self-hosting
description: Run the skael platform on your own infrastructure.
---

skael is a single Go binary that embeds the dashboard and serves the API. It needs one thing: a Postgres database.

## Configuration

`DATABASE_URL` is the only required variable. On first start the server prints a claim code to its log; open the server in a browser and enter it to create the owner account.

The owner sets almost everything else on **Instance settings**: the public URL, sign-up, sign-in providers, email, import, scanning, the quality floor, retention, webhooks and rate limits. A change applies on every replica without a restart. Each section has an **Info** guide and, where it makes sense, a **Test** button.

An environment variable wins over the page and locks its field, so a GitOps or Helm install keeps working. A few settings are read before the database and live only in the environment. [Configuration](/docs/configuration) lists every setting, generated from the same definitions the server uses.

Migrations run automatically on startup.

## Bring your own Postgres

```bash
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/skael?sslmode=disable" \
  -v skael-data:/data/skills \
  -e STORAGE_PATH=/data/skills \
  ghcr.io/skael-dev/skael:latest
```

## Bundled Postgres (Docker Compose)

```bash
docker compose up -d
```

This starts the platform plus a Postgres container with a persistent volume. The platform is at `http://localhost:8080`. Enter the claim code from `docker compose logs server` to create the owner account. Publishing and scanning work right away. Evaluations do not — that's a separate opt-in piece, see below.

## Evaluation worker (optional)

The server queues evaluation jobs but never runs them — no Docker socket and no LLM key live on it. Running evaluations requires a separate `skael-worker` process, because it needs a sandbox to run each session in and a direct Anthropic API key to judge the result.

### Choosing a sandbox driver

`SANDBOX_DRIVER` selects how the worker sandboxes each session. It defaults to `docker`, which needs a Docker daemon the worker can reach — everything on this page assumes it, unless you set the variable to something else.

| You have | Set `SANDBOX_DRIVER` to |
|---|---|
| A Docker daemon on the worker's host | `docker` — the default, described below |
| A Kubernetes cluster, no Docker daemon on the worker's host | `kubernetes` — see `deploy/kubernetes/README.md` |
| Neither, only a host that can run a container | `northflank` — see `deploy/northflank/README.md` |

Both alternative drivers resolve a published base image rather than building one, so a skill that declares `apt`, `pip`, or `npm` dependencies is refused by name on them; only the `docker` driver builds. `WORKER_RUN_ROOT`, below, applies to the `docker` driver only.

### With Docker

Run it on the host, alongside Compose rather than inside it:

```bash
export SKAEL_ENDPOINT=http://localhost:8080
export SKAEL_API_KEY=<a key from Evaluation → Add a worker>
skael-worker
```

The worker needs nothing else. Provider keys stay on the server, and each job claim carries the keys its judge and panel use.

### In a container

The worker also ships as an image, published with every release:

```bash
docker run -d \
  -e SKAEL_ENDPOINT=http://localhost:8080 \
  -e SKAEL_API_KEY=<a key from Evaluation → Add a worker> \
  -e WORKER_RUN_ROOT=/var/lib/skael/run \
  -e WORKER_WORK_ROOT=/var/lib/skael/work \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /var/lib/skael/run:/var/lib/skael/run \
  -v /var/lib/skael/work:/var/lib/skael/work \
  ghcr.io/skael-dev/skael-worker:latest
```

`docker compose --profile worker up` runs the same thing from source.

One rule governs the container, and it is load-bearing. The worker starts sandbox containers as *siblings* through the mounted socket, so the host daemon resolves every bind source. `WORKER_RUN_ROOT` and `WORKER_WORK_ROOT` must therefore be bound at the **same path on both sides** — never a named volume. Docker creates a missing bind source as an empty directory instead of failing, so a container-local path yields a sandbox with no task and no verifier, which scores as a skill that did nothing. The worker refuses to start containerized unless both are set.

### Worker configuration

On the **Evaluation** page, choose a provider (OpenRouter, Anthropic, OpenAI, Azure AI Foundry, or another OpenAI-compatible endpoint) and paste its key. The server seals the key under the master key; an `env:NAME` or `file:/path` reference works too. A provider with default models fills an empty panel. Then **Add a worker**: it creates a key that can only run eval jobs and shows a Compose, `docker run` or Helm snippet.

Every panel session runs in OpenCode inside the sandbox, on any provider. Each worker checks in while it runs, and the Evaluation page shows a worker that is offline or on a sandbox image the others do not run.

**Evaluation → Jobs** lists every queued, running and recent job with its phase, sessions done, cost and the time since its last event. A running job with no event for 3 minutes shows as stalled. A job's page lists its events, with the provider's own error for each failed session, and a **Stop** button. The worker writes the same events to its log. [Configuration](/docs/configuration#worker) lists every worker variable.

## Storage

A new install with no `STORAGE_PATH` keeps skill archives in Postgres, so the server needs no volume and one database backup holds everything. An install that started on the filesystem keeps its directory; the migration records that.

Set `STORAGE_PATH` to a directory to keep archives on disk, and mount a persistent volume there.

## Kubernetes (Helm)

`deploy/helm/skael` installs the server and, optionally, workers that run each session as a pod through the kubernetes sandbox driver:

```bash
helm upgrade --install skael deploy/helm/skael \
  --set server.database.url='postgres://skael:...@postgres:5432/skael' \
  --set ingress.enabled=true --set ingress.host=skael.example.com --set ingress.tls=true
```

The chart generates the master key into a Secret on install, keeps it on upgrade and on uninstall, and runs the server with a read-only root file system. To add workers, create a Secret with `SKAEL_API_KEY` from **Evaluation → Add a worker**, then set `worker.enabled=true` and `worker.existingSecret`. On a cluster whose node pools are tainted, set `worker.sandboxTolerations` and `worker.sandboxNodeSelector` for the session pods, and `tolerations` and `nodeSelector` under `server` and `worker` for their own pods.

## Object storage (S3-compatible)

For ephemeral/k8s deployments or to run multiple replicas, point `STORAGE_PATH` at S3-compatible object storage (AWS S3, MinIO, Cloudflare R2, Backblaze B2, DigitalOcean Spaces):

```bash
docker run -p 8080:8080 \
  -e DATABASE_URL="postgres://user:pass@host:5432/skael?sslmode=disable" \
  -e STORAGE_PATH="s3://my-bucket/skael" \
  -e S3_REGION="us-east-1" \
  -e S3_ACCESS_KEY_ID="..." -e S3_SECRET_ACCESS_KEY="..." \
  ghcr.io/skael-dev/skael:latest
```

| Variable | Default | Description |
|---|---|---|
| `STORAGE_PATH` | `./data/skills` | `s3://bucket/prefix` switches to S3; any other value is a local path |
| `S3_ENDPOINT` | `s3.amazonaws.com` | Set for MinIO, R2, Spaces. No `AWS_*` fallback |
| `S3_REGION` | `us-east-1` | Falls back to `AWS_REGION` when unset |
| `S3_ACCESS_KEY_ID` | — | Falls back to `AWS_ACCESS_KEY_ID` |
| `S3_SECRET_ACCESS_KEY` | — | Falls back to `AWS_SECRET_ACCESS_KEY` |
| `S3_USE_PATH_STYLE` | `false` | `true` for MinIO |
| `S3_USE_SSL` | `true` | `false` for local MinIO. Only the literal `false` disables it |

Credentials resolve in one step: if a key and a secret are both found — under either the `S3_*` or the `AWS_*` name — they are used as static credentials. If either is missing, skael falls back to an IAM instance role (EC2, ECS, EKS) and ignores the one that was set. So half a key pair is the same as none.

The bucket must already exist. skael checks it at startup and refuses to boot if it is missing or unreachable.
