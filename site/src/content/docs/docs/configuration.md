---
title: Configuration
description: Every setting the server and the worker read, and where to change it.
---

<!-- Generated from internal/settings by `go run ./cmd/server --config-docs`. Do not edit. -->

An owner or admin changes most server settings on the Settings page, and a change applies on every replica without a restart. An environment variable wins over the page and locks its field. Settings marked *env only* are read before the database, so they are set only in the environment.

A secret field on the page takes a value, which the server seals under the master key, or a reference: `env:NAME` reads another variable, and `file:/path` reads a mounted file.

## Server

### General

Set the public URL to the address people type to reach skael, with no path. Behind a reverse proxy, this is the proxy's address. Sign-in callbacks, links in email and the GitHub App's webhooks use it, so change it before you set up any of them.

| Variable | Where | Default | Description |
|---|---|---|---|
| `PUBLIC_URL` | Settings page, required with SSO, SMTP or GitHub | — | The URL people use to reach skael. Sign-in callbacks, email links and GitHub webhooks are built from it. |

### Sign-up

The first person to sign up becomes the owner. After that, every new account is a member.

Disable sign-up after your team has joined, or limit sign-up to your email domains. Invite people from Settings → Team instead. OIDC sign-in still creates accounts when sign-up is disabled, because your identity provider decides who may sign in.

| Variable | Where | Default | Description |
|---|---|---|---|
| `DISABLE_SIGNUP` | Settings page | `false` | Refuses new password, Google and GitHub accounts. OIDC stays open, because the identity provider is the gate. |
| `SIGNUP_ALLOWED_DOMAINS` | Settings page | — | Comma-separated. A new account from any other domain is refused on every sign-in path. |

### Sign-in providers

Each provider needs an application at the provider, and each application needs the callback URL of this instance. Set the public URL first.

#### Google

1. In Google Cloud, open APIs & Services → Credentials and create an OAuth client ID of type Web application.
2. Add the authorized redirect URI `https://skael.example.com/api/auth/oauth/google/callback`.
3. Paste the client ID and the client secret here.

#### GitHub

1. In GitHub, open Settings → Developer settings → OAuth Apps → New OAuth App.
2. Set the homepage URL to `https://skael.example.com` and the authorization callback URL to `https://skael.example.com/api/auth/oauth/github/callback`.
3. Generate a client secret, and paste the client ID and the secret here.

This OAuth App is only for sign-in. Git sync uses the GitHub App on Settings → GitHub.

#### Any OpenID Connect provider

1. In your provider (Okta, Entra ID, Auth0, Keycloak, Authentik, Google Workspace), create a web application with the authorization code flow.
2. Register the redirect URI `https://skael.example.com/api/auth/oauth/oidc/callback`.
3. Paste the issuer URL, the client ID and the client secret here. The issuer URL is the address that serves `/.well-known/openid-configuration`.

The test fetches the discovery document and checks that the issuer answers.

| Variable | Where | Default | Description |
|---|---|---|---|
| `OIDC_ISSUER` | Settings page | — | The issuer URL of any OpenID Connect provider. The first sign-in creates a member, even with sign-up disabled. |
| `OIDC_CLIENT_ID` | Settings page | — | From the application you register at the provider. |
| `OIDC_CLIENT_SECRET` | Settings page | — | From the application you register at the provider. |
| `GOOGLE_CLIENT_ID` | Settings page | — | An OAuth client of type Web application in Google Cloud. |
| `GOOGLE_CLIENT_SECRET` | Settings page | — | From the same Google OAuth client. |
| `GITHUB_CLIENT_ID` | Settings page | — | A GitHub OAuth App. The GitHub App for git sync and the import token are separate. |
| `GITHUB_CLIENT_SECRET` | Settings page | — | From the same GitHub OAuth App. |

### Email

Email turns on password reset and email change. Without it, an owner resets a password with `skael-server reset-password --email <address>`.

| Provider | Host | Port | Username | Password |
|---|---|---|---|---|
| Postmark | `smtp.postmarkapp.com` | 587 | the server API token | the server API token |
| Amazon SES | `email-smtp.<region>.amazonaws.com` | 587 | the SMTP user name | the SMTP password |
| Resend | `smtp.resend.com` | 465 | `resend` | the API key |
| SendGrid | `smtp.sendgrid.net` | 587 | `apikey` | the API key |
| Mailgun | `smtp.mailgun.org` | 587 | the SMTP login | the SMTP password |
| Gmail | `smtp.gmail.com` | 587 | your address | an app password |

