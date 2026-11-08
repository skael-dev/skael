package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	authall "github.com/alternayte/auth-all"
	"github.com/alternayte/auth-all/apierr"
	"github.com/alternayte/auth-all/email"
	"github.com/alternayte/auth-all/events"
	"github.com/alternayte/auth-all/hook"
	"github.com/alternayte/auth-all/oauth"
	"github.com/alternayte/auth-all/oauth/github"
	"github.com/alternayte/auth-all/oauth/google"
	"github.com/alternayte/auth-all/oauth/oidc"
	"github.com/alternayte/auth-all/plugin"
	"github.com/alternayte/auth-all/plugins/admin"
	"github.com/alternayte/auth-all/plugins/roles"
	"github.com/alternayte/auth-all/ratelimit"
	"github.com/alternayte/auth-all/ratelimit/storelimit"
	"github.com/alternayte/auth-all/schema"
	"github.com/alternayte/auth-all/store"
	"github.com/alternayte/auth-all/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/audit"
)

// oidcProviderID names the generic OIDC provider, which is the signup exception.
const oidcProviderID = "oidc"

// PeopleConfig is the part of the server configuration auth-all reads.
type PeopleConfig struct {
	PublicURL      string
	CookieSecure   bool
	DisableSignup  bool
	AllowedDomains []string
	TrustedOrigins []string
	TrustedProxies []string

	OIDCIssuer, OIDCClientID, OIDCClientSecret string
	GoogleClientID, GoogleClientSecret         string
	GitHubClientID, GitHubClientSecret         string
	EmailSender                                email.Sender
	// Inspecting builds routes for the OpenAPI document with no database.
	Inspecting bool
	// FirstPerson, when set, must accept the creation of the first person.
	// The server uses it to require the claim code.
	FirstPerson func(ctx context.Context) error
	// Live, when set, returns the public URL, the signup rules, the sign-in
	// providers and the email sender on each use, so a settings change applies
	// without a restart. The other fields of its result are ignored.
	Live func() PeopleConfig
}

// SignInMethods says which sign-in methods an instance offers.
type SignInMethods struct {
	Password       bool     `json:"password"`
	Signup         bool     `json:"signup"`
	ForgotPassword bool     `json:"forgot_password"`
	Providers      []string `json:"providers"`
}

// People is the auth-all instance behind every person's account.
type People struct {
	Auth  *authall.Auth
	Admin *admin.Plugin
	cfg   PeopleConfig

	mu        sync.Mutex
	providerK string
	providerV []oauth.Provider
}

// current returns the settings in effect now.
func (p *People) current() PeopleConfig {
	if p.cfg.Live != nil {
		return p.cfg.Live()
	}
	return p.cfg
}

// providers returns the configured providers. An OIDC provider discovers its
// endpoints on first use, so the same objects are kept until the settings change.
func (p *People) providers() []oauth.Provider {
	c := p.current()
	key := strings.Join([]string{c.OIDCIssuer, c.OIDCClientID, c.OIDCClientSecret,
		c.GoogleClientID, c.GoogleClientSecret, c.GitHubClientID, c.GitHubClientSecret}, "\x00")
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.providerV == nil || p.providerK != key {
		p.providerK, p.providerV = key, c.providers()
	}
	return p.providerV
}

