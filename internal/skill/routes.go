package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/scan"
)

// NewChiAPI creates a new Chi router and Huma API suitable for production and
// tests. Returns both so callers can mount additional middleware on the router
// or serve it directly.
func NewChiAPI() (chi.Router, huma.API) {
	r := chi.NewMux()
	api := humachi.New(r, huma.DefaultConfig("Skael API", "1.0.0"))
	return r, api
}

// validSkillName matches lowercase alphanumeric names that may contain internal
// hyphens, but must start and end with a lowercase letter or digit (no trailing
// or leading hyphens).
var validSkillName = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)

// validRegisterName is the relaxed check for /api/skills/register: that
// endpoint deliberately accepts arbitrary display-style names from agent
// hooks, but never path fragments or control characters.
func validRegisterName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	if strings.Contains(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// publishOverrideAllowed reports whether the caller may publish a version whose
// scan came back blocking. Only an authenticated privileged user can, and only
// when they asked for it explicitly. Without this escape hatch a skill that
// trips a heuristic is unpublishable by any route, which is worse than a
// recorded, deliberate override.
func publishOverrideAllowed(ctx context.Context, requested bool) bool {
	if !requested {
		return false
	}
	return auth.UserFromContext(ctx).IsPrivileged()
}

// DecidePublish is the one definition of "what does this scan report mean for
// this version" shared by every route that creates a version — publish and
// import alike. A version has no quality state yet by definition: the
// evaluation that could produce one runs against the bundle this call is
// creating, so q is always nil here. o carries the resolved ownership state;
// pass a zero OwnerState to skip the ownership dimension entirely (the CLI's
// local pre-scan does exactly that). floor and override are the resolved
// gate.Policy inputs; import has no interactive override and must always
// pass false.
func DecidePublish(rep *scan.Report, o gate.OwnerState, floor float64, override bool) gate.Decision {
	return gate.Decide(*rep, nil, o, gate.Policy{Floor: floor, AdminOverride: override})
}

// EvalJobRequest is what publish needs to enqueue an evaluation. It mirrors
// the fields of evalqueue.Job that publish sets, but is declared in this
// package rather than imported: internal/evalqueue imports internal/skill
// (for route wiring), so importing evalqueue back here would cycle. Callers
// adapt a real evalqueue.Executor to QueueSubmitter (see internal/server).
type EvalJobRequest struct {
	SkillID     string
	SkillName   string
	Version     int
	SuiteRef    string
	Tier        string
	RequestedBy string
}

// QueueSubmitter is the subset of evalqueue.Executor that publish needs.
type QueueSubmitter interface {
	Submit(ctx context.Context, job EvalJobRequest) (string, error)
}

// SuiteRecord is the subset of evalsuite.Record that publish needs to build
// an EvalJobRequest.
type SuiteRecord struct {
	Ref string
}

// SuiteLookup is the subset of evalsuite.Registry that publish needs. It
// mirrors LatestForSkill's (nil, nil) contract: no error, nil record means no
// suite is registered for the skill.
type SuiteLookup interface {
	LatestForSkill(ctx context.Context, skillName string) (*SuiteRecord, error)
}

// OwnerResolver is the subset of the ownership store publish needs. It is
// declared here rather than imported because internal/ownership must stay
// free of internal/skill and the concrete wiring lives in internal/server —
// the same shape as QueueSubmitter and SuiteLookup.
//
// ResolveForPublish folds instance privilege into IsOwner: an instance owner
// or admin is an implicit owner of everything, which is what makes O5's
// "admins are the reviewers of record" fall out without a special case.
type OwnerResolver interface {
	ResolveForPublish(ctx context.Context, skillName string, user *auth.User) (gate.OwnerState, error)
	ClaimOnFirstPublish(ctx context.Context, skillName string, user *auth.User) error
}

// RouteOptions carries the optional collaborators RegisterRoutes wires into
// the publish (and related) handlers. Each is independently optional (nil
// disables the behavior it powers) so a caller — including tests — can opt
// into only what it needs.
type RouteOptions struct {
	// External is the opt-in external scanner (Phase 2); nil disables it.
	External *scan.ExternalScanner
	// Queue, when set together with Suites, lets publish enqueue an
	// evaluation for a skill that has a registered suite. A queue outage
	// must never fail a publish — see the enqueue step below.
	Queue QueueSubmitter
	// Suites looks up the latest registered eval suite for a skill by name.
	Suites SuiteLookup
	// QualityFloor is the minimum headline quality score a verified
	// evaluation must reach to clear a held version. It comes from
	// platform.Config.QualityFloor; the zero value means any verified,
	// complete, contract-clean report clears.
	QualityFloor float64
	// Ownership resolves and claims skill ownership for publish. A nil
	// resolver leaves the zero gate.OwnerState, which contributes nothing —
	// that is what keeps every test harness and the CLI's local pre-scan on
	// the pre-ownership behaviour.
	Ownership OwnerResolver
	// Git routes a change to a git-bound skill into a pull request. Nil means
	// every skill is native.
	Git GitBoundary
}

// OutboundChange is the pull request Skael opened for a change to a git-bound
// skill. The skill gets a version when it merges.
type OutboundChange struct {
	ID       string `json:"id"`
	PRNumber int    `json:"pr_number"`
	PRURL    string `json:"pr_url"`
	State    string `json:"state"`
}

// GitBoundary knows which skills git owns and opens their changes as pull
// requests. An interface because git sync imports this package.
type GitBoundary interface {
	// Bound reports whether git owns the skill, and names its repo path.
	Bound(ctx context.Context, name string) (bool, string, error)
	// OpenChange commits archive to a new branch and opens a pull request,
	// on behalf of the user in ctx.
	OpenChange(ctx context.Context, name string, archive []byte, note string) (*OutboundChange, error)
}

// ErrGitBound answers a write that git owns.
func ErrGitBound(name, where string) error {
	return huma.Error409Conflict(fmt.Sprintf("%s is git-bound to %s; change it in the repo", name, where))
}

// RegisterRoutes wires up all skill-related HTTP endpoints onto the provided
// Huma API and Chi router. The router is needed for the two raw-response
// routes (download + scan) that stream bytes rather than returning JSON.
func RegisterRoutes(api huma.API, router chi.Router, store *Store, storage platform.Storage, opts RouteOptions) {
	// -----------------------------------------------------------------
	// POST /api/skills — create a skill
	// -----------------------------------------------------------------
	type createBody struct {
		Name        string `json:"name" minLength:"1" maxLength:"128"`
		Description string `json:"description,omitempty"`
	}
	type createInput struct {
		Body createBody
	}
	type createOutput struct {
		Body *Skill
	}
	huma.Register(api, huma.Operation{
		OperationID:   "create-skill",
		Method:        http.MethodPost,
		Path:          "/api/skills",
		Summary:       "Create a skill",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *createInput) (*createOutput, error) {
		if !validSkillName.MatchString(input.Body.Name) {
			return nil, huma.Error422UnprocessableEntity("skill name must be lowercase alphanumeric with hyphens")
		}
		sk, err := store.Create(ctx,
			input.Body.Name,
			"", // display_name is empty at creation time
			input.Body.Description,
			"",
			json.RawMessage(`{}`),
		)
		if err != nil {
			if platform.IsDuplicateKey(err) {
				return nil, huma.Error409Conflict(
					fmt.Sprintf("skill %q already exists", input.Body.Name))
			}
			return nil, fmt.Errorf("create skill: %w", err)
		}
		return &createOutput{Body: sk}, nil
	})

	// -----------------------------------------------------------------
	// POST /api/skills/register — register a skill stub (no name validation)
	// -----------------------------------------------------------------
	type registerBody struct {
		Name string `json:"name" minLength:"1" maxLength:"255"`
	}
	type registerInput struct {
		Body registerBody
	}
	type registerOutput struct {
		Body *Skill
	}
	huma.Register(api, huma.Operation{
		OperationID:   "register-skill",
		Method:        http.MethodPost,
		Path:          "/api/skills/register",
		Summary:       "Register a skill stub (no name format validation)",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *registerInput) (*registerOutput, error) {
		if !validRegisterName(input.Body.Name) {
			return nil, huma.Error422UnprocessableEntity(
				"skill name must not contain path separators, '..', or control characters")
		}
		sk, err := store.Create(ctx, input.Body.Name, "", "", "", json.RawMessage(`{}`))
		if err != nil {
			if platform.IsDuplicateKey(err) {
				return nil, huma.Error409Conflict(
					fmt.Sprintf("skill %q already exists", input.Body.Name))
			}
			return nil, fmt.Errorf("register skill: %w", err)
		}
		return &registerOutput{Body: sk}, nil
	})

	// -----------------------------------------------------------------
	// GET /api/skills/{name} — get a skill by name
	// -----------------------------------------------------------------
	type getInput struct {
		Name string `path:"name"`
	}
	type getOutput struct {
		Body *Skill
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-skill",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}",
		Summary:     "Get a skill by name",
	}, func(ctx context.Context, input *getInput) (*getOutput, error) {
		sk, err := store.GetByName(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("get skill: %w", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(
				fmt.Sprintf("skill %q not found", input.Name))
		}
		return &getOutput{Body: sk}, nil
	})

	// -----------------------------------------------------------------
	// GET /api/skills — list skills
	// -----------------------------------------------------------------
	type listInput struct {
		Limit   int    `query:"limit"   default:"20" minimum:"1" maximum:"100"`
		Offset  int    `query:"offset"  default:"0"  minimum:"0"`
		Author  string `query:"author"`
		Tag     string `query:"tag"`
		License string `query:"license"`
	}
	type listBody struct {
		Skills []Skill `json:"skills"`
		Total  int     `json:"total"`
	}
	type listOutput struct {
		Body listBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-skills",
		Method:      http.MethodGet,
		Path:        "/api/skills",
		Summary:     "List skills",
	}, func(ctx context.Context, input *listInput) (*listOutput, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 20
		}
		skills, total, err := store.List(ctx, ListOptions{
			Limit:   limit,
			Offset:  input.Offset,
			Author:  input.Author,
			Tag:     input.Tag,
			License: input.License,
		})
		if err != nil {
			return nil, fmt.Errorf("list skills: %w", err)
		}
		if skills == nil {
			skills = []Skill{}
		}
		return &listOutput{Body: listBody{Skills: skills, Total: total}}, nil
	})

	// -----------------------------------------------------------------
	// DELETE /api/skills/{name} — delete a skill
	// -----------------------------------------------------------------
	type deleteInput struct {
		Name string `path:"name"`
	}
	huma.Register(api, huma.Operation{
		OperationID:   "delete-skill",
		Method:        http.MethodDelete,
		Path:          "/api/skills/{name}",
		Summary:       "Delete a skill",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *deleteInput) (*struct{}, error) {
		sk, err := store.GetByName(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("delete skill lookup: %w", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(
				fmt.Sprintf("skill %q not found", input.Name))
		}
		// Clean up archive files before deleting the DB record.
		versions, err := store.ListVersions(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("delete skill: list versions for cleanup: %w", err)
		}
		for _, v := range versions {
			if v.ArchivePath != "" {
				_ = storage.Delete(ctx, v.ArchivePath)
			}
		}
		if err := store.Delete(ctx, input.Name); err != nil {
			return nil, fmt.Errorf("delete skill: %w", err)
		}
		return nil, nil
	})

	// -----------------------------------------------------------------
	// POST /api/skills/{name}/versions — publish a new version
	// -----------------------------------------------------------------
	publisher := NewPublisher(store, storage, opts)
	type publishInput struct {
		Name     string `path:"name"`
		Override bool   `query:"override" doc:"Publish despite blocking scan findings. Owner or admin only; recorded server-side."`
		RawBody  []byte `contentType:"application/gzip,application/octet-stream"`
	}
	type publishOutput struct {
		Status int
		Body   *PublishBody
	}
	huma.Register(api, huma.Operation{
		OperationID:   "publish-skill-version",
		Method:        http.MethodPost,
		Path:          "/api/skills/{name}/versions",
		Summary:       "Publish a new skill version. A git-bound skill gets a pull request instead, with 202.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *publishInput) (*publishOutput, error) {
		if opts.Git != nil {
			bound, _, err := opts.Git.Bound(ctx, input.Name)
			if err != nil {
				return nil, fmt.Errorf("publish: git binding: %w", err)
			}
			if bound {
				res, err := publisher.OpenChange(ctx, input.Name, input.RawBody, "", opts.Git)
				if err != nil {
					return nil, err
				}
				return &publishOutput{Status: http.StatusAccepted, Body: res}, nil
			}
		}
		res, err := publisher.Publish(ctx, input.Name, input.RawBody, input.Override)
		if err != nil {
			return nil, err
		}
		return &publishOutput{Status: http.StatusCreated, Body: res}, nil
	})

	// -----------------------------------------------------------------
	// GET /api/skills/{name}/versions — list versions
	// -----------------------------------------------------------------
	type listVersionsInput struct {
		Name string `path:"name"`
	}
	type listVersionsBody struct {
		Versions []Version `json:"versions"`
	}
	type listVersionsOutput struct {
		Body listVersionsBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-skill-versions",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/versions",
		Summary:     "List versions of a skill",
	}, func(ctx context.Context, input *listVersionsInput) (*listVersionsOutput, error) {
		sk, err := store.GetByName(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("list versions: lookup skill: %w", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(
				fmt.Sprintf("skill %q not found", input.Name))
		}

		versions, err := store.ListVersions(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("list versions: %w", err)
		}
		if versions == nil {
			versions = []Version{}
		}
		return &listVersionsOutput{Body: listVersionsBody{Versions: versions}}, nil
	})

	// -----------------------------------------------------------------
	// GET /api/search?q=...&limit=20 — full-text + fuzzy search
	// -----------------------------------------------------------------
	type searchInput struct {
		Q     string `query:"q"     required:"true" minLength:"1"`
		Limit int    `query:"limit" default:"20"    minimum:"1" maximum:"100"`
	}
	type searchBody struct {
		Skills []Skill `json:"skills"`
	}
	type searchOutput struct {
		Body searchBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "search-skills",
		Method:      http.MethodGet,
		Path:        "/api/search",
		Summary:     "Search skills by full-text and fuzzy name matching",
	}, func(ctx context.Context, input *searchInput) (*searchOutput, error) {
		limit := input.Limit
		if limit == 0 {
			limit = 20
		}
		skills, err := store.Search(ctx, input.Q, limit)
		if err != nil {
			return nil, fmt.Errorf("search skills: %w", err)
		}
		if skills == nil {
			skills = []Skill{}
		}
		return &searchOutput{Body: searchBody{Skills: skills}}, nil
	})

	// -----------------------------------------------------------------
	// PUT /api/skills/review — bulk review (must be registered before
	// /api/skills/{name}/review so the static path takes precedence)
	// -----------------------------------------------------------------
	type bulkReviewBody struct {
		Names []string `json:"names" minItems:"1" maxItems:"100"`
	}
	type bulkReviewInput struct {
		Body bulkReviewBody
	}
	type bulkReviewResponseBody struct {
		Reviewed int `json:"reviewed"`
	}
	type bulkReviewOutput struct {
		Body bulkReviewResponseBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "bulk-review-skills",
		Method:      http.MethodPut,
		Path:        "/api/skills/review",
		Summary:     "Bulk mark skills as reviewed",
	}, func(ctx context.Context, input *bulkReviewInput) (*bulkReviewOutput, error) {
		reviewedBy := "admin"
		if u := auth.UserFromContext(ctx); u != nil {
			reviewedBy = u.Name
		}
		n, err := store.BulkSetReview(ctx, input.Body.Names, reviewedBy)
		if err != nil {
			return nil, fmt.Errorf("bulk review: %w", err)
		}
		return &bulkReviewOutput{Body: bulkReviewResponseBody{Reviewed: n}}, nil
	})

	// -----------------------------------------------------------------
	// PUT /api/skills/{name}/review — mark a skill as reviewed
	// -----------------------------------------------------------------
	type reviewInput struct {
		Name string `path:"name"`
	}
	type reviewOutput struct {
		Body *Skill
	}
	huma.Register(api, huma.Operation{
		OperationID: "review-skill",
		Method:      http.MethodPut,
		Path:        "/api/skills/{name}/review",
		Summary:     "Mark skill as reviewed",
	}, func(ctx context.Context, input *reviewInput) (*reviewOutput, error) {
		sk, err := store.GetByName(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("review skill: %w", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(
				fmt.Sprintf("skill %q not found", input.Name))
		}
		reviewedBy := "admin"
		if u := auth.UserFromContext(ctx); u != nil {
			reviewedBy = u.Name
		}
		if err := store.SetReview(ctx, input.Name, reviewedBy); err != nil {
			return nil, fmt.Errorf("review skill: %w", err)
		}
		sk, err = store.GetByName(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("review skill: fetch updated: %w", err)
		}
		return &reviewOutput{Body: sk}, nil
	})

	// -----------------------------------------------------------------
	// DELETE /api/skills/{name}/review — unmark a skill as reviewed
	// -----------------------------------------------------------------
	type unreviewInput struct {
		Name string `path:"name"`
	}
	huma.Register(api, huma.Operation{
		OperationID:   "unreview-skill",
		Method:        http.MethodDelete,
		Path:          "/api/skills/{name}/review",
		Summary:       "Unmark skill as reviewed",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *unreviewInput) (*struct{}, error) {
		sk, err := store.GetByName(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("unreview skill: %w", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(
				fmt.Sprintf("skill %q not found", input.Name))
		}
		if err := store.ClearReview(ctx, input.Name); err != nil {
			return nil, fmt.Errorf("unreview skill: %w", err)
		}
		return nil, nil
	})

	// -----------------------------------------------------------------
	// GET /api/skills/{name}/aliases
	// -----------------------------------------------------------------
	type aliasListInput struct {
		Name string `path:"name"`
	}
	type aliasListOutput struct {
		Body []Alias
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-skill-aliases",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/aliases",
		Summary:     "List aliases for a skill",
	}, func(ctx context.Context, input *aliasListInput) (*aliasListOutput, error) {
		aliases, err := store.ListAliases(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("list aliases: %w", err)
		}
		return &aliasListOutput{Body: aliases}, nil
	})

	// -----------------------------------------------------------------
	// POST /api/skills/{name}/aliases
	// -----------------------------------------------------------------
	type aliasCreateBody struct {
		Alias string `json:"alias" minLength:"1"`
	}
	type aliasCreateInput struct {
		Name string `path:"name"`
		Body aliasCreateBody
	}
	huma.Register(api, huma.Operation{
		OperationID:   "create-skill-alias",
		Method:        http.MethodPost,
		Path:          "/api/skills/{name}/aliases",
		Summary:       "Add an alias for a skill",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *aliasCreateInput) (*struct{}, error) {
		sk, err := store.GetByName(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("create alias: %w", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("skill %q not found", input.Name))
		}
		// Reject aliases that would shadow an existing skill name.
		existing, err := store.GetByName(ctx, input.Body.Alias)
		if err != nil {
			return nil, fmt.Errorf("create alias: %w", err)
		}
		if existing != nil {
			return nil, huma.Error409Conflict(fmt.Sprintf("a skill named %q already exists", input.Body.Alias))
		}
		if err := store.CreateAlias(ctx, input.Body.Alias, input.Name); err != nil {
			return nil, fmt.Errorf("create alias: %w", err)
		}
		return nil, nil
	})

	// -----------------------------------------------------------------
	// DELETE /api/skills/{name}/aliases/{alias}
	// -----------------------------------------------------------------
	type aliasDeleteInput struct {
		Name  string `path:"name"`
		Alias string `path:"alias"`
	}
	huma.Register(api, huma.Operation{
		OperationID:   "delete-skill-alias",
		Method:        http.MethodDelete,
		Path:          "/api/skills/{name}/aliases/{alias}",
		Summary:       "Remove an alias",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *aliasDeleteInput) (*struct{}, error) {
		canonical, err := store.ResolveAlias(ctx, input.Alias)
		if err != nil {
			return nil, fmt.Errorf("delete alias: %w", err)
		}
		if canonical != input.Name {
			return nil, huma.Error404NotFound(fmt.Sprintf("alias %q not found for skill %q", input.Alias, input.Name))
		}
		if err := store.DeleteAlias(ctx, input.Alias); err != nil {
			return nil, fmt.Errorf("delete alias: %w", err)
		}
		return nil, nil
	})

	// -----------------------------------------------------------------
	// POST /api/skills/merge
	// -----------------------------------------------------------------
	type mergeBody struct {
		Source string `json:"source" minLength:"1"`
		Target string `json:"target" minLength:"1"`
	}
	type mergeInput struct {
		Body mergeBody
	}
	type mergeOutput struct {
		Body *Skill
	}
	huma.Register(api, huma.Operation{
		OperationID: "merge-skills",
		Method:      http.MethodPost,
		Path:        "/api/skills/merge",
		Summary:     "Merge source skill into target skill",
	}, func(ctx context.Context, input *mergeInput) (*mergeOutput, error) {
		if input.Body.Source == input.Body.Target {
			return nil, huma.Error400BadRequest("cannot merge a skill into itself")
		}
		if opts.Git != nil {
			for _, n := range []string{input.Body.Source, input.Body.Target} {
				bound, where, err := opts.Git.Bound(ctx, n)
				if err != nil {
					return nil, fmt.Errorf("merge: git binding: %w", err)
				}
				if bound {
					return nil, ErrGitBound(n, where)
				}
			}
		}
		merged, err := store.Merge(ctx, input.Body.Source, input.Body.Target)
		if err != nil {
			errMsg := err.Error()
			if strings.Contains(errMsg, "not found") {
				return nil, huma.Error404NotFound(errMsg)
			}
			return nil, fmt.Errorf("merge skills: %w", err)
		}
		return &mergeOutput{Body: merged}, nil
	})

	// -----------------------------------------------------------------
	// Raw routes registered directly on the Chi router (streaming responses).
	// -----------------------------------------------------------------
	if router != nil {
		// GET /api/skills/{name}/versions/{version}/download
		router.Get("/api/skills/{name}/versions/{version}/download",
			makeDownloadHandler(store, storage))

		// GET /api/skills/{name}/versions/{version}/files/{path}
		router.Get("/api/skills/{name}/versions/{version}/files/*",
			makeFileHandler(store, storage))

		// GET /api/skills/{name}/scan — scan results for the latest version
		router.Get("/api/skills/{name}/scan", makeLatestScanHandler(store))

		// GET /api/skills/{name}/versions/{version}/diff — diff a version
		// against the currently-served one. Open to any authenticated user
		// (auth middleware already covers /api/), not just owners/admins: a
		// reviewer isn't the only person who benefits from seeing what a
		// held version actually changed.
		router.Get("/api/skills/{name}/versions/{version}/diff", makeDiffHandler(store))
	}

	// POST /api/skills/{name}/versions/{version}/review — owner/admin (or,
	// for the ownership reason only, a namespace owner) approval or
	// rejection of a version held for review.
	registerReviewRoutes(api, store, opts.Ownership)
}