The provider must allow the from address. The test sends one email to your own address.

| Variable | Where | Default | Description |
|---|---|---|---|
| `SMTP_HOST` | Settings page | — | Turns on password reset and email change. For example smtp.postmarkapp.com or email-smtp.us-east-1.amazonaws.com. |
| `SMTP_PORT` | Settings page | `587` | 587 uses STARTTLS, 465 uses TLS from the first byte. |
| `SMTP_USERNAME` | Settings page | — | Leave empty for a relay without authentication. |
| `SMTP_PASSWORD` | Settings page | — | The SMTP password or the provider's API token. |
| `SMTP_FROM` | Settings page | — | For example Skael <skael@example.com>. The provider must allow this sender. |

### Import

Import reads public GitHub repositories. Without a token, GitHub allows 60 requests an hour from the server's address, and a large import stops at that limit. A fine-grained personal access token with read access to public repositories raises the limit to 5000.

The test reads the token's rate limit from GitHub.

| Variable | Where | Default | Description |
|---|---|---|---|
| `GITHUB_TOKEN` | Settings page | — | A personal access token for import. It raises GitHub's rate limit from 60 to 5000 requests an hour. Read-only public access is enough. |

### Security scanning

The built-in scanner runs on every publish and import. An external scanner runs after it, and its findings join the same report. The tool must be installed on the server and must print SARIF to standard output.

- gitleaks finds secrets: `gitleaks dir {dir} --report-format sarif --report-path /dev/stdout --exit-code 0`
- Cisco skill-scanner finds risky agent instructions: `skill-scanner scan {dir} --format sarif`, installed with `pip install cisco-ai-skill-scanner`

The test runs the command on a sample skill and parses its output.

| Variable | Where | Default | Description |
|---|---|---|---|
| `EXTERNAL_SCAN_CMD` | Settings page | — | Runs on every publish and import in addition to the built-in scanner. {dir} is the skill directory, and the command must print SARIF. The tool must be installed on the server. |
| `EXTERNAL_SCAN_TIMEOUT` | Settings page | `60s` | A Go duration, for example 60s or 2m. |

### Quality gate

A version with a registered eval suite is held until a verified evaluation scores it. The floor is the lowest headline that releases it. A floor of 0 releases every verified report with a complete panel and no critical contract violation.

| Variable | Where | Default | Description |
|---|---|---|---|
| `QUALITY_FLOOR` | Settings page | `0` | The minimum headline a verified evaluation must reach to release a held version. 0 accepts any verified report with a complete panel and no critical contract violation. |

### Analytics

Activation events come from the hooks that `skael hook install` adds to each agent, and from runs that consumers report. The server deletes events older than the retention once a day.

| Variable | Where | Default | Description |
|---|---|---|---|
| `EVENT_RETENTION_DAYS` | Settings page | `90` | Activation events older than this are deleted daily. 0 keeps every event. |
| `ACTIVATION_RECORD_IDENTITY` | Settings page | `true` | Stores the person whose key sent each activation. Off stores no person, only the model, the harness and a hash of the machine. Turn it off where a works council or monitoring rule requires it. |
| `ACTIVATION_MEMBER_VISIBILITY` | Settings page | `aggregate` | Owners and admins always see everything recorded, and every member sees their own activations. Aggregates hide any model or harness that fewer than 5 people used. One of: `aggregate`, `person`. |

### Webhooks

By default, a delivery reaches only public HTTPS addresses, so a webhook cannot be used to reach your internal network. Allow private addresses only when a consumer runs on your own network. Link-local addresses, such as cloud metadata endpoints, stay refused.

| Variable | Where | Default | Description |
|---|---|---|---|
| `WEBHOOK_ALLOW_PRIVATE` | Settings page | `false` | Lets deliveries reach private addresses, loopback, shared address space and http URLs, for a consumer on your own network. Link-local addresses stay refused. |

### Install ping

The ping tells the skael project how many installs run and which versions they run. It sends one JSON object a day to `https://telemetry.skael.dev/v1/ping`:

```json
{
  "install_id": "a random UUID made at install",
  "version": "0.19.0",
  "postgres_version": "17.2",
  "workers": 2,
  "evals_configured": true
}
```

It sends no user, email, skill, repository, host name or IP address of yours beyond the connection itself. Sample data never counts. Turn it off here or with `TELEMETRY_ENABLED=false`.

| Variable | Where | Default | Description |
|---|---|---|---|
| `TELEMETRY_ENABLED` | Settings page | `true` | Once a day, sends the skael version, the Postgres version, the number of workers and whether evaluation is set up, with a random install id. Never names, skills or email addresses. |

### Rate limits

Each class also has a per-IP ceiling of ten times its limit, checked before the per-key budget, so one address cannot exceed it with many keys. Behind a proxy, set `TRUSTED_PROXIES`, or every client shares one budget.

| Variable | Where | Default | Description |
|---|---|---|---|
| `RATE_LIMIT_AUTH` | Settings page | `20` | For /api/auth/*, per IP address. |
| `RATE_LIMIT_EVENTS` | Settings page | `600` | For POST /api/events, per API key, else per IP address. |
| `RATE_LIMIT_READ` | Settings page | `300` | For GET and HEAD, per API key, else per IP address. |
| `RATE_LIMIT_WRITE` | Settings page | `60` | For every other method, per API key, else per IP address. |
| `RATE_LIMIT_SUITES` | Settings page | `20` | For POST /api/eval/suites, which accepts up to 10 MB a call. |

### Deployment

These settings are read before the server reads its database, or a wrong value could lock you out of this page. Set them in the environment and restart.

Without `SKAEL_MASTER_KEYS`, the server generates a master key on first start and writes it to `master.key` in the data directory. Back up that file with Postgres. If the key is lost, the server still starts, and this page lists each secret to enter again.

| Variable | Where | Default | Description |
|---|---|---|---|
| `DATABASE_URL` | env only, required yes | — | The Postgres connection string. |
| `LISTEN_ADDR` | env only | `:8080` | The HTTP listen address. |
| `SKAEL_MASTER_KEYS` | env only | — | Seals the secrets stored from the UI. Comma-separated id:base64key pairs of 32 bytes; the first seals, every one opens. Unset, the server generates a key file in the data directory. |
| `SKAEL_DATA_DIR` | env only | — | Where the server keeps the generated master key. Mount it on a volume. Unset uses the storage directory when STORAGE_PATH is a directory, else ./data. |
| `COOKIE_SECURE` | env only | `false` | Marks the session cookie Secure. Needs TLS in front: a browser refuses a Secure cookie over plain HTTP, which breaks sign-in. |
| `TRUSTED_PROXIES` | env only | — | CIDR blocks or addresses whose X-Forwarded-For, X-Real-IP and X-Forwarded-Proto are believed. Empty trusts none. Behind a proxy, set it, or every client shares one rate-limit bucket. |
| `CORS_ORIGINS` | env only | — | Comma-separated origins allowed to call the API from a browser. |

### Storage

A new install with no `STORAGE_PATH` stores archives in Postgres. Skill archives are at most 10 MB, so one Postgres backup then holds everything. An install that started with a directory keeps it.

Set `STORAGE_PATH=s3://bucket/prefix` for S3, MinIO, R2 or Spaces. A change of store does not move existing archives.

| Variable | Where | Default | Description |
|---|---|---|---|
| `STORAGE_PATH` | env only | — | Where archives live: a local directory, or s3://bucket/prefix. Unset on a new install stores archives in Postgres. An existing install keeps the store it started with. |
| `S3_ENDPOINT` | env only | `s3.amazonaws.com` | Set for MinIO, R2 or Spaces. No AWS_* fallback. |
| `S3_REGION` | env only | `us-east-1` | Falls back to AWS_REGION. |
| `S3_ACCESS_KEY_ID` | env only | — | Falls back to AWS_ACCESS_KEY_ID. Set both keys or neither; omit both for an IAM role. |
| `S3_SECRET_ACCESS_KEY` | env only | — | Falls back to AWS_SECRET_ACCESS_KEY. |
| `S3_USE_PATH_STYLE` | env only | `false` | true for MinIO. |
| `S3_USE_SSL` | env only | `true` | TLS to the S3 endpoint. |

### Database pool

The pool defaults suit one server replica. With several replicas, keep the sum of their maximum connections below the database's `max_connections`.

| Variable | Where | Default | Description |
|---|---|---|---|
| `DB_MAX_CONNS` | env only | `25` | The most connections in the pool. |
| `DB_MIN_CONNS` | env only | `5` | Idle connections the pool keeps. |
| `DB_MAX_CONN_LIFETIME` | env only | `1h` | A connection closes after this long. |
| `DB_MAX_CONN_IDLE_TIME` | env only | `30m` | An idle connection closes after this long. |
| `DB_HEALTH_CHECK_PERIOD` | env only | `1m` | How often the pool checks its connections. |

### Logging and metrics

The server writes JSON logs by default. `LOG_FORMAT=pretty` writes colored lines for development.

| Variable | Where | Default | Description |
|---|---|---|---|
| `LOG_LEVEL` | env only | `info` | The lowest level written. One of: `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic`. |
| `LOG_FORMAT` | env only | — | pretty writes colored console lines for development. Unset writes JSON. One of: `pretty`, `json`. |
| `LOG_PRETTY` | env only | `false` | The same as LOG_FORMAT=pretty. |
| `METRICS_ENABLED` | env only | `true` | false removes the /metrics endpoint. |

## Worker

`skael-worker` reads only its own environment.

### Worker

A worker runs evaluations. The server holds no Docker socket and no LLM key, so evaluations run only on workers. Settings → Workers → Add a worker creates a key and shows the command to start one.

Invalid durations and numbers stop the worker at start with the variable named.

#### Running the worker in a container

With the docker driver, the worker starts sandbox containers as siblings through a mounted `/var/run/docker.sock`. Two rules follow, and both apply to the docker driver only:

- Bind-mount `WORKER_RUN_ROOT` at the same path on both sides, for example `-v /var/lib/skael/run:/var/lib/skael/run`. Session workspaces are bind-mounted into each sandbox, and the host daemon resolves that path. The worker refuses to start in a container without it.
- Give credentials as environment variables, not as a mounted directory. A Claude subscription works in a container through `CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`.

| Variable | Where | Default | Description |
|---|---|---|---|
| `SKAEL_ENDPOINT` | env only, required yes | — | The skael server the worker claims jobs from. |
| `SKAEL_API_KEY` | env only, required yes | — | A key with permission to claim eval jobs. Settings → Workers → Add a worker creates one. |
| `WORKER_ID` | env only | `{hostname}-{pid}` | Names this worker in leases and on the Workers page. |
| `WORKER_LEASE` | env only | `5m` | How long a claimed job is held before another worker may take it. |
| `WORKER_POLL` | env only | `15s` | How often an idle worker asks for a job and checks in. |
| `WORKER_WORK_ROOT` | env only | `the OS temp directory` | Where eval workspaces are built. Never mounted into a sandbox. |
| `WORKER_RUN_ROOT` | env only, required in a container | `the OS temp directory` | Where session workspaces are created. They are bind-mounted into sandboxes, so in a container the path must be the same on the host. Required when containerized. |
| `WORKER_CONCURRENCY` | env only | `1` | Sandbox sessions at once within one eval. |
| `WORKER_GRADE_CONCURRENCY` | env only | `8` | Judge calls at once. A judge call is bound by the account's rate limit, not by the host. |
| `WORKER_CONTEST_PARALLEL` | env only | `2` | Each candidate runs its own sessions, so this multiplies the sandbox load. |

### Worker LLM credentials

The server chooses the judge and the panel. A worker holds only the credentials that the server's providers name, and reports which ones it has.

A worker that sets `ANTHROPIC_BASE_URL` or `LLM_MODEL` keeps its own configuration, and the Workers page marks it as pinned by env.

| Variable | Where | Default | Description |
|---|---|---|---|
| `ANTHROPIC_API_KEY` | env only | — | The credential of the Anthropic API preset, sent as x-api-key. |
| `ANTHROPIC_AUTH_TOKEN` | env only | — | A gateway credential sent as a bearer token, such as an OpenRouter key. |
| `ANTHROPIC_BASE_URL` | env only | — | Set on a worker, it pins that worker to its own gateway and the server's providers do not apply to it. |
| `LLM_MODEL` | env only | — | Set on a worker, it pins that worker's panel, most capable first, and the server's panel does not apply to it. |
| `CLAUDE_CODE_OAUTH_TOKEN` | env only | — | From claude setup-token. The credential of the Claude subscription provider, for panel members only. |

### Sandbox driver

The docker driver needs a Docker socket on the worker's host. The kubernetes driver schedules each session as a pod and needs no socket. The northflank driver runs each session as a service in a Northflank project.

| Variable | Where | Default | Description |
|---|---|---|---|
| `SANDBOX_DRIVER` | env only | `docker` | Where an eval session runs. One of: `docker`, `kubernetes`, `northflank`. |

### Kubernetes driver

Grant the worker's service account only `deploy/kubernetes/worker-rbac.yaml`, and put nothing but session pods in the namespace: `pods/exec` is the driver's whole privilege, and whoever holds it can run commands in any pod there.

The driver uses a published image and builds none. The default is the image the release publishes, tagged by its base version and never by the release version, so a release does not change the eval environment.

| Variable | Where | Default | Description |
|---|---|---|---|
| `SANDBOX_K8S_NAMESPACE` | env only, required with kubernetes | — | The namespace session pods run in. It must hold nothing but session pods. |
| `SANDBOX_K8S_IMAGE` | env only | `the published whetstone base image` | The image a session pod runs. It must already be in a registry. |
| `SANDBOX_K8S_PULL_SECRET` | env only | — | An image pull secret in the namespace, for a private registry. |
| `SANDBOX_K8S_RUNTIME_CLASS` | env only | — | runtimeClassName for a hardware-isolated runtime, for example kata. |
| `SANDBOX_K8S_TOLERATIONS` | env only | — | Tolerations for session and proxy pods, as a JSON list in the Kubernetes shape, for example [{"key":"dedicated","operator":"Equal","value":"evals","effect":"NoSchedule"}]. |
| `SANDBOX_K8S_NODE_SELECTOR` | env only | — | Node labels session and proxy pods must match, as a JSON object, for example {"agentpool":"evals"}. |
| `SANDBOX_K8S_HARDWARE_ISOLATED` | env only | — | Asserts the runtime is hardware-isolated. Accepted only as the exact lowercase string `true`; any other value reads as unasserted and the driver refuses a run that needs it. |
| `SANDBOX_K8S_NETWORK_POLICY` | env only | — | Asserts the cluster's CNI enforces NetworkPolicy. Check it with whetstone doctor --check-egress. Accepted only as the exact lowercase string `true`; any other value reads as unasserted and the driver refuses a run that needs it. |

### Northflank driver

Give the driver a project of its own. It deletes every service with its owner label, including orphans it finds at start. `whetstone doctor` reports only whether the token is set.

| Variable | Where | Default | Description |
|---|---|---|---|
| `SANDBOX_NF_TOKEN` | env only, required with northflank | — | The Northflank API token. |
| `SANDBOX_NF_PROJECT` | env only, required with northflank | — | Give sessions a project of their own: the driver deletes every service with its owner label. |
| `SANDBOX_NF_IMAGE` | env only | `the published whetstone base image` | The base image a session runs. |
| `SANDBOX_NF_REGISTRY_CREDENTIAL` | env only | — | A saved Northflank credential, for a private registry. |
| `SANDBOX_NF_PLAN` | env only | `nf-compute-20` | The resource plan of each session service. |
| `SANDBOX_NF_CLI` | env only | `northflank` | The CLI the driver uses for workspace transfer. |
| `SANDBOX_NF_ALLOWED_DOMAINS` | env only, required for restricted runs | — | The domains your project's egress policy permits. |
| `SANDBOX_NF_NETWORK_POLICY` | env only | — | Asserts the project's egress policy enforces the allowed domains. Accepted only as the exact lowercase string `true`; any other value reads as unasserted and the driver refuses a run that needs it. |
| `SANDBOX_NF_HARDWARE_ISOLATED` | env only | — | Asserts the plan is hardware-isolated. Accepted only as the exact lowercase string `true`; any other value reads as unasserted and the driver refuses a run that needs it. |
