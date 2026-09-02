# Running the Skael worker with Northflank sandboxes

Use this driver when the worker runs somewhere that has no Docker daemon and no
Kubernetes cluster — Azure Container Apps, Cloud Run, or any host where you can
run a container but cannot start a sibling one.

The worker holds a Northflank API token. Northflank runs each evaluation
session as a sandbox service in your own project, and the worker deletes it
when the session ends.

## Before you start

You need a Northflank account, a project the worker can create services in, and
an API token with access to it.

**Give the worker its own project.** The driver deletes every service carrying
its owner label, including orphans it finds at startup. Point it at a project
holding anything else and it will delete that too.

## The shortest working configuration

```bash
SANDBOX_DRIVER=northflank
SANDBOX_NF_TOKEN=nf_...
SANDBOX_NF_PROJECT=skael-sandboxes
```

Everything else has a default. The worker pulls the published base image,
`ghcr.io/skael-dev/whetstone-base:1`, which carries the Claude Code CLI, Python,
Node, pandoc, LibreOffice, poppler and ffmpeg.

This configuration runs sessions **with unrestricted network access**. Read the
next section before scoring anything with it.

## Egress: the part that needs your attention

An evaluation runs a skill you may not have written. It needs to reach the LLM
gateway, and it should reach nothing else.

Northflank gives no way to set a different allowlist for each session, so the
driver cannot enforce one per run. Instead it relies on the egress policy you
configure on your Northflank project, and it refuses to pretend that policy
exists when you have not said it does.

**Northflank Cloud cannot make this assertion at all.** Northflank documents
egress network policies for a BYOC cluster only. They are configured through
its web UI, with no API, and they apply to a whole project rather than one
session. On Northflank Cloud no egress restriction is documented, so every
restricted run is refused there and only fully open runs work. This means a
release cannot be scored on Northflank Cloud; use a BYOC cluster for that.

Until you make the assertion below, the driver **refuses every restricted run**.
Only fully open runs work, which is fine for trying it out and wrong for
scoring a release.

To enable restricted runs:

1. Configure your Northflank project's egress policy to permit only the
   destinations your evaluations need, typically your LLM gateway.
2. Tell the worker what you configured, and assert that it is enforced:

```bash
SANDBOX_NF_ALLOWED_DOMAINS=api.anthropic.com
SANDBOX_NF_NETWORK_POLICY=true
```

The driver then accepts a run only when every domain it asks for is inside
`SANDBOX_NF_ALLOWED_DOMAINS`. A skill declaring a domain you have not listed is
refused by name, rather than run with access you never granted.

**Both assertions accept only the exact lowercase string `true`.** `TRUE` and
`1` read as "not asserted", which fails closed. If your restricted runs are
being refused and you believe you enabled this, check the spelling first.

## Hardware isolation

Untrusted work is refused by default. Northflank describes its sandboxes as
VM-isolated, but the worker cannot verify that from the inside, so it will not
assume it:

```bash
SANDBOX_NF_HARDWARE_ISOLATED=true
```

Set this only if you have satisfied yourself that the claim holds for your plan.

## All settings

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `SANDBOX_DRIVER` | yes | `docker` | Set to `northflank` |
| `SANDBOX_NF_TOKEN` | yes | — | Northflank API token |
| `SANDBOX_NF_PROJECT` | yes | — | Project sandboxes run in. Give it a project of its own |
| `SANDBOX_NF_IMAGE` | no | `ghcr.io/skael-dev/whetstone-base:1` | Base image a session runs |
| `SANDBOX_NF_REGISTRY_CREDENTIAL` | no | — | Saved credential name, for a private registry. Not needed for the default image |
| `SANDBOX_NF_PLAN` | no | `nf-compute-20` | Resource plan for each sandbox |
| `SANDBOX_NF_ALLOWED_DOMAINS` | for restricted runs | — | Domains your project's egress policy permits, comma separated |
| `SANDBOX_NF_NETWORK_POLICY` | no | `false` | Assert that egress is enforced. Exactly `true` |
| `SANDBOX_NF_HARDWARE_ISOLATED` | no | `false` | Assert hardware isolation. Exactly `true` |
| `SANDBOX_NF_CLI` | no | `northflank` | Path to the CLI used to copy the workspace |

The worker's own settings — `SKAEL_ENDPOINT`, `SKAEL_API_KEY`, the LLM
credentials — are unchanged. See the worker table in `CLAUDE.md`.

`WORKER_RUN_ROOT` does not apply. It exists because the Docker driver
bind-mounts host paths that the host daemon must resolve; this driver mounts
nothing.

## Checking your setup

```bash
whetstone doctor
```

It prints the driver it resolved, the project and image, and a warning for each
guarantee you have not asserted.

## Proving it works

```bash
just test-northflank
```

This runs the live conformance and egress suite against a real project. It
needs `SANDBOX_NF_TOKEN` and creates real sandboxes, so it costs money and
takes minutes rather than seconds. It is the only end-to-end proof this
driver works: every other test in this package runs against a fake HTTP
server and a fake CLI. CI does not run it.

## Costs

Northflank bills a sandbox as a service, not per second, and a paused service
still bills for storage. The driver therefore deletes sandboxes rather than
pausing them, on every exit path, and sweeps orphans at startup.

If you find sandboxes you did not expect, check the worker's logs before
deleting them by hand — a leak is a bug worth reporting, and unlike a stale
container it costs money while it waits to be noticed.

## Choosing between the drivers

| You have | Use |
|---|---|
| A Docker daemon on the host | `docker`, the default |
| A Kubernetes cluster | `kubernetes` — see `deploy/kubernetes/worker-rbac.yaml` |
| Neither, only a container runtime | `northflank` |

The Kubernetes driver enforces a per-session egress allowlist itself and needs
no assertion about domains. Prefer it when you have a cluster.