// NewPeople builds auth-all with Skael's roles, key resolver and signup rules.
func NewPeople(pool *pgxpool.Pool, keys *KeyStore, cfg PeopleConfig) (*People, error) {
	var s store.Store
	if pool == nil {
		s = postgres.New((*sql.DB)(nil))
	} else {
		var err error
		if s, err = postgres.NewPool(pool, postgres.AllowStatementCache()); err != nil {
			return nil, fmt.Errorf("auth.NewPeople: %w", err)
		}
	}
	limiter, err := storelimit.New(s, ratelimit.DefaultSignInRules())
	if err != nil {
		return nil, fmt.Errorf("auth.NewPeople: %w", err)
	}

	adm := admin.New(admin.AdminRole(RoleOwner))
	secure := cfg.CookieSecure
	opts := []authall.Option{
		authall.WithStore(s),
		authall.WithSchema(schema.Options{Prefix: "auth_", IDType: schema.IDUUID}),
		authall.WithUserFields(schema.UserField{Name: "kind", Type: schema.TypeText, Default: "'human'"}),
		authall.WithSchemaCheck(authall.SchemaCheckCatalog),
		authall.WithEmailPassword(),
		authall.WithTOTP(),
		authall.WithCookie(authall.CookieOptions{Secure: &secure}),
		authall.WithRateLimiter(limiter),
		authall.WithBrowserErrorRedirect("/login"),
		authall.WithLogger(slog.Default()),
		authall.WithPlugins(
			roles.New(roles.Hierarchy(RoleMember, RoleAdmin, RoleOwner), roles.Default(RoleMember)),
			adm,
			&keyPlugin{keys: keys},
		),
	}
	p := &People{Admin: adm, cfg: cfg}
	opts = append(opts,
		authall.WithBaseURLSource(func(context.Context) (string, error) { return p.current().PublicURL, nil }),
		authall.WithEmailSenderSource(func(context.Context) (email.Sender, error) { return p.current().EmailSender, nil }),
		authall.WithProviderSource(func(context.Context) ([]oauth.Provider, error) { return p.providers(), nil }),
	)
	if len(cfg.TrustedOrigins) > 0 {
		opts = append(opts, authall.WithTrustedOrigins(cfg.TrustedOrigins...))
	}
	if len(cfg.TrustedProxies) > 0 {
		opts = append(opts, authall.WithTrustedProxies(cfg.TrustedProxies...))
	}
	if cfg.Inspecting {
		opts = append(opts, authall.WithoutProviderCheck())
	}
	if pool != nil {
		opts = append(opts, authall.WithEventHandler(events.HandlerFunc(func(ctx context.Context, e events.Event) {
			recordEvent(ctx, pool, e)
		})))
	}

	a, err := authall.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("auth.NewPeople: %w", err)
	}
	p.Auth = a
	a.Hooks().OnBeforeUserCreate(p.beforeUserCreate(pool))
	a.Hooks().OnBeforeSessionCreate(func(_ context.Context, ev *hook.SessionCreate) error {
		if KindOf(ev.User) == KindService {
			return apierr.ErrForbidden.WithMessage("A service account has no session.")
		}
		return nil
	})
	return p, nil
}

func (c PeopleConfig) providers() []oauth.Provider {
	var out []oauth.Provider
	if c.OIDCIssuer != "" {
		out = append(out, oidc.New(oidc.WithID(oidcProviderID), oidc.WithIssuer(c.OIDCIssuer),
			oidc.WithClientID(c.OIDCClientID), oidc.WithClientSecret(c.OIDCClientSecret)))
	}
	if c.GoogleClientID != "" {
		out = append(out, google.New(google.WithClientID(c.GoogleClientID), google.WithClientSecret(c.GoogleClientSecret)))
	}
	if c.GitHubClientID != "" {
		out = append(out, github.New(github.WithClientID(c.GitHubClientID), github.WithClientSecret(c.GitHubClientSecret)))
	}
	return out
}

// SignInMethods reports what the sign-in page may show.
func (p *People) SignInMethods() SignInMethods {
	c := p.current()
	m := SignInMethods{
		Password:       true,
		Signup:         !c.DisableSignup,
		ForgotPassword: c.EmailSender != nil,
		Providers:      []string{},
	}
	for _, provider := range p.providers() {
		m.Providers = append(m.Providers, provider.ID())
	}
	return m
}

