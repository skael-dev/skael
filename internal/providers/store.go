// Package providers holds the LLM backends the server names for its workers,
// their keys, and the judge and panel every worker runs. The server calls no
// LLM: it seals each key and hands it to a worker only inside a job claim.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/settings"
)

// Provider is one backend, as the owner sees it. The key itself is never
// returned: HasKey says whether one is stored, and KeyRef shows a reference.
type Provider struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Type          string    `json:"type" enum:"anthropic,openai,openrouter,openai-compatible"`
	BaseURL       string    `json:"base_url"`
	AuthHeader    string    `json:"auth_header"`
	HasKey        bool      `json:"has_key"`
	KeyRef        string    `json:"key_ref"`
	KeyUnreadable bool      `json:"key_unreadable"`
	UpdatedBy     string    `json:"updated_by"`
	UpdatedAt     time.Time `json:"updated_at"`

	// key is the new value or reference to store; empty keeps the old one.
	key    string
	sealed string
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

var headerName = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// Validate checks one provider.
func (p *Provider) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 64 {
		return invalid("a provider needs a name of at most 64 characters")
	}
	if !slices.Contains(evalqueue.ProviderTypes, p.Type) {
		return invalid("the type must be one of %s", strings.Join(evalqueue.ProviderTypes, ", "))
	}
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if p.BaseURL != "" {
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
			return invalid("the base URL must be an http or https URL")
		}
	}
	switch p.Type {
	case evalqueue.ProviderOpenAICompatible:
		if p.BaseURL == "" {
			return invalid("an OpenAI-compatible provider needs the base URL its /chat/completions path is under, such as https://my-resource.openai.azure.com/openai/v1")
		}
	case evalqueue.ProviderAnthropic:
		if strings.HasSuffix(p.BaseURL, "/v1") {
			return invalid("leave /v1 off an Anthropic base URL; skael posts to {base}/v1/messages")
		}
	}
	p.AuthHeader = strings.TrimSpace(p.AuthHeader)
	if p.AuthHeader != "" {
		if p.Type != evalqueue.ProviderOpenAICompatible {
			return invalid("only an OpenAI-compatible provider takes a custom auth header")
		}
		if !headerName.MatchString(p.AuthHeader) {
			return invalid("the auth header must be a header name such as api-key")
		}
	}
	return nil
}

// Store reads and writes providers and the panel.
type Store struct {
	pool    *pgxpool.Pool
	keys    *platform.Keyring
	catalog *Catalog
	client  *http.Client
}

// NewStore returns a store on pool. keys seals provider keys; catalog prices
// models and lists them.
func NewStore(pool *pgxpool.Pool, keys *platform.Keyring, catalog *Catalog) *Store {
	return &Store{pool: pool, keys: keys, catalog: catalog, client: &http.Client{Timeout: 15 * time.Second}}
}

func aad(id string) string { return "provider:" + id }

