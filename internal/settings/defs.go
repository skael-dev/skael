// Package settings is the one source of every setting: its env name, type,
// default, help, and whether the UI may change it. The server's validation, the
// settings page, configuration.md and .env.example all come from Defs.
package settings

// Kind is how a value is parsed and edited.
type Kind string

const (
	KindString   Kind = "string"
	KindURL      Kind = "url"
	KindBool     Kind = "bool"
	KindInt      Kind = "int"
	KindFloat    Kind = "float"
	KindDuration Kind = "duration"
	KindEnum     Kind = "enum"
	KindList     Kind = "list"
	KindSecret   Kind = "secret"
)

// Scope is who owns a setting. On a self-hosted install the one org is the instance.
type Scope string

const (
	ScopeInstance Scope = "instance"
	ScopeOrg      Scope = "org"
)

// Binary is the program that reads a setting.
type Binary string

const (
	BinaryServer Binary = "server"
	BinaryWorker Binary = "worker"
)

// SettingsOption is one known value with the label the UI shows for it.
type SettingsOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Def is one setting.
type Def struct {
	Key     string
	Env     string
	Kind    Kind
	Section string
	Label   string
	// Help is the one line under the field. Doc, when set, is the longer
	// description in configuration.md.
	Help     string
	Doc      string
	Default  string
	Required string
	// Options are the only values of an enum. Suggestions are presets for a
	// free value, such as a scanner command.
	Options     []SettingsOption
	Suggestions []SettingsOption
	Min, Max    *float64
	// EnvOnly settings are read before the database or would lock the admin
	// out; the UI shows them read-only.
	EnvOnly bool
	// StrictEnv refuses to start on an invalid env value instead of using
	// the default.
	StrictEnv bool
	Scope     Scope
	Binary    Binary
}

// Section groups settings on the page and in the docs.
type Section struct {
	ID     string
	Title  string
	Binary Binary
	// Test names the section's test action, if it has one.
	Test string
}

func num(f float64) *float64 { return &f }

// Sections in page and docs order.
var Sections = []Section{
	{ID: "general", Title: "General", Binary: BinaryServer},
	{ID: "signup", Title: "Sign-up", Binary: BinaryServer},
	{ID: "signin", Title: "Sign-in providers", Binary: BinaryServer, Test: "oidc"},
	{ID: "email", Title: "Email", Binary: BinaryServer, Test: "smtp"},
	{ID: "import", Title: "Import", Binary: BinaryServer, Test: "github_token"},
	{ID: "scanning", Title: "Security scanning", Binary: BinaryServer, Test: "scanner"},
	{ID: "quality", Title: "Quality gate", Binary: BinaryServer},
	{ID: "analytics", Title: "Analytics", Binary: BinaryServer},
	{ID: "webhooks", Title: "Webhooks", Binary: BinaryServer},
	{ID: "telemetry", Title: "Install ping", Binary: BinaryServer},
	{ID: "rate_limits", Title: "Rate limits", Binary: BinaryServer},
	{ID: "deployment", Title: "Deployment", Binary: BinaryServer},
	{ID: "storage", Title: "Storage", Binary: BinaryServer},
	{ID: "database", Title: "Database pool", Binary: BinaryServer},
	{ID: "logging", Title: "Logging and metrics", Binary: BinaryServer},
	{ID: "worker", Title: "Worker", Binary: BinaryWorker},
	{ID: "worker_llm", Title: "Worker LLM credentials", Binary: BinaryWorker},
	{ID: "sandbox", Title: "Sandbox driver", Binary: BinaryWorker},
	{ID: "sandbox_k8s", Title: "Kubernetes driver", Binary: BinaryWorker},
	{ID: "sandbox_nf", Title: "Northflank driver", Binary: BinaryWorker},
}

const trueOnly = "Accepted only as the exact lowercase string `true`; any other value reads as unasserted and the driver refuses a run that needs it."

