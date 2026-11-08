// Package providers holds the LLM backends the server names for its workers,
// and the judge and panel every worker runs. No credential is stored here.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/evalqueue"
)

// Provider is one backend.
type Provider struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Adapter       string    `json:"adapter" enum:"anthropic,gateway,subscription"`
	BaseURL       string    `json:"base_url"`
	AuthStyle     string    `json:"auth_style" enum:"x-api-key,bearer"`
	CredentialEnv string    `json:"credential_env"`
	UpdatedBy     string    `json:"updated_by"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Slot is one provider and model.
type Slot struct {
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
}

// Panel is the judge and the members, primary first.
type Panel struct {
	Judge     Slot      `json:"judge"`
	Members   []Slot    `json:"members"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// ErrInvalid is a change the rules refuse. Its message is for the user.
type ErrInvalid struct{ Msg string }

func (e *ErrInvalid) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ErrInvalid{Msg: fmt.Sprintf(format, a...)} }

var envName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// Validate checks one provider.
func (p *Provider) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 64 {
		return invalid("a provider needs a name of at most 64 characters")
	}
	if !envName.MatchString(p.CredentialEnv) {
		return invalid("the credential variable must be an env var name such as OPENROUTER_API_KEY")
	}
	if p.AuthStyle == "" {
		p.AuthStyle = "x-api-key"
	}
	switch p.Adapter {
	case evalqueue.AdapterGateway:
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
			return invalid("a gateway needs a base URL such as https://openrouter.ai/api")
		}
		if strings.HasSuffix(strings.TrimSuffix(u.Path, "/"), "/v1") {
			return invalid("leave /v1 off the base URL; skael posts to {base}/v1/messages")
		}
		p.BaseURL = strings.TrimSuffix(p.BaseURL, "/")
	case evalqueue.AdapterAnthropic, evalqueue.AdapterSubscription:
		p.BaseURL, p.AuthStyle = "", "x-api-key"
	default:
		return invalid("the adapter must be anthropic, gateway or subscription")
	}
	return nil
}

// Store reads and writes providers and the panel.
type Store struct{ pool *pgxpool.Pool }

