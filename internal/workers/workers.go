// Package workers records each eval worker's check-in and tells the owner
// whether it is online and on the common sandbox image. Provider keys live on
// the server, so a worker needs nothing per provider.
package workers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/auth"
)

// StaleAfter is how long a worker may be silent before it shows as offline.
const StaleAfter = 5 * time.Minute

// WorkersCheckin is what a worker reports on each poll.
type WorkersCheckin struct {
	WorkerID         string `json:"worker_id" minLength:"1" maxLength:"200"`
	Version          string `json:"version"`
	Driver           string `json:"driver"`
	HardwareIsolated bool   `json:"hardware_isolated"`
	NetworkPolicy    bool   `json:"network_policy"`
	Concurrency      int    `json:"concurrency"`
	SandboxImage     string `json:"sandbox_image"`
	// Harness is the agent CLI and version the sandbox image runs.
	Harness string `json:"harness,omitempty"`
}

// WorkersCheckinReply acknowledges a check-in.
type WorkersCheckinReply struct {
	OK bool `json:"ok"`
}

// Worker is one worker's last check-in and its readiness.
type Worker struct {
	WorkersCheckin
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	// Problems are why the worker cannot run the configured panel. Empty means ready.
	Problems []string `json:"problems"`
}

// Store reads and writes check-ins.
type Store struct{ pool *pgxpool.Pool }

// NewStore returns a store on pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Checkin records one report.
func (s *Store) Checkin(ctx context.Context, c WorkersCheckin) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workers (id, version, driver, hardware_isolated, network_policy, concurrency, sandbox_image, harness, pinned, credentials, verifications)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '', '[]', '[]')
		ON CONFLICT (id) DO UPDATE SET version = EXCLUDED.version, driver = EXCLUDED.driver,
			hardware_isolated = EXCLUDED.hardware_isolated, network_policy = EXCLUDED.network_policy,
			concurrency = EXCLUDED.concurrency, sandbox_image = EXCLUDED.sandbox_image, harness = EXCLUDED.harness,
			last_seen = now()`,
		c.WorkerID, c.Version, c.Driver, c.HardwareIsolated, c.NetworkPolicy, c.Concurrency, c.SandboxImage, c.Harness)
	if err != nil {
		return fmt.Errorf("workers: checkin: %w", err)
	}
	return nil
}

// List returns every worker seen in the last day, newest first.
func (s *Store) List(ctx context.Context) ([]Worker, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, version, driver, hardware_isolated, network_policy, concurrency, sandbox_image, harness,
			first_seen, last_seen
		FROM workers WHERE last_seen > now() - interval '1 day' ORDER BY last_seen DESC`)
	if err != nil {
		return nil, fmt.Errorf("workers: list: %w", err)
	}
	defer rows.Close()
	var out []Worker
	for rows.Next() {
		var w Worker
		if err := rows.Scan(&w.WorkerID, &w.Version, &w.Driver, &w.HardwareIsolated, &w.NetworkPolicy, &w.Concurrency,
			&w.SandboxImage, &w.Harness, &w.FirstSeen, &w.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Readiness fills each worker's problems: offline, or on a sandbox image the
// other workers do not run.
func Readiness(list []Worker, now time.Time) {
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
	// Env is every variable the new worker needs, filled in. Provider keys
	// stay on the server.
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
func RegisterRoutes(api huma.API, s *Store, keys *auth.KeyStore, publicURL func() string) {
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
		return &checkinOutput{Body: WorkersCheckinReply{OK: true}}, nil
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
		Readiness(list, time.Now())
		if list == nil {
			list = []Worker{}
		}
		return &listOutput{Body: WorkersListBody{Workers: list}}, nil
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
