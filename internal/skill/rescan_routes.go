package skill

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/auth"
)

// JobInserter enqueues a background job.
type JobInserter interface {
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// RescanArgs asks for one version's rescan. Args hold ids only, because River
// UI shows them to admins.
type RescanArgs struct {
	VersionID     string `json:"version_id" river:"unique"`
	RequestedByID string `json:"requested_by_id"`
}

func (RescanArgs) Kind() string { return "skill_rescan" }

// InsertOpts collapses a second "rescan all" into the jobs still waiting.
func (RescanArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{rivertype.JobStateAvailable, rivertype.JobStatePending,
				rivertype.JobStateRunning, rivertype.JobStateScheduled, rivertype.JobStateRetryable},
		},
	}
}

// RescanWorker rescans one version. Bind must run before River starts it.
type RescanWorker struct {
	river.WorkerDefaults[RescanArgs]
	publisher *Publisher
}

// Bind gives the worker the publisher whose scan steps it runs.
func (w *RescanWorker) Bind(p *Publisher) { w.publisher = p }

// Work rescans a version that is still released. A version that went away or
// changed state since the job was queued is not an error.
func (w *RescanWorker) Work(ctx context.Context, job *river.Job[RescanArgs]) error {
	if w.publisher == nil {
		return errors.New("skill rescan: the worker has no publisher")
	}
	name, version, state, found, err := w.publisher.store.VersionRef(ctx, job.Args.VersionID)
	if err != nil {
		return err
	}
	if !found || state != "released" {
		return nil
	}
	_, err = w.publisher.Rescan(ctx, name, version, RescanActor, RescanAudit{RequestedByID: job.Args.RequestedByID})
	return err
}

// Timeout bounds one rescan, which can call the external scanner.
func (w *RescanWorker) Timeout(*river.Job[RescanArgs]) time.Duration { return 5 * time.Minute }

type scanInput struct {
	Name string `path:"name"`
}

type scanOutput struct {
	Body *SkillScan
}

type rescanInput struct {
	Name    string `path:"name"`
	Version int    `path:"version"`
}

// SkillRescanAllBody answers a request to rescan every released version.
type SkillRescanAllBody struct {
	Queued int `json:"queued"`
}

// registerScanRoutes wires the stored-report read, the rescan of one version,
// and the admin rescan of every released version.
func registerScanRoutes(api huma.API, store *Store, publisher *Publisher, opts RouteOptions) {
	huma.Register(api, huma.Operation{
		OperationID: "get-skill-scan",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/scan",
		Summary:     "The stored scan report of the newest version, held or released",
	}, func(ctx context.Context, input *scanInput) (*scanOutput, error) {
		sk, err := store.GetByName(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("scan: lookup skill: %w", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
		}
		// A machine key without eval:work reads only what it could install.
		key := auth.KeyFromContext(ctx)
		releasedOnly := key.Restricted() && !auth.HasScope(ctx, auth.ScopeEvalWork)
		view, err := store.ScanView(ctx, input.Name, 0, releasedOnly)
		if errors.Is(err, errNoReport) {
			return nil, huma.Error404NotFound(fmt.Sprintf("%s v%d has no scan report; rescan it to store one", input.Name, view.Version))
		}
		if err != nil {
			return nil, err
		}
		if view == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("%s has no version", input.Name))
		}
		return &scanOutput{Body: view}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "rescan-skill-version",
		Method:      http.MethodPost,
		Path:        "/api/skills/{name}/versions/{version}/scan",
		Summary:     "Scan a stored version again and store the new report. Owner of the skill or admin only.",
	}, func(ctx context.Context, input *rescanInput) (*scanOutput, error) {
		user := auth.UserFromContext(ctx)
		allowed := user.IsPrivileged()
		if !allowed && opts.Ownership != nil {
			st, err := opts.Ownership.ResolveForPublish(ctx, input.Name, user)
			if err != nil {
				return nil, fmt.Errorf("rescan: resolve ownership: %w", err)
			}
			allowed = st.IsOwner
		}
		if !allowed {
			return nil, huma.Error403Forbidden("only an owner of this skill, or an instance admin, may rescan it")
		}
		view, err := publisher.Rescan(ctx, input.Name, input.Version, user.Email, RescanAudit{})
		if err != nil {
			return nil, err
		}
		return &scanOutput{Body: view}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "rescan-all-released",
		Method:        http.MethodPost,
		Path:          "/api/admin/rescan",
		Summary:       "Queue a rescan of every released version. Owner or admin only.",
		DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body SkillRescanAllBody }, error) {
		user := auth.UserFromContext(ctx)
		if !user.IsPrivileged() {
			return nil, huma.Error403Forbidden("owner or admin role required")
		}
		if opts.Jobs == nil {
			return nil, huma.Error503ServiceUnavailable("the job queue is not running")
		}
		ids, err := store.ReleasedVersionIDs(ctx)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, err := opts.Jobs.Insert(ctx, RescanArgs{VersionID: id, RequestedByID: user.ID}, nil); err != nil {
				return nil, fmt.Errorf("rescan all: enqueue: %w", err)
			}
		}
		if err := audit.Record(ctx, store.Pool(), user.Email, "skill.rescan_all", "skills", SkillRescanAllBody{Queued: len(ids)}); err != nil {
			return nil, err
		}
		log.Info().Str("user", user.Email).Int("versions", len(ids)).Msg("queued a rescan of every released version")
		return &struct{ Body SkillRescanAllBody }{SkillRescanAllBody{Queued: len(ids)}}, nil
	})
}