// beforeUserCreate enforces the signup rules and assigns the role. It always
// writes a role, because auth_users refuses the empty role auth-all defaults to.
func (p *People) beforeUserCreate(pool *pgxpool.Pool) hook.BeforeUserCreateFunc {
	return func(ctx context.Context, ev *hook.UserCreate) error {
		c := p.current()
		if err := SignupAllowed(ev.Provider, ev.User.Email, c.DisableSignup, c.AllowedDomains); err != nil {
			return err
		}
		// auth-all writes NULL for a host field it holds no value for.
		ev.User.Extra = store.NewExtraFields(map[string]any{"kind": KindHuman})
		ev.User.Role = RoleMember
		var humans int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM auth_users WHERE kind = 'human'`).Scan(&humans); err != nil {
			return fmt.Errorf("auth: count users: %w", err)
		}
		if humans == 0 {
			if p.cfg.FirstPerson != nil {
				if err := p.cfg.FirstPerson(ctx); err != nil {
					return err
				}
			}
			ev.User.Role = RoleOwner
		}
		return nil
	}
}

// SignupAllowed decides whether one new account may exist. The company's
// identity provider is its own gate, so DISABLE_SIGNUP does not stop OIDC.
func SignupAllowed(provider, address string, disableSignup bool, domains []string) error {
	if disableSignup && provider != oidcProviderID {
		return apierr.ErrForbidden.WithMessage("Signup is disabled.")
	}
	if len(domains) == 0 {
		return nil
	}
	_, domain, _ := strings.Cut(email.Normalize(address), "@")
	for _, d := range domains {
		if strings.EqualFold(strings.TrimSpace(d), domain) {
			return nil
		}
	}
	return apierr.ErrForbidden.WithMessage("This email domain cannot sign up.")
}

// KindOf returns a user's kind, human or service.
func KindOf(u *store.User) string {
	kind, err := authall.Field[string](u, "kind")
	if err != nil || kind == "" {
		return KindHuman
	}
	return kind
}

// auditedEvents are the account and access changes an operator must trace.
var auditedEvents = map[events.Name]bool{
	events.UserCreated:          true,
	events.SignUp:               true,
	events.UserDisabled:         true,
	events.UserEnabled:          true,
	events.RoleChanged:          true,
	events.PasswordResetByAdmin: true,
	events.AccountLinked:        true,
	events.AccountUnlinked:      true,
	events.TOTPEnabled:          true,
	events.TOTPDisabled:         true,
}

func recordEvent(ctx context.Context, pool *pgxpool.Pool, e events.Event) {
	if !auditedEvents[e.Name] {
		log.Info().Str("event", string(e.Name)).Str("user", e.UserID).Msg("auth event")
		return
	}
	actor := e.Actor
	if actor == "" {
		actor = e.UserID
	}
	if err := audit.Record(context.WithoutCancel(ctx), pool, actor, string(e.Name), "user:"+e.UserID, nil); err != nil {
		log.Error().Err(err).Str("event", string(e.Name)).Msg("audit auth event")
	}
}

// keyPlugin registers the resolver that keeps sk- keys in Skael.
type keyPlugin struct{ keys *KeyStore }

func (*keyPlugin) ID() string { return "skael-keys" }

func (k *keyPlugin) Register(r *plugin.Registry) error {
	r.Resolver(&keyResolver{keys: k.keys, users: r.Services().Store()})
	return nil
}

type keyResolver struct {
	keys  *KeyStore
	users store.Store
}

func (*keyResolver) Claims(bearer string) bool { return strings.HasPrefix(bearer, "sk-") }

// Resolve authenticates a key. Skael's middleware reads the key's limits.
func (k *keyResolver) Resolve(ctx context.Context, bearer string) (*plugin.Principal, error) {
	if len(bearer) < keyPrefixLen {
		return nil, apierr.ErrUnauthorized
	}
	row, err := k.keys.GetByPrefix(ctx, bearer[:keyPrefixLen])
	if err != nil {
		return nil, apierr.ErrInternal.WithCause(err)
	}
	if row == nil || !CheckAPIKey(row.KeyHash, bearer) {
		return nil, apierr.ErrUnauthorized
	}
	user, err := k.users.Users().GetByID(ctx, row.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, apierr.ErrUnauthorized
		}
		return nil, apierr.ErrInternal.WithCause(err)
	}
	if user.DisabledAt != nil {
		return nil, apierr.ErrUnauthorized
	}
	return &plugin.Principal{
		User:   user,
		APIKey: &store.APIKey{ID: row.ID, UserID: row.UserID},
		Role:   user.Role,
		Method: authall.MethodAPIKey,
	}, nil
}

// PassXAPIKey lets installed hooks keep sending X-API-Key.
func PassXAPIKey(r *http.Request) {
	if r.Header.Get("Authorization") != "" {
		return
	}
	if key := r.Header.Get("X-API-Key"); key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
}
