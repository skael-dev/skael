// Package workers records each eval worker's check-in and tells the owner
// whether it can run the configured judge and panel.
package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/evalqueue"
)

// StaleAfter is how long a worker may be silent before it shows as offline.
const StaleAfter = 5 * time.Minute

// Verification is one provider check a worker ran with its own credential.
type Verification struct {
	Provider string   `json:"provider"`
	OK       bool     `json:"ok"`
	Detail   string   `json:"detail"`
	Models   []string `json:"models"`
}

// WorkersCheckin is what a worker reports on each poll.
type WorkersCheckin struct {
	WorkerID         string         `json:"worker_id" minLength:"1" maxLength:"200"`
	Version          string         `json:"version"`
	Driver           string         `json:"driver"`
	HardwareIsolated bool           `json:"hardware_isolated"`
	NetworkPolicy    bool           `json:"network_policy"`
	Concurrency      int            `json:"concurrency"`
	SandboxImage     string         `json:"sandbox_image"`
	Pinned           string         `json:"pinned,omitempty"`
	Credentials      []string       `json:"credentials" maxItems:"64"`
	Verifications    []Verification `json:"verifications" maxItems:"32"`
}

// WorkersCheckinReply names the providers a worker must hold credentials for.
type WorkersCheckinReply struct {
	Providers []evalqueue.EvalBackend `json:"providers"`
}

// Worker is one worker's last check-in and its readiness.
type Worker struct {
	WorkersCheckin
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// Problems are why the worker cannot run the configured panel. Empty means ready.
	Problems []string `json:"problems"`
}

// ProviderSource lists the server's providers and the configured panel.
type ProviderSource interface {
	EvalConfig(ctx context.Context) (*evalqueue.EvalConfig, error)
	Backends(ctx context.Context) ([]evalqueue.EvalBackend, error)
}

// Store reads and writes check-ins.
type Store struct{ pool *pgxpool.Pool }