// CanReadVersion is the read rule for a version's content. Any person may read
// any version, and the worker must reach a held version to score it. Another
// scoped key is a machine that would run what it fetches, so it gets only a
// released version.
func CanReadVersion(ctx context.Context, ver *Version) bool {
	key := auth.KeyFromContext(ctx)
	return !key.Restricted() || ver.GateState == "released" || auth.HasScope(ctx, auth.ScopeEvalWork)
}

// makeDownloadHandler returns a handler that streams the archive for a specific
// version of a skill.
func makeDownloadHandler(store *Store, storage platform.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		versionStr := chi.URLParam(r, "version")
		version, err := strconv.Atoi(versionStr)
		if err != nil {
			http.Error(w, "invalid version number", http.StatusBadRequest)
			return
		}

		ver, err := store.GetVersion(r.Context(), name, version)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if ver == nil {
			http.NotFound(w, r)
			return
		}

		if !CanReadVersion(r.Context(), ver) {
			http.NotFound(w, r)
			return
		}

		rc, err := storage.Read(r.Context(), ver.ArchivePath)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer rc.Close()

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf(`attachment; filename="%s-v%d.tar.gz"`, name, version))
		w.WriteHeader(http.StatusOK)
		io.Copy(w, rc) //nolint:errcheck
	}
}

