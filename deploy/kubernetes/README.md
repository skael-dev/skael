# Running the Skael worker against a Kubernetes cluster

Use this driver when the worker runs on a node that has no Docker daemon, or
that must not hold a Docker socket at all. `SANDBOX_DRIVER=kubernetes`
schedules each evaluation session as a pod through the cluster API, instead of
talking to a Docker socket. `WORKER_RUN_ROOT` does not apply here: the docker
driver bind-mounts host paths that its daemon must resolve, and this driver
mounts nothing.

## Before you start

Apply the manifest in this directory:

```bash
kubectl apply -f deploy/kubernetes/worker-rbac.yaml
```

It creates a namespace, a service account, and a role granting exactly what
the driver needs: create, get, and delete on pods; create on `pods/exec`;
create and delete on configmaps and networkpolicies.

**Give the worker a namespace of its own, holding nothing but session pods.**
`pods/exec` is the driver's entire privilege surface — whoever holds it can
execute in any pod in that namespace. Sharing the namespace with anything
else hands that same privilege to session pods that were meant to hold only
one skill under evaluation.

## The shortest working configuration

```bash
SANDBOX_DRIVER=kubernetes
SANDBOX_K8S_NAMESPACE=skael-sandbox
```

Everything else has a default. The worker resolves the published base image,
`ghcr.io/skael-dev/whetstone-base:<n>`, from a registry rather than building
one — a skill that declares a dependency is refused by name, because this
driver has no build step to install it into.

## All settings

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `SANDBOX_DRIVER` | yes | `docker` | Set to `kubernetes` |
| `SANDBOX_K8S_NAMESPACE` | yes | — | Namespace the driver creates session pods in. Must hold nothing but session pods |
| `SANDBOX_K8S_IMAGE` | no | the published whetstone base image | Image a session pod runs. Must already be in a registry — this driver resolves an image, it does not build one |
| `SANDBOX_K8S_PULL_SECRET` | no | — | Name of an image pull secret in `SANDBOX_K8S_NAMESPACE`, for a private registry |
| `SANDBOX_K8S_RUNTIME_CLASS` | no | — | `runtimeClassName` set on session pods, for example `kata`, for a hardware-isolated runtime |
| `SANDBOX_K8S_HARDWARE_ISOLATED` | no | unset | Asserts the runtime is hardware-isolated. Accepted only as the exact lowercase string `true`; any other value reads as unasserted, and the driver refuses a run that needs isolation |
| `SANDBOX_K8S_NETWORK_POLICY` | no | unset | Asserts the cluster's CNI enforces `NetworkPolicy`. Accepted only as the exact lowercase string `true`; any other value reads as unasserted, and the driver refuses a run that restricts the network |

The worker's own settings — `SKAEL_ENDPOINT`, `SKAEL_API_KEY`, the LLM
credentials — are unchanged. See the worker table in `CLAUDE.md`.

## Egress: the part that needs your attention

A CNI that does not enforce `NetworkPolicy` accepts the policy object the
driver creates and ignores it. An unasserted cluster then runs every
"restricted" session unrestricted, while every test still passes, because
nothing in the driver can observe the difference from inside the cluster.

Set `SANDBOX_K8S_NETWORK_POLICY=true` only after you confirm your CNI
enforces `NetworkPolicy`, and verify the assertion against the real cluster
first:

```bash
whetstone doctor --check-egress
```

## Checking your setup

```bash
whetstone doctor
```

It prints the driver it resolved, the namespace and image, and a warning for
each guarantee you have not asserted.

## Choosing between the drivers

| You have | Use |
|---|---|
| A Docker daemon on the host | `docker`, the default |
| A Kubernetes cluster | `kubernetes` — this driver |
| Neither, only a container runtime | `northflank` — see `deploy/northflank/README.md` |

This driver enforces a per-session egress allowlist itself, through the
`NetworkPolicy` objects it creates, and needs no domain list to assert
against. The Northflank driver, by contrast, cannot scope egress to one
session and instead checks each run's requested domains against an
operator-configured list. Prefer this driver when you have a cluster.