// Defs in page and docs order within each section.
var Defs = []Def{
	{Key: "public_url", Env: "PUBLIC_URL", Kind: KindURL, Section: "general", Label: "Public URL",
		Help:     "The URL people use to reach skael. Sign-in callbacks, email links and GitHub webhooks are built from it.",
		Required: "with SSO, SMTP or GitHub"},

	{Key: "signup.disabled", Env: "DISABLE_SIGNUP", Kind: KindBool, Section: "signup", Label: "Disable sign-up",
		Help:    "Refuses new password, Google and GitHub accounts. OIDC stays open, because the identity provider is the gate.",
		Default: "false"},
	{Key: "signup.allowed_domains", Env: "SIGNUP_ALLOWED_DOMAINS", Kind: KindList, Section: "signup", Label: "Allowed email domains",
		Help: "Comma-separated. A new account from any other domain is refused on every sign-in path."},

	{Key: "oidc.issuer", Env: "OIDC_ISSUER", Kind: KindURL, Section: "signin", Label: "OIDC issuer",
		Help: "The issuer URL of any OpenID Connect provider. The first sign-in creates a member, even with sign-up disabled."},
	{Key: "oidc.client_id", Env: "OIDC_CLIENT_ID", Kind: KindString, Section: "signin", Label: "OIDC client ID",
		Help: "From the application you register at the provider."},
	{Key: "oidc.client_secret", Env: "OIDC_CLIENT_SECRET", Kind: KindSecret, Section: "signin", Label: "OIDC client secret",
		Help: "From the application you register at the provider."},
	{Key: "google.client_id", Env: "GOOGLE_CLIENT_ID", Kind: KindString, Section: "signin", Label: "Google client ID",
		Help: "An OAuth client of type Web application in Google Cloud."},
	{Key: "google.client_secret", Env: "GOOGLE_CLIENT_SECRET", Kind: KindSecret, Section: "signin", Label: "Google client secret",
		Help: "From the same Google OAuth client."},
	{Key: "github.client_id", Env: "GITHUB_CLIENT_ID", Kind: KindString, Section: "signin", Label: "GitHub client ID",
		Help: "A GitHub OAuth App. The GitHub App for git sync and the import token are separate."},
	{Key: "github.client_secret", Env: "GITHUB_CLIENT_SECRET", Kind: KindSecret, Section: "signin", Label: "GitHub client secret",
		Help: "From the same GitHub OAuth App."},

	{Key: "smtp.host", Env: "SMTP_HOST", Kind: KindString, Section: "email", Label: "SMTP host",
		Help: "Turns on password reset and email change. For example smtp.postmarkapp.com or email-smtp.us-east-1.amazonaws.com."},
	{Key: "smtp.port", Env: "SMTP_PORT", Kind: KindInt, Section: "email", Label: "SMTP port",
		Help: "587 uses STARTTLS, 465 uses TLS from the first byte.", Default: "587", Min: num(1), Max: num(65535),
		Suggestions: []SettingsOption{{"587", "587 (STARTTLS)"}, {"465", "465 (TLS)"}, {"2525", "2525 (STARTTLS, when 587 is blocked)"}, {"25", "25 (relay)"}}},
	{Key: "smtp.username", Env: "SMTP_USERNAME", Kind: KindString, Section: "email", Label: "SMTP username",
		Help: "Leave empty for a relay without authentication."},
	{Key: "smtp.password", Env: "SMTP_PASSWORD", Kind: KindSecret, Section: "email", Label: "SMTP password",
		Help: "The SMTP password or the provider's API token."},
	{Key: "smtp.from", Env: "SMTP_FROM", Kind: KindString, Section: "email", Label: "From address",
		Help: "For example Skael <skael@example.com>. The provider must allow this sender."},

	{Key: "github_token", Env: "GITHUB_TOKEN", Kind: KindSecret, Section: "import", Label: "GitHub token",
		Help: "A personal access token for import. It raises GitHub's rate limit from 60 to 5000 requests an hour. Read-only public access is enough."},

	{Key: "external_scan.cmd", Env: "EXTERNAL_SCAN_CMD", Kind: KindString, Section: "scanning", Label: "External scanner command",
		Help: "Runs on every publish and import in addition to the built-in scanner. {dir} is the skill directory, and the command must print SARIF. The tool must be installed on the server.",
		Suggestions: []SettingsOption{
			{"gitleaks dir {dir} --report-format sarif --report-path /dev/stdout --exit-code 0", "gitleaks"},
			{"skill-scanner scan {dir} --format sarif", "Cisco skill-scanner"},
		}},
	{Key: "external_scan.timeout", Env: "EXTERNAL_SCAN_TIMEOUT", Kind: KindDuration, Section: "scanning", Label: "Scanner timeout",
		Help: "A Go duration, for example 60s or 2m.", Default: "60s"},

	{Key: "quality_floor", Env: "QUALITY_FLOOR", Kind: KindFloat, Section: "quality", Label: "Quality floor",
		Help:    "The minimum headline a verified evaluation must reach to release a held version. 0 accepts any verified report with a complete panel and no critical contract violation.",
		Default: "0", Min: num(0), Max: num(100), StrictEnv: true},

	{Key: "event_retention_days", Env: "EVENT_RETENTION_DAYS", Kind: KindInt, Section: "analytics", Label: "Event retention (days)",
		Help: "Activation events older than this are deleted daily. 0 keeps every event.", Default: "90", Min: num(0)},

	{Key: "activation.record_identity", Env: "ACTIVATION_RECORD_IDENTITY", Kind: KindBool, Section: "analytics", Label: "Record who ran a skill",
		Help:    "Stores the person whose key sent each activation. Off stores no person, only the model, the harness and a hash of the machine. Turn it off where a works council or monitoring rule requires it.",
		Default: "true", Scope: ScopeOrg},
	{Key: "activation.member_visibility", Env: "ACTIVATION_MEMBER_VISIBILITY", Kind: KindEnum, Section: "analytics", Label: "What members see",
		Help:    "Owners and admins always see everything recorded, and every member sees their own activations. Aggregates hide any model or harness that fewer than 5 people used.",
		Default: "aggregate", Scope: ScopeOrg,
		Options: []SettingsOption{{"aggregate", "Aggregates only"}, {"person", "Per person"}}},
	{Key: "webhooks.allow_private", Env: "WEBHOOK_ALLOW_PRIVATE", Kind: KindBool, Section: "webhooks", Label: "Allow private addresses",
		Help:    "Lets deliveries reach private addresses, loopback, shared address space and http URLs, for a consumer on your own network. Link-local addresses stay refused.",
		Default: "false"},

	{Key: "telemetry.enabled", Env: "TELEMETRY_ENABLED", Kind: KindBool, Section: "telemetry", Label: "Send the install ping",
		Help:    "Once a day, sends the skael version, the Postgres version, the number of workers and whether evaluation is set up, with a random install id. Never names, skills or email addresses.",
		Default: "true"},

	{Key: "rate_limit.auth", Env: "RATE_LIMIT_AUTH", Kind: KindInt, Section: "rate_limits", Label: "Sign-in requests per minute",
		Help: "For /api/auth/*, per IP address.", Default: "20", Min: num(1)},
	{Key: "rate_limit.events", Env: "RATE_LIMIT_EVENTS", Kind: KindInt, Section: "rate_limits", Label: "Activation events per minute",
		Help: "For POST /api/events, per API key, else per IP address.", Default: "600", Min: num(1)},
	{Key: "rate_limit.read", Env: "RATE_LIMIT_READ", Kind: KindInt, Section: "rate_limits", Label: "Reads per minute",
		Help: "For GET and HEAD, per API key, else per IP address.", Default: "300", Min: num(1)},
	{Key: "rate_limit.write", Env: "RATE_LIMIT_WRITE", Kind: KindInt, Section: "rate_limits", Label: "Writes per minute",
		Help: "For every other method, per API key, else per IP address.", Default: "60", Min: num(1)},
	{Key: "rate_limit.suites", Env: "RATE_LIMIT_SUITES", Kind: KindInt, Section: "rate_limits", Label: "Eval suite uploads per minute",
		Help: "For POST /api/eval/suites, which accepts up to 10 MB a call.", Default: "20", Min: num(1)},

	{Key: "database_url", Env: "DATABASE_URL", Kind: KindSecret, Section: "deployment", Label: "Database URL",
		Help: "The Postgres connection string.", Required: "yes", EnvOnly: true},
	{Key: "listen_addr", Env: "LISTEN_ADDR", Kind: KindString, Section: "deployment", Label: "Listen address",
		Help: "The HTTP listen address.", Default: ":8080", EnvOnly: true},
	{Key: "master_keys", Env: "SKAEL_MASTER_KEYS", Kind: KindSecret, Section: "deployment", Label: "Master keys",
		Help:    "Seals the secrets stored from the UI. Comma-separated id:base64key pairs of 32 bytes; the first seals, every one opens. Unset, the server generates a key file in the data directory.",
		EnvOnly: true},
	{Key: "data_dir", Env: "SKAEL_DATA_DIR", Kind: KindString, Section: "deployment", Label: "Data directory",
		Help:    "Where the server keeps the generated master key. Mount it on a volume. Unset uses the storage directory when STORAGE_PATH is a directory, else ./data.",
		EnvOnly: true},
	{Key: "cookie_secure", Env: "COOKIE_SECURE", Kind: KindBool, Section: "deployment", Label: "Secure cookie",
		Help:    "Marks the session cookie Secure. Needs TLS in front: a browser refuses a Secure cookie over plain HTTP, which breaks sign-in.",
		Default: "false", EnvOnly: true},
	{Key: "trusted_proxies", Env: "TRUSTED_PROXIES", Kind: KindList, Section: "deployment", Label: "Trusted proxies",
		Help:    "CIDR blocks or addresses whose X-Forwarded-For, X-Real-IP and X-Forwarded-Proto are believed. Empty trusts none. Behind a proxy, set it, or every client shares one rate-limit bucket.",
		EnvOnly: true},
	{Key: "cors_origins", Env: "CORS_ORIGINS", Kind: KindList, Section: "deployment", Label: "CORS origins",
		Help: "Comma-separated origins allowed to call the API from a browser.", EnvOnly: true},

	{Key: "storage_path", Env: "STORAGE_PATH", Kind: KindString, Section: "storage", Label: "Storage path",
		Help:    "Where archives live: a local directory, or s3://bucket/prefix. Unset on a new install stores archives in Postgres. An existing install keeps the store it started with.",
		EnvOnly: true},
	{Key: "s3.endpoint", Env: "S3_ENDPOINT", Kind: KindString, Section: "storage", Label: "S3 endpoint",
		Help: "Set for MinIO, R2 or Spaces. No AWS_* fallback.", Default: "s3.amazonaws.com", EnvOnly: true},
	{Key: "s3.region", Env: "S3_REGION", Kind: KindString, Section: "storage", Label: "S3 region",
		Help: "Falls back to AWS_REGION.", Default: "us-east-1", EnvOnly: true},
	{Key: "s3.access_key_id", Env: "S3_ACCESS_KEY_ID", Kind: KindSecret, Section: "storage", Label: "S3 access key ID",
		Help: "Falls back to AWS_ACCESS_KEY_ID. Set both keys or neither; omit both for an IAM role.", EnvOnly: true},
	{Key: "s3.secret_access_key", Env: "S3_SECRET_ACCESS_KEY", Kind: KindSecret, Section: "storage", Label: "S3 secret access key",
		Help: "Falls back to AWS_SECRET_ACCESS_KEY.", EnvOnly: true},
	{Key: "s3.use_path_style", Env: "S3_USE_PATH_STYLE", Kind: KindBool, Section: "storage", Label: "Path-style addressing",
		Help: "true for MinIO.", Default: "false", EnvOnly: true},
	{Key: "s3.use_ssl", Env: "S3_USE_SSL", Kind: KindBool, Section: "storage", Label: "Use TLS",
		Help: "TLS to the S3 endpoint.", Default: "true", EnvOnly: true},

	{Key: "db.max_conns", Env: "DB_MAX_CONNS", Kind: KindInt, Section: "database", Label: "Maximum connections",
		Help: "The most connections in the pool.", Default: "25", Min: num(1), EnvOnly: true},
	{Key: "db.min_conns", Env: "DB_MIN_CONNS", Kind: KindInt, Section: "database", Label: "Minimum connections",
		Help: "Idle connections the pool keeps.", Default: "5", Min: num(0), EnvOnly: true},
	{Key: "db.max_conn_lifetime", Env: "DB_MAX_CONN_LIFETIME", Kind: KindDuration, Section: "database", Label: "Connection lifetime",
		Help: "A connection closes after this long.", Default: "1h", EnvOnly: true},
	{Key: "db.max_conn_idle_time", Env: "DB_MAX_CONN_IDLE_TIME", Kind: KindDuration, Section: "database", Label: "Connection idle time",
		Help: "An idle connection closes after this long.", Default: "30m", EnvOnly: true},
	{Key: "db.health_check_period", Env: "DB_HEALTH_CHECK_PERIOD", Kind: KindDuration, Section: "database", Label: "Health check period",
		Help: "How often the pool checks its connections.", Default: "1m", EnvOnly: true},

	{Key: "log.level", Env: "LOG_LEVEL", Kind: KindEnum, Section: "logging", Label: "Log level",
		Help: "The lowest level written.", Default: "info", EnvOnly: true,
		Options: []SettingsOption{{"trace", "trace"}, {"debug", "debug"}, {"info", "info"}, {"warn", "warn"}, {"error", "error"}, {"fatal", "fatal"}, {"panic", "panic"}}},
	{Key: "log.format", Env: "LOG_FORMAT", Kind: KindEnum, Section: "logging", Label: "Log format",
		Help: "pretty writes colored console lines for development. Unset writes JSON.", EnvOnly: true,
		Options: []SettingsOption{{"pretty", "pretty"}, {"json", "json"}}},
	{Key: "log.pretty", Env: "LOG_PRETTY", Kind: KindBool, Section: "logging", Label: "Pretty logs",
		Help: "The same as LOG_FORMAT=pretty.", Default: "false", EnvOnly: true},
	{Key: "metrics.enabled", Env: "METRICS_ENABLED", Kind: KindBool, Section: "logging", Label: "Prometheus metrics",
		Help: "false removes the /metrics endpoint.", Default: "true", EnvOnly: true},

	{Key: "worker.endpoint", Env: "SKAEL_ENDPOINT", Kind: KindURL, Section: "worker", Label: "Server URL",
		Help: "The skael server the worker claims jobs from.", Required: "yes", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.api_key", Env: "SKAEL_API_KEY", Kind: KindSecret, Section: "worker", Label: "API key",
		Help: "A key with permission to claim eval jobs. Settings → Workers → Add a worker creates one.", Required: "yes", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.id", Env: "WORKER_ID", Kind: KindString, Section: "worker", Label: "Worker ID",
		Help: "Names this worker in leases and on the Workers page.", Default: "{hostname}-{pid}", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.lease", Env: "WORKER_LEASE", Kind: KindDuration, Section: "worker", Label: "Lease",
		Help: "How long a claimed job is held before another worker may take it.", Default: "5m", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.poll", Env: "WORKER_POLL", Kind: KindDuration, Section: "worker", Label: "Poll interval",
		Help: "How often an idle worker asks for a job and checks in.", Default: "15s", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.work_root", Env: "WORKER_WORK_ROOT", Kind: KindString, Section: "worker", Label: "Work root",
		Help: "Where eval workspaces are built. Never mounted into a sandbox.", Default: "the OS temp directory", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.run_root", Env: "WORKER_RUN_ROOT", Kind: KindString, Section: "worker", Label: "Run root",
		Help:     "Where session workspaces are created. They are bind-mounted into sandboxes, so in a container the path must be the same on the host. Required when containerized.",
		Required: "in a container", Default: "the OS temp directory", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.concurrency", Env: "WORKER_CONCURRENCY", Kind: KindInt, Section: "worker", Label: "Session concurrency",
		Help: "Sandbox sessions at once within one eval.", Default: "1", Min: num(1), EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.grade_concurrency", Env: "WORKER_GRADE_CONCURRENCY", Kind: KindInt, Section: "worker", Label: "Judge concurrency",
		Help: "Judge calls at once. A judge call is bound by the account's rate limit, not by the host.", Default: "8", Min: num(1), EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.contest_parallel", Env: "WORKER_CONTEST_PARALLEL", Kind: KindInt, Section: "worker", Label: "Contest candidates at once",
		Help: "Each candidate runs its own sessions, so this multiplies the sandbox load.", Default: "2", Min: num(1), EnvOnly: true, Binary: BinaryWorker},

	{Key: "worker.anthropic_api_key", Env: "ANTHROPIC_API_KEY", Kind: KindSecret, Section: "worker_llm", Label: "Anthropic API key",
		Help: "The credential of the Anthropic API preset, sent as x-api-key.", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.anthropic_auth_token", Env: "ANTHROPIC_AUTH_TOKEN", Kind: KindSecret, Section: "worker_llm", Label: "Bearer token",
		Help: "A gateway credential sent as a bearer token, such as an OpenRouter key.", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.anthropic_base_url", Env: "ANTHROPIC_BASE_URL", Kind: KindURL, Section: "worker_llm", Label: "Gateway base URL",
		Help: "Set on a worker, it pins that worker to its own gateway and the server's providers do not apply to it.", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.llm_model", Env: "LLM_MODEL", Kind: KindList, Section: "worker_llm", Label: "Model panel",
		Help: "Set on a worker, it pins that worker's panel, most capable first, and the server's panel does not apply to it.", EnvOnly: true, Binary: BinaryWorker},
	{Key: "worker.claude_code_oauth_token", Env: "CLAUDE_CODE_OAUTH_TOKEN", Kind: KindSecret, Section: "worker_llm", Label: "Claude subscription token",
		Help: "From claude setup-token. The credential of the Claude subscription provider, for panel members only.", EnvOnly: true, Binary: BinaryWorker},

	{Key: "sandbox.driver", Env: "SANDBOX_DRIVER", Kind: KindEnum, Section: "sandbox", Label: "Sandbox driver",
		Help: "Where an eval session runs.", Default: "docker", EnvOnly: true, Binary: BinaryWorker,
		Options: []SettingsOption{{"docker", "docker (sibling containers on the local daemon)"}, {"kubernetes", "kubernetes (a pod through the cluster API)"}, {"northflank", "northflank (a service in a Northflank project)"}}},

	{Key: "sandbox.k8s.namespace", Env: "SANDBOX_K8S_NAMESPACE", Kind: KindString, Section: "sandbox_k8s", Label: "Namespace",
		Help: "The namespace session pods run in. It must hold nothing but session pods.", Required: "with kubernetes", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.k8s.image", Env: "SANDBOX_K8S_IMAGE", Kind: KindString, Section: "sandbox_k8s", Label: "Image",
		Help: "The image a session pod runs. It must already be in a registry.", Default: "the published whetstone base image", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.k8s.pull_secret", Env: "SANDBOX_K8S_PULL_SECRET", Kind: KindString, Section: "sandbox_k8s", Label: "Pull secret",
		Help: "An image pull secret in the namespace, for a private registry.", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.k8s.runtime_class", Env: "SANDBOX_K8S_RUNTIME_CLASS", Kind: KindString, Section: "sandbox_k8s", Label: "Runtime class",
		Help: "runtimeClassName for a hardware-isolated runtime, for example kata.", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.k8s.tolerations", Env: "SANDBOX_K8S_TOLERATIONS", Kind: KindString, Section: "sandbox_k8s", Label: "Tolerations",
		Help: `Tolerations for session and proxy pods, as a JSON list in the Kubernetes shape, for example [{"key":"dedicated","operator":"Equal","value":"evals","effect":"NoSchedule"}].`, EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.k8s.node_selector", Env: "SANDBOX_K8S_NODE_SELECTOR", Kind: KindString, Section: "sandbox_k8s", Label: "Node selector",
		Help: `Node labels session and proxy pods must match, as a JSON object, for example {"agentpool":"evals"}.`, EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.k8s.hardware_isolated", Env: "SANDBOX_K8S_HARDWARE_ISOLATED", Kind: KindString, Section: "sandbox_k8s", Label: "Hardware isolated",
		Help: "Asserts the runtime is hardware-isolated. " + trueOnly, EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.k8s.network_policy", Env: "SANDBOX_K8S_NETWORK_POLICY", Kind: KindString, Section: "sandbox_k8s", Label: "Network policy enforced",
		Help: "Asserts the cluster's CNI enforces NetworkPolicy. Check it with whetstone doctor --check-egress. " + trueOnly, EnvOnly: true, Binary: BinaryWorker},

	{Key: "sandbox.nf.token", Env: "SANDBOX_NF_TOKEN", Kind: KindSecret, Section: "sandbox_nf", Label: "API token",
		Help: "The Northflank API token.", Required: "with northflank", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.nf.project", Env: "SANDBOX_NF_PROJECT", Kind: KindString, Section: "sandbox_nf", Label: "Project",
		Help: "Give sessions a project of their own: the driver deletes every service with its owner label.", Required: "with northflank", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.nf.image", Env: "SANDBOX_NF_IMAGE", Kind: KindString, Section: "sandbox_nf", Label: "Image",
		Help: "The base image a session runs.", Default: "the published whetstone base image", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.nf.registry_credential", Env: "SANDBOX_NF_REGISTRY_CREDENTIAL", Kind: KindString, Section: "sandbox_nf", Label: "Registry credential",
		Help: "A saved Northflank credential, for a private registry.", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.nf.plan", Env: "SANDBOX_NF_PLAN", Kind: KindString, Section: "sandbox_nf", Label: "Plan",
		Help: "The resource plan of each session service.", Default: "nf-compute-20", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.nf.cli", Env: "SANDBOX_NF_CLI", Kind: KindString, Section: "sandbox_nf", Label: "CLI path",
		Help: "The CLI the driver uses for workspace transfer.", Default: "northflank", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.nf.allowed_domains", Env: "SANDBOX_NF_ALLOWED_DOMAINS", Kind: KindList, Section: "sandbox_nf", Label: "Allowed domains",
		Help: "The domains your project's egress policy permits.", Required: "for restricted runs", EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.nf.network_policy", Env: "SANDBOX_NF_NETWORK_POLICY", Kind: KindString, Section: "sandbox_nf", Label: "Network policy enforced",
		Help: "Asserts the project's egress policy enforces the allowed domains. " + trueOnly, EnvOnly: true, Binary: BinaryWorker},
	{Key: "sandbox.nf.hardware_isolated", Env: "SANDBOX_NF_HARDWARE_ISOLATED", Kind: KindString, Section: "sandbox_nf", Label: "Hardware isolated",
		Help: "Asserts the plan is hardware-isolated. " + trueOnly, EnvOnly: true, Binary: BinaryWorker},
}

var byKey = func() map[string]*Def {
	m := make(map[string]*Def, len(Defs))
	for i := range Defs {
		d := &Defs[i]
		if d.Scope == "" {
			d.Scope = ScopeInstance
		}
		if d.Binary == "" {
			d.Binary = BinaryServer
		}
		m[d.Key] = d
	}
	return m
}()

// Lookup returns the definition of key, or nil.
func Lookup(key string) *Def { return byKey[key] }

// SectionByID returns the section, or nil.
func SectionByID(id string) *Section {
	for i := range Sections {
		if Sections[i].ID == id {
			return &Sections[i]
		}
	}
	return nil
}