// NewStore returns a store on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Checkin records one report.
func (s *Store) Checkin(ctx context.Context, c WorkersCheckin) error {
	creds, err := json.Marshal(nonNil(c.Credentials))
	if err != nil {
		return err
	}
	ver, err := json.Marshal(c.Verifications)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO workers (id, version, driver, hardware_isolated, network_policy, concurrency, sandbox_image, pinned, credentials, verifications)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET version = EXCLUDED.version, driver = EXCLUDED.driver,
			hardware_isolated = EXCLUDED.hardware_isolated, network_policy = EXCLUDED.network_policy,
			concurrency = EXCLUDED.concurrency, sandbox_image = EXCLUDED.sandbox_image, pinned = EXCLUDED.pinned,
			credentials = EXCLUDED.credentials, verifications = EXCLUDED.verifications, last_seen = now()`,
		c.WorkerID, c.Version, c.Driver, c.HardwareIsolated, c.NetworkPolicy, c.Concurrency, c.SandboxImage, c.Pinned, creds, ver)
	if err != nil {
		return fmt.Errorf("workers: checkin: %w", err)
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// List returns every worker seen in the last day, newest first.
func (s *Store) List(ctx context.Context) ([]Worker, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, version, driver, hardware_isolated, network_policy, concurrency, sandbox_image, pinned,
			credentials, verifications, first_seen, last_seen
		FROM workers WHERE last_seen > now() - interval '1 day' ORDER BY last_seen DESC`)
	if err != nil {
		return nil, fmt.Errorf("workers: list: %w", err)
	}
	defer rows.Close()
	var out []Worker
	for rows.Next() {
		var w Worker
		var creds, ver []byte
		if err := rows.Scan(&w.WorkerID, &w.Version, &w.Driver, &w.HardwareIsolated, &w.NetworkPolicy, &w.Concurrency,
			&w.SandboxImage, &w.Pinned, &creds, &ver, &w.FirstSeen, &w.LastSeen); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(creds, &w.Credentials); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(ver, &w.Verifications); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Readiness fills each worker's problems against the configured panel and the
// other workers' sandbox image.
func Readiness(list []Worker, cfg *evalqueue.EvalConfig, now time.Time) {
	images := map[string]int{}
	for _, w := range list {
		if now.Sub(w.LastSeen) < StaleAfter {
			images[w.SandboxImage]++
		}
	}
	common, best := "", 0
	for img, n := range images {
		if n > best || n == best && img < common {
			common, best = img, n
		}
	}
	for i := range list {
		w := &list[i]
		w.Problems = []string{}
		if now.Sub(w.LastSeen) >= StaleAfter {
			w.Problems = append(w.Problems, "offline: last seen "+now.Sub(w.LastSeen).Round(time.Minute).String()+" ago")
			continue
		}
		if len(images) > 1 && w.SandboxImage != common {
			w.Problems = append(w.Problems, fmt.Sprintf("its sandbox image %s differs from %s on the other workers, so its scores are not comparable", w.SandboxImage, common))
		}
		if w.Pinned != "" {
			continue
		}
		if cfg == nil {
			if !slices.Contains(w.Credentials, "ANTHROPIC_API_KEY") {
				w.Problems = append(w.Problems, "no provider is set up and the worker has no ANTHROPIC_API_KEY")
			}
			continue
		}
		seen := map[string]bool{}
		for _, b := range append([]evalqueue.EvalBackend{cfg.Judge}, cfg.Members...) {
			if seen[b.Provider] {
				continue
			}
			seen[b.Provider] = true
			if !slices.Contains(w.Credentials, b.CredentialEnv) {
				w.Problems = append(w.Problems, fmt.Sprintf("missing %s for the provider %s", b.CredentialEnv, b.Provider))
				continue
			}
			for _, v := range w.Verifications {
				if v.Provider == b.Provider && !v.OK {
					w.Problems = append(w.Problems, fmt.Sprintf("%s: %s", b.Provider, v.Detail))
				}
			}
		}
	}
}

type checkinInput struct {
	Body WorkersCheckin
}

type checkinOutput struct {
	Body WorkersCheckinReply
}

type WorkersListBody struct {
	Workers []Worker `json:"workers"`
	// Models is every model id any worker listed, per provider, for the
	// panel's dropdowns.
	Models map[string][]string `json:"models"`
}

type listOutput struct {
	Body WorkersListBody
}

type WorkersKeyBody struct {
	Name string `json:"name" minLength:"1" maxLength:"64"`
}

type keyInput struct {
	Body WorkersKeyBody
}

type WorkersKeyReply struct {
	Key string `json:"key"`
	// Env is every variable the new worker needs, with the key and the
	// endpoint filled in and each provider credential left for the owner.
	Env []WorkersEnvVar `json:"env"`
}

type WorkersEnvVar struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type keyOutput struct {
	Body WorkersKeyReply
}

// RegisterRoutes serves the check-in to workers and the list to owners.
func RegisterRoutes(api huma.API, s *Store, providers ProviderSource, keys *auth.KeyStore, publicURL func() string) {
	huma.Register(api, huma.Operation{
		OperationID: "worker-checkin", Method: http.MethodPost, Path: "/api/eval/workers/checkin",
		Summary: "Report a worker's state and learn the providers it needs",
	}, func(ctx context.Context, in *checkinInput) (*checkinOutput, error) {
		u := auth.UserFromContext(ctx)
		if !u.IsPrivileged() && !auth.HasScope(ctx, auth.ScopeEvalWork) {
			return nil, huma.Error403Forbidden("worker check-in: a key with the eval:work scope is required")
		}
		if err := s.Checkin(ctx, in.Body); err != nil {
			return nil, huma.Error500InternalServerError("worker check-in: internal error", err)
		}
		backends, err := providers.Backends(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("worker check-in: internal error", err)
		}
		return &checkinOutput{Body: WorkersCheckinReply{Providers: backends}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-workers", Method: http.MethodGet, Path: "/api/admin/workers",
		Summary: "List the workers seen in the last day and what each one lacks",
	}, func(ctx context.Context, _ *struct{}) (*listOutput, error) {
		if err := requirePrivileged(ctx); err != nil {
			return nil, err
		}
		list, err := s.List(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("workers: internal error", err)
		}
		cfg, err := providers.EvalConfig(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("workers: internal error", err)
		}
		Readiness(list, cfg, time.Now())
		models := map[string][]string{}
		for _, w := range list {
			for _, v := range w.Verifications {
				for _, m := range v.Models {
					if !slices.Contains(models[v.Provider], m) {
						models[v.Provider] = append(models[v.Provider], m)
					}
				}
			}
		}
		for k := range models {
			slices.Sort(models[k])
		}
		if list == nil {
			list = []Worker{}
		}
		return &listOutput{Body: WorkersListBody{Workers: list, Models: models}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-worker-key", Method: http.MethodPost, Path: "/api/admin/workers/key",
		Summary: "Create a key that can only run eval jobs, and the env a new worker needs",
	}, func(ctx context.Context, in *keyInput) (*keyOutput, error) {
		u := auth.UserFromContext(ctx)
		if err := requirePrivileged(ctx); err != nil {
			return nil, err
		}
		pool := s.pool
		key, err := auth.CreateWorkerKey(ctx, pool, keys, strings.TrimSpace(in.Body.Name), u.Email)
		if err != nil {
			return nil, huma.Error500InternalServerError("create worker key: internal error", err)
		}
		if err := audit.Record(ctx, pool, u.Email, "worker_key.create", "service_account:"+auth.WorkersAccount, map[string]any{"name": in.Body.Name}); err != nil {
			return nil, huma.Error500InternalServerError("create worker key: internal error", err)
		}
		endpoint := strings.TrimSuffix(publicURL(), "/")
		env := []WorkersEnvVar{{Name: "SKAEL_ENDPOINT", Value: endpoint}, {Name: "SKAEL_API_KEY", Value: key, Secret: true}}
		backends, err := providers.Backends(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("create worker key: internal error", err)
		}
		seen := map[string]bool{}
		for _, b := range backends {
			if !seen[b.CredentialEnv] {
				seen[b.CredentialEnv] = true
				env = append(env, WorkersEnvVar{Name: b.CredentialEnv, Secret: true})
			}
		}
		if len(backends) == 0 {
			env = append(env, WorkersEnvVar{Name: "ANTHROPIC_API_KEY", Secret: true})
		}
		return &keyOutput{Body: WorkersKeyReply{Key: key, Env: env}}, nil
	})
}

func requirePrivileged(ctx context.Context) error {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return huma.Error401Unauthorized("not authenticated")
	}
	if !u.IsPrivileged() {
		return huma.Error403Forbidden("owner or admin role required")
	}
	return nil
}