// List returns every provider by name.
func (s *Store) List(ctx context.Context) ([]Provider, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, type, base_url, auth_header, credential_sealed, credential_ref, updated_by, updated_at FROM providers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("providers: list: %w", err)
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &p.BaseURL, &p.AuthHeader, &p.sealed, &p.KeyRef, &p.UpdatedBy, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("providers: list: %w", err)
		}
		p.HasKey = p.sealed != "" || p.KeyRef != ""
		if p.sealed != "" {
			if _, err := s.keys.Open(p.sealed, aad(p.ID)); err != nil {
				p.KeyUnreadable = true
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// credential returns a provider's key: opened, or read from its reference.
func (s *Store) credential(p Provider) (string, error) {
	switch {
	case p.KeyRef != "":
		return settings.ResolveReference(p.KeyRef)
	case p.sealed != "":
		b, err := s.keys.Open(p.sealed, aad(p.ID))
		if err != nil {
			return "", fmt.Errorf("its key does not open with the current master key; enter it again")
		}
		return string(b), nil
	}
	return "", errors.New("it has no key")
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

func (s *Store) upsertProvider(ctx context.Context, tx pgx.Tx, p *Provider, actor string) error {
	if err := p.Validate(); err != nil {
		return err
	}
	var err error
	if p.ID == "" {
		err = tx.QueryRow(ctx, `
			INSERT INTO providers (name, type, base_url, auth_header, updated_by) VALUES ($1, $2, $3, $4, $5)
			RETURNING id`, p.Name, p.Type, p.BaseURL, p.AuthHeader, actor).Scan(&p.ID)
	} else {
		var tag interface{ RowsAffected() int64 }
		tag, err = tx.Exec(ctx, `UPDATE providers SET name = $2, type = $3, base_url = $4, auth_header = $5,
			updated_by = $6, updated_at = now() WHERE id = $1`,
			p.ID, p.Name, p.Type, p.BaseURL, p.AuthHeader, actor)
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
	if k := strings.TrimSpace(p.key); k != "" {
		sealed, ref := "", ""
		if settings.IsReference(k) {
			ref = k
		} else if strings.HasPrefix(k, "env:") || strings.HasPrefix(k, "file:") {
			return invalid("a reference is env:NAME or file:/absolute/path")
		} else {
			if !s.keys.Enabled() {
				return invalid("no master key can seal a key; use an env: or file: reference")
			}
			if sealed, err = s.keys.Seal([]byte(k), aad(p.ID)); err != nil {
				return fmt.Errorf("providers: seal: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE providers SET credential_sealed = $2, credential_ref = $3 WHERE id = $1`, p.ID, sealed, ref); err != nil {
			return fmt.Errorf("providers: save key: %w", err)
		}
	}
	if err := audit.Record(ctx, tx, actor, "provider.save", "provider:"+p.Name,
		map[string]any{"type": p.Type, "base_url": p.BaseURL, "key_changed": p.key != ""}); err != nil {
		return err
	}
	return checkPanel(ctx, tx)
}

// Save creates or updates one provider. key is a new key or reference; empty
// keeps the stored one.
func (s *Store) Save(ctx context.Context, p *Provider, key, actor string) error {
	p.key = key
	return s.tx(ctx, func(tx pgx.Tx) error { return s.upsertProvider(ctx, tx, p, actor) })
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

// checkPanel enforces that every member names a provider.
func checkPanel(ctx context.Context, tx pgx.Tx) error {
	var members []byte
	err := tx.QueryRow(ctx, `SELECT members FROM eval_panel WHERE id = 1`).Scan(&members)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("providers: check panel: %w", err)
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

// EvalConfig resolves the panel into what a worker needs for one job, keys
// included, or nil when no panel is configured. A provider whose key is
// missing travels without one, and the worker fails the job naming it.
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
		key, _ := s.credential(p)
		b := evalqueue.EvalBackend{Provider: p.Name, Type: p.Type, BaseURL: p.BaseURL, AuthHeader: p.AuthHeader,
			Credential: key, Model: sl.Model}
		if s.catalog != nil {
			b.Cost = s.catalog.Cost(ctx, p.Type, sl.Model)
		}
		return b, nil
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

// EvalConfigFor is EvalConfig for a job that may name its own models. Each
// requested model runs on its named provider, or on the primary member's when
// it names none; the judge stays the configured one.
func (s *Store) EvalConfigFor(ctx context.Context, requested evalqueue.Panel) (*evalqueue.EvalConfig, error) {
	cfg, err := s.EvalConfig(ctx)
	if err != nil || cfg == nil || len(requested.Models) == 0 {
		return cfg, err
	}
	panel, err := s.Panel(ctx)
	if err != nil || panel == nil {
		return cfg, err
	}
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]Provider{}
	for _, p := range list {
		byID[p.ID] = p
	}
	members := make([]evalqueue.EvalBackend, 0, len(requested.Models))
	for i, model := range requested.Models {
		id := panel.Members[0].ProviderID
		if i < len(requested.Providers) && requested.Providers[i] != "" {
			id = requested.Providers[i]
		}
		p, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("providers: the job names a missing provider %s for %s", id, model)
		}
		key, _ := s.credential(p)
		b := evalqueue.EvalBackend{Provider: p.Name, Type: p.Type, BaseURL: p.BaseURL, AuthHeader: p.AuthHeader, Credential: key, Model: model}
		if s.catalog != nil {
			b.Cost = s.catalog.Cost(ctx, p.Type, model)
		}
		members = append(members, b)
	}
	cfg.Members = members
	return cfg, nil
}

// Problems lists why the configured panel cannot run, in words for the owner:
// no panel, or a provider it uses without a readable key.
func (s *Store) Problems(ctx context.Context) ([]string, error) {
	panel, err := s.Panel(ctx)
	if err != nil {
		return nil, err
	}
	if panel == nil {
		return []string{"no judge and panel are set: add a provider in Settings → Evaluation"}, nil
	}
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{panel.Judge.ProviderID: true}
	for _, m := range panel.Members {
		used[m.ProviderID] = true
	}
	var out []string
	for _, p := range list {
		if !used[p.ID] {
			continue
		}
		if _, err := s.credential(p); err != nil {
			out = append(out, fmt.Sprintf("the provider %s needs a key: %s", p.Name, err))
		}
	}
	return out, nil
}

// Models lists the models a provider offers: the catalog for a named type, the
// endpoint's own list for an openai-compatible one.
func (s *Store) Models(ctx context.Context, id string) ([]Model, string, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, p := range list {
		if p.ID != id {
			continue
		}
		if p.Type == evalqueue.ProviderOpenAICompatible {
			key, _ := s.credential(p)
			models, err := endpointModels(ctx, s.client, p.BaseURL, p.AuthHeader, key)
			if err != nil {
				return []Model{}, err.Error(), nil
			}
			return models, "", nil
		}
		if s.catalog == nil {
			return []Model{}, "", nil
		}
		models, err := s.catalog.Models(ctx, p.Type)
		if err != nil {
			return []Model{}, "the model catalog is unavailable: " + err.Error(), nil
		}
		if models == nil {
			models = []Model{}
		}
		return models, "", nil
	}
	return nil, "", invalid("no provider has that id")
}

// Unreadable implements settings.SealedSource: provider keys that no longer
// open with any master key.
func (s *Store) Unreadable(ctx context.Context) ([]settings.Unreadable, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []settings.Unreadable
	for _, p := range list {
		if p.KeyUnreadable {
			out = append(out, settings.Unreadable{Name: "Key of the provider " + p.Name, Where: "Settings → Evaluation: enter the key again"})
		}
	}
	return out, nil
}

// Discard implements settings.SealedSource: it drops keys that no longer open.
func (s *Store) Discard(ctx context.Context, actor string) error {
	list, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, p := range list {
		if !p.KeyUnreadable {
			continue
		}
		if _, err := s.pool.Exec(ctx, `UPDATE providers SET credential_sealed = '' WHERE id = $1`, p.ID); err != nil {
			return err
		}
		if err := audit.Record(ctx, s.pool, actor, "provider.key_discard", "provider:"+p.Name, nil); err != nil {
			return err
		}
	}
	return nil
}