// makeFileHandler returns a handler that serves one file from a version's
// archive, under the same read rule as the download route.
//
// Archive content is untrusted and the app shares its origin, so the response
// is always an attachment of an opaque type that a browser never renders.
func makeFileHandler(store *Store, storage platform.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version, err := strconv.Atoi(chi.URLParam(r, "version"))
		if err != nil {
			http.Error(w, "invalid version number", http.StatusBadRequest)
			return
		}

		// Chi matches on RawPath when it is set, and the wildcard is then still
		// escaped. Without this, %2e%2e reaches the checks as literal text.
		requested := chi.URLParam(r, "*")
		if r.URL.RawPath != "" {
			requested, err = url.PathUnescape(requested)
			if err != nil {
				http.Error(w, "invalid file path", http.StatusBadRequest)
				return
			}
		}
		filePath, ok := cleanArchivePath(requested)
		if !ok {
			http.Error(w, "invalid file path", http.StatusBadRequest)
			return
		}

		name := chi.URLParam(r, "name")
		ver, err := store.GetVersion(r.Context(), name, version)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if ver == nil || !CanReadVersion(r.Context(), ver) {
			http.NotFound(w, r)
			return
		}

		rc, err := storage.Read(r.Context(), ver.ArchivePath)
		if err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer rc.Close()

		content, err := readArchiveFile(rc, filePath)
		if errors.Is(err, errFileNotInArchive) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			log.Warn().Err(err).Str("skill", name).Int("version", version).Msg("file handler: read archive failed")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(filePath)})
		if disposition == "" {
			disposition = "attachment"
		}
		size := strconv.Itoa(len(content))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", disposition)
		w.Header().Set("Content-Length", size)
		w.Header().Set("X-Skael-File-Size", size)
		w.WriteHeader(http.StatusOK)
		w.Write(content) //nolint:errcheck
	}
}

// makeDiffHandler returns a handler that diffs a specific version of a
// skill against the version currently served.
func makeDiffHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		versionStr := chi.URLParam(r, "version")
		version, err := strconv.Atoi(versionStr)
		if err != nil {
			http.Error(w, "invalid version number", http.StatusBadRequest)
			return
		}

		diff, err := store.DiffAgainstServed(r.Context(), name, version)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if diff == nil {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(diff); err != nil {
			log.Warn().Err(err).Str("skill", name).Int("version", version).Msg("diff handler: encode response failed")
		}
	}
}

// makeLatestScanHandler returns a handler that returns the scan result JSON for
// the latest version of a skill.
func makeLatestScanHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")

		sk, err := store.GetByName(r.Context(), name)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if sk == nil {
			http.NotFound(w, r)
			return
		}
		if sk.LatestVersion == 0 {
			http.Error(w, "no versions published", http.StatusNotFound)
			return
		}

		ver, err := store.GetVersion(r.Context(), name, sk.LatestVersion)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if ver == nil {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(ver.ScanResult) //nolint:errcheck
	}
}