// NewStore returns a store on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// List returns every provider by name.
func (s *Store) List(ctx context.Context) ([]Provider, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, adapter, base_url, auth_style, credential_env, updated_by, updated_at FROM providers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("providers: list: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Provider])
	if err != nil {
		return nil, fmt.Errorf("providers: list: %w", err)
	}
	return out, nil
}

// Panel returns the configured panel, or nil.
func (s *Store) Panel(ctx context.Context) (*Panel, error) {
	var p Panel
	var members []byte
	err := s.pool.QueryRow(ctx, `SELECT judge_provider, judge_model, members, updated_by, updated_at FROM eval_panel WHERE id = 1`).
		Scan(&p.Judge.ProviderID, &p.Judge.Model, &members, &p.UpdatedBy, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("providers: panel: %w", err)
	}
	if err := json.Unmarshal(members, &p.Members); err != nil {
		return nil, fmt.Errorf("providers: panel members: %w", err)
	}
	return &p, nil
}

func upsertProvider(ctx context.Context, tx pgx.Tx, p *Provider, actor string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	var err error
	if p.ID == "" {
		err = tx.QueryRow(ctx, `
			INSERT INTO providers (name, adapter, base_url, auth_style, credential_env, updated_by) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (name) DO UPDATE SET adapter = EXCLUDED.adapter, base_url = EXCLUDED.base_url,
				auth_style = EXCLUDED.auth_style, credential_env = EXCLUDED.credential_env,
				updated_by = EXCLUDED.updated_by, updated_at = now()
			RETURNING id`, p.Name, p.Adapter, p.BaseURL, p.AuthStyle, p.CredentialEnv, actor).Scan(&p.ID)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = tx.Exec(ctx, `UPDATE providers SET name = $2, adapter = $3, base_url = $4, auth_style = $5,
			credential_env = $6, updated_by = $7, updated_at = now() WHERE id = $1`,
			p.ID, p.Name, p.Adapter, p.BaseURL, p.AuthStyle, p.CredentialEnv, actor)
		if err == nil && tag.RowsAffected() == 0 {
			return invalid("no provider has that id")
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "providers_name_key") {
			return invalid("a provider named %q exists", p.Name)
		}
		return fmt.Errorf("providers: save: %w", err)
	}
	if err := audit.Record(ctx, tx, actor, "provider.save", "provider:"+p.Name,
		map[string]any{"adapter": p.Adapter, "base_url": p.BaseURL, "credential_env": p.CredentialEnv}); err != nil {
		return err
	}
	if err := checkPanel(ctx, tx); err != nil {
		return err
	}
	return nil
}

// Save creates or updates one provider.
func (s *Store) Save(ctx context.Context, p *Provider, actor string) error {
	return s.tx(ctx, func(tx pgx.Tx) error { return upsertProvider(ctx, tx, p, actor) })
}

// Delete removes a provider the panel does not use.
func (s *Store) Delete(ctx context.Context, id, actor string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		var used bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM eval_panel WHERE judge_provider = $1 OR members @> jsonb_build_array(jsonb_build_object('provider_id', $1::text)))`, id).Scan(&used); err != nil {
			return fmt.Errorf("providers: delete: %w", err)
		}
		if used {
			return invalid("the judge or a panel member uses this provider; change the panel first")
		}
		var name string
		err := tx.QueryRow(ctx, `DELETE FROM providers WHERE id = $1 RETURNING name`, id).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return invalid("no provider has that id")
		}
		if err != nil {
			return fmt.Errorf("providers: delete: %w", err)
		}
		return audit.Record(ctx, tx, actor, "provider.delete", "provider:"+name, nil)
	})
}

// SetPanel replaces the judge and the members.
func (s *Store) SetPanel(ctx context.Context, p Panel, actor string) error {
	return s.tx(ctx, func(tx pgx.Tx) error { return setPanel(ctx, tx, p, actor) })
}

func setPanel(ctx context.Context, tx pgx.Tx, p Panel, actor string) error {
	if len(p.Members) == 0 || len(p.Members) > 4 {
		return invalid("the panel needs one to four members")
	}
	seen := map[string]bool{}
	for _, m := range append([]Slot{p.Judge}, p.Members...) {
		if strings.TrimSpace(m.Model) == "" {
			return invalid("every slot needs a model")
		}
	}
	for _, m := range p.Members {
		if seen[m.Model] {
			return invalid("the model %s appears twice in the panel", m.Model)
		}
		seen[m.Model] = true
	}
	members, err := json.Marshal(p.Members)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO eval_panel (id, judge_provider, judge_model, members, updated_by) VALUES (1, $1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET judge_provider = EXCLUDED.judge_provider, judge_model = EXCLUDED.judge_model,
			members = EXCLUDED.members, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		p.Judge.ProviderID, p.Judge.Model, members, actor)
	if err != nil {
		if strings.Contains(err.Error(), "violates foreign key") || strings.Contains(err.Error(), "invalid input syntax for type uuid") {
			return invalid("the judge names no provider")
		}
		return fmt.Errorf("providers: set panel: %w", err)
	}
	if err := checkPanel(ctx, tx); err != nil {
		return err
	}
	return audit.Record(ctx, tx, actor, "eval_panel.update", "eval_panel", p)
}

// checkPanel enforces the rules that span providers and the panel: every
// member names a provider, and a subscription never judges.
func checkPanel(ctx context.Context, tx pgx.Tx) error {
	var judgeAdapter string
	var members []byte
	err := tx.QueryRow(ctx, `SELECT p.adapter, e.members FROM eval_panel e JOIN providers p ON p.id = e.judge_provider WHERE e.id = 1`).Scan(&judgeAdapter, &members)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("providers: check panel: %w", err)
	}
	if judgeAdapter == evalqueue.AdapterSubscription {
		return invalid("a Claude subscription cannot judge: a score that releases a version must come from a metered API pinned to a model")
	}
	var slots []Slot
	if err := json.Unmarshal(members, &slots); err != nil {
		return err
	}
	for _, m := range slots {
		var ok bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM providers WHERE id::text = $1)`, m.ProviderID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return invalid("a panel member names no provider")
		}
	}
	return nil
}

func (s *Store) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// EvalConfig resolves the panel into what a worker needs, or nil when no panel
// is configured.
func (s *Store) EvalConfig(ctx context.Context) (*evalqueue.EvalConfig, error) {
	panel, err := s.Panel(ctx)
	if err != nil || panel == nil {
		return nil, err
	}
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]Provider{}
	for _, p := range list {
		byID[p.ID] = p
	}
	backend := func(sl Slot) (evalqueue.EvalBackend, error) {
		p, ok := byID[sl.ProviderID]
		if !ok {
			return evalqueue.EvalBackend{}, fmt.Errorf("providers: the panel names a missing provider %s", sl.ProviderID)
		}
		return evalqueue.EvalBackend{Provider: p.Name, Adapter: p.Adapter, BaseURL: p.BaseURL,
			AuthStyle: p.AuthStyle, CredentialEnv: p.CredentialEnv, Model: sl.Model}, nil
	}
	cfg := &evalqueue.EvalConfig{}
	if cfg.Judge, err = backend(panel.Judge); err != nil {
		return nil, err
	}
	for _, m := range panel.Members {
		b, err := backend(m)
		if err != nil {
			return nil, err
		}
		cfg.Members = append(cfg.Members, b)
	}
	return cfg, nil
}

// Backends returns every provider as a worker sees it, with no model.
func (s *Store) Backends(ctx context.Context) ([]evalqueue.EvalBackend, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]evalqueue.EvalBackend, 0, len(list))
	for _, p := range list {
		out = append(out, evalqueue.EvalBackend{Provider: p.Name, Adapter: p.Adapter, BaseURL: p.BaseURL,
			AuthStyle: p.AuthStyle, CredentialEnv: p.CredentialEnv})
	}
	return out, nil
}
