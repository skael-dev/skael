package evalsuite

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/skill"
)

// suiteCheck mirrors Check for the wire format.
type suiteCheck struct {
	TaskID string `json:"task_id"`
	OK     bool   `json:"ok"`
	Void   bool   `json:"void,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type suiteBody struct {
	Skill       string       `json:"skill" minLength:"1"`
	SpecVersion int          `json:"spec_version"`
	Checks      []suiteCheck `json:"checks"`
	// Spec is the pusher's spec.yaml as JSON. Optional so an older client
	// can still push, but a worker materializing this suite later has no
	// other way to recover it — see evalsuite.Record.Spec.
	Spec          json.RawMessage `json:"spec,omitempty"`
	ArchiveBase64 string          `json:"archive_base64" minLength:"1"`
	// JobID and ClaimToken are the eval worker's proof that it is deriving
	// this suite for a job it currently holds the claim on. Both empty is the
	// ordinary authored push.
	JobID      string `json:"job_id,omitempty"`
	ClaimToken string `json:"claim_token,omitempty"`
}

// DeriveClaims is the eval-queue surface this route needs to attribute a push
// to the job whose worker is deriving for it. It is an interface rather than
// the concrete type because internal/evalqueue imports this package for its
// own route wiring, so the reverse import is impossible; internal/server
// bridges the two.
type DeriveClaims interface {
	// VerifyDerivePush reports whether jobID is a job for skillName that is
	// currently claimed with token and was submitted with no suite of its own.
	VerifyDerivePush(ctx context.Context, jobID, token, skillName string) (bool, error)
	// RecordDerivedSuite records ref as jobID's suite, through q so it lands in
	// the same transaction as the suite row itself.
	RecordDerivedSuite(ctx context.Context, q Queryer, jobID, ref string) error
}

// RouteOptions carries optional collaborators. A nil Claims disables push-time
// provenance entirely: every upload is recorded authored, as it was before.
type RouteOptions struct {
	Claims DeriveClaims
}

type suiteInput struct {
	Body suiteBody
}

type suiteOutputBody struct {
	Ref       string `json:"ref"`
	TaskCount int    `json:"task_count"`
}

type suiteOutput struct {
	Status int
	Body   suiteOutputBody
}

// RegisterRoutes wires up the eval suite registry HTTP endpoints onto the
// provided Huma API and Chi router. The router is needed for the raw-response
// download route, which streams bytes rather than returning JSON.
func RegisterRoutes(api huma.API, router chi.Router, reg *Registry, skills *skill.Store, opts RouteOptions) {
	huma.Register(api, huma.Operation{
		OperationID:   "upload-eval-suite",
		Method:        http.MethodPost,
		Path:          "/api/eval/suites",
		Summary:       "Upload an evaluation suite",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *suiteInput) (*suiteOutput, error) {
		sk, err := skills.GetByName(ctx, input.Body.Skill)
		if err != nil {
			return nil, fmt.Errorf("upload eval suite: lookup skill: %w", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Body.Skill))
		}

		if len(input.Body.Checks) == 0 {
			return nil, huma.Error422UnprocessableEntity("checks must not be empty")
		}

		archive, err := base64.StdEncoding.DecodeString(input.Body.ArchiveBase64)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("archive_base64 is not valid base64: " + err.Error())
		}

		checks := make([]Check, len(input.Body.Checks))
		for i, c := range input.Body.Checks {
			checks[i] = Check(c)
		}

		uploadedBy := "system"
		if u := auth.UserFromContext(ctx); u != nil {
			uploadedBy = u.Email
		}

		// A push that presents a job claim is the worker deriving a suite for
		// a skill that has none. Recording that here, rather than waiting for
		// the report, is what stops a run that never reports — a lost lease, a
		// crashed worker — from leaving a machine-generated suite recorded as
		// authored, which a later re-run's score could then use to clear a
		// scan hold. The claim is verified, never believed: a push that
		// presents one that does not check out is refused outright rather than
		// quietly recorded as authored, since the only caller that sends one
		// is a worker whose run is about to measure against it.
		var rec *Record
		if opts.Claims != nil && input.Body.JobID != "" {
			ok, err := opts.Claims.VerifyDerivePush(ctx, input.Body.JobID, input.Body.ClaimToken, input.Body.Skill)
			if err != nil {
				log.Error().Err(err).Str("job_id", input.Body.JobID).Msg("evalsuite: verify derive claim failed")
				return nil, huma.Error500InternalServerError("upload eval suite: internal error")
			}
			if !ok {
				return nil, huma.Error403Forbidden("upload eval suite: invalid claim for job " + input.Body.JobID)
			}
			jobID := input.Body.JobID
			rec, err = reg.PutDerived(ctx, input.Body.Skill, archive, checks, input.Body.SpecVersion, uploadedBy, input.Body.Spec,
				func(ctx context.Context, q Queryer, ref string) error {
					return opts.Claims.RecordDerivedSuite(ctx, q, jobID, ref)
				})
			if err != nil {
				if errors.Is(err, ErrInvalidArchive) {
					return nil, huma.Error422UnprocessableEntity("upload eval suite: " + err.Error())
				}
				log.Error().Err(err).Str("skill", input.Body.Skill).Msg("evalsuite: store derived suite failed")
				return nil, huma.Error500InternalServerError("upload eval suite: internal error")
			}
			return &suiteOutput{Status: http.StatusCreated, Body: suiteOutputBody{Ref: rec.Ref, TaskCount: rec.TaskCount}}, nil
		}

		rec, err = reg.Put(ctx, input.Body.Skill, archive, checks, input.Body.SpecVersion, uploadedBy, input.Body.Spec)
		if err != nil {
			if errors.Is(err, ErrInvalidArchive) {
				return nil, huma.Error422UnprocessableEntity("upload eval suite: " + err.Error())
			}
			log.Error().Err(err).Str("skill", input.Body.Skill).Msg("evalsuite: store suite failed")
			return nil, huma.Error500InternalServerError("upload eval suite: internal error")
		}

		return &suiteOutput{
			Status: http.StatusCreated,
			Body: suiteOutputBody{
				Ref:       rec.Ref,
				TaskCount: rec.TaskCount,
			},
		}, nil
	})

	// get-eval-suite-meta serves everything a worker needs to materialize a
	// suite besides the archive bytes themselves: the oracle-gate checks
	// (without which RunEvalWith's gate refuses to run at all) and the
	// authored spec (without which a worker rebuilding a workspace from a
	// downloaded bundle has no source for the skill's deps or purpose — see
	// evalsuite.Record.Spec). Both live on one route rather than two because
	// every caller that needs one needs the other.
	huma.Register(api, huma.Operation{
		OperationID: "get-eval-suite-meta",
		Method:      http.MethodGet,
		Path:        "/api/eval/suites/{ref}/meta",
		Summary:     "Get the oracle-gate checks and spec recorded for a suite",
	}, func(ctx context.Context, input *getSuiteMetaInput) (*getSuiteMetaOutput, error) {
		rec, err := reg.Get(ctx, input.Ref)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, huma.Error404NotFound(fmt.Sprintf("suite %q not found", input.Ref))
			}
			log.Error().Err(err).Str("ref", input.Ref).Msg("evalsuite: lookup suite failed")
			return nil, huma.Error500InternalServerError("get eval suite meta: internal error")
		}
		checks := make([]suiteCheck, len(rec.Checks))
		for i, c := range rec.Checks {
			checks[i] = suiteCheck(c)
		}
		return &getSuiteMetaOutput{Body: getSuiteMetaBody{
			Checks:      checks,
			SpecVersion: rec.SpecVersion,
			Spec:        rec.Spec,
			Origin:      string(rec.Origin),
		}}, nil
	})

	if router != nil {
		router.Get("/api/eval/suites/{ref}", makeDownloadHandler(reg))
	}
}

type getSuiteMetaInput struct {
	Ref string `path:"ref"`
}

type getSuiteMetaBody struct {
	Checks      []suiteCheck    `json:"checks"`
	SpecVersion int             `json:"spec_version"`
	Spec        json.RawMessage `json:"spec,omitempty"`
	// Origin is how this suite came to exist ("authored" or "derived"), so a
	// caller can decide void-tolerance from the suite itself rather than from
	// whether this particular run was the one that derived it — see
	// worker.RunInput.AllowVoid.
	Origin string `json:"origin"`
}

type getSuiteMetaOutput struct {
	Body getSuiteMetaBody
}

// makeDownloadHandler returns a handler that streams the archive for a suite
// ref. It mirrors internal/skill/routes.go's makeDownloadHandler: a lookup
// failure that isn't "no such ref" is a 500 (something is wrong on this
// side), a genuine unknown ref is a 404, and a storage read failure is a 404
// only when the object itself is missing — any other storage error is a 500.
func makeDownloadHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref := chi.URLParam(r, "ref")

		rec, err := reg.Get(r.Context(), ref)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			log.Error().Err(err).Str("ref", ref).Msg("evalsuite: lookup suite failed")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		rc, err := reg.st.Read(r.Context(), rec.ArchivePath)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			log.Error().Err(err).Str("ref", ref).Msg("evalsuite: read suite archive failed")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer rc.Close()

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, ref))
		w.WriteHeader(http.StatusOK)
		io.Copy(w, rc) //nolint:errcheck
	}
}
