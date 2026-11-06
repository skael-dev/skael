package gitsync

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
)

type gitConnectionRequestBody struct {
	RepoOwner   string `json:"repo_owner" minLength:"1"`
	RepoName    string `json:"repo_name" minLength:"1"`
	Branch      string `json:"branch" minLength:"1"`
	RootPath    string `json:"root_path,omitempty"`
	Prefix      string `json:"prefix" minLength:"2"`
	ContestMode string `json:"contest_mode,omitempty" enum:"off,on_request,ready,"`
}

func (b gitConnectionRequestBody) request() CreateRequest { return CreateRequest(b) }

type gitConnectionPreviewInput struct {
	Body gitConnectionRequestBody
}

type gitConnectionPreviewOutput struct {
	Body Preview
}

// gitConnectionCreateBody repeats the request fields: Huma does not flatten an
// unexported embedded struct into the schema.
type gitConnectionCreateBody struct {
	RepoOwner   string `json:"repo_owner" minLength:"1"`
	RepoName    string `json:"repo_name" minLength:"1"`
	Branch      string `json:"branch" minLength:"1"`
	RootPath    string `json:"root_path,omitempty"`
	Prefix      string `json:"prefix" minLength:"2"`
	ContestMode string `json:"contest_mode,omitempty" enum:"off,on_request,ready,"`
	// Adopt names the existing skills to bind: any from the adopt or match
	// groups of the preview.
	Adopt []string `json:"adopt,omitempty"`
}

type gitConnectionCreateInput struct {
	Body gitConnectionCreateBody
}

type gitConnectionOutput struct {
	Status int
	Body   Connection
}

type gitConnectionListOutput struct {
	Body struct {
		Connections []Connection `json:"connections"`
	}
}

type gitConnectionIDInput struct {
	ID string `path:"id" format:"uuid"`
}

// GitConnectionDetail is a connection with its skills and refusals. Exported
// because Huma drops an unexported embedded struct from a response.
type GitConnectionDetail struct {
	Connection
	Bindings []Binding `json:"bindings"`
	Refusals []Refusal `json:"refusals"`
	// Webhooks is true when GitHub reached this instance for the connection.
	// False means changes arrive through the minute-by-minute check only.
	Webhooks bool `json:"webhooks"`
}

type gitConnectionDetailOutput struct {
	Body GitConnectionDetail
}

type gitConnectionUpdateInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		ContestMode string `json:"contest_mode" enum:"off,on_request,ready"`
	}
}

type gitConnectionAdoptInput struct {
	ID   string `path:"id" format:"uuid"`
	Body struct {
		Name string `json:"name" minLength:"1"`
	}
}

type gitReposOutput struct {
	Body struct {
		Repos []Repo `json:"repos"`
	}
}

type gitSkillInput struct {
	Name string `path:"name"`
}

// GitSkillBinding says where a git-bound skill lives. Bound is false for a
// native skill.
type GitSkillBinding struct {
	Bound      bool        `json:"bound"`
	Binding    *Binding    `json:"binding,omitempty"`
	Connection *Connection `json:"connection,omitempty"`
}

type gitSkillOutput struct {
	Body GitSkillBinding
}

type gitPullsOutput struct {
	Body struct {
		Pulls []PullCheck `json:"pulls"`
	}
}

type gitPullContestInput struct {
	ID     string `path:"id" format:"uuid"`
	Number int    `path:"number"`
}

type gitPRHeadInput struct {
	ID string `path:"id" format:"uuid"`
}

type gitPRHeadOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

type gitChangesOutput struct {
	Body struct {
		Changes []Change `json:"changes"`
	}
}

func mapError(what string, err error) error {
	var ue *UserError
	var apiErr *APIError
	switch {
	case errors.As(err, &ue):
		return huma.Error422UnprocessableEntity(ue.Error())
	case errors.Is(err, ErrForbidden):
		return huma.Error403Forbidden(err.Error())
	case errors.Is(err, ErrNoApp):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrNotFound), errors.As(err, &apiErr):
		return huma.Error502BadGateway(what + ": " + err.Error())
	}
	log.Error().Err(err).Msg("gitsync: " + what)
	return huma.Error500InternalServerError(what + ": internal error")
}

func requireUser(ctx context.Context) (*auth.User, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil, huma.Error401Unauthorized("not authenticated")
	}
	if u.Kind == auth.KindService {
		return nil, huma.Error403Forbidden("a service account cannot manage git connections")
	}
	return u, nil
}

// RegisterConnectionRoutes wires connections. Any person may read them; only
// an admin or an owner of a connection's prefix may change one.
func RegisterConnectionRoutes(api huma.API, syncer *Syncer, store *Store, jobs JobInserter) {
	enqueue := func(ctx context.Context, id string, force bool) {
		if jobs == nil {
			return
		}
		if _, err := jobs.Insert(ctx, SyncArgs{ConnectionID: id, Force: force}, nil); err != nil {
			log.Error().Err(err).Str("connection", id).Msg("gitsync: enqueue sync")
		}
	}

	huma.Register(api, huma.Operation{
		OperationID: "list-git-repos", Method: http.MethodGet, Path: "/api/git/repos",
		Summary: "List the repos the GitHub App can reach",
	}, func(ctx context.Context, _ *struct{}) (*gitReposOutput, error) {
		if _, err := requireUser(ctx); err != nil {
			return nil, err
		}
		repos, err := syncer.Repos(ctx)
		if err != nil {
			return nil, mapError("list repos", err)
		}
		out := &gitReposOutput{}
		out.Body.Repos = repos
		if out.Body.Repos == nil {
			out.Body.Repos = []Repo{}
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "preview-git-connection", Method: http.MethodPost, Path: "/api/git/connections/preview",
		Summary: "Show what connecting a repo path would adopt, create and refuse",
	}, func(ctx context.Context, in *gitConnectionPreviewInput) (*gitConnectionPreviewOutput, error) {
		u, err := requireUser(ctx)
		if err != nil {
			return nil, err
		}
		p, err := syncer.Preview(ctx, u, in.Body.request())
		if err != nil {
			return nil, mapError("preview connection", err)
		}
		return &gitConnectionPreviewOutput{Body: *p}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-git-connection", Method: http.MethodPost, Path: "/api/git/connections",
		Summary: "Connect a repo path to a name prefix", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *gitConnectionCreateInput) (*gitConnectionOutput, error) {
		u, err := requireUser(ctx)
		if err != nil {
			return nil, err
		}
		b := in.Body
		req := CreateRequest{RepoOwner: b.RepoOwner, RepoName: b.RepoName, Branch: b.Branch,
			RootPath: b.RootPath, Prefix: b.Prefix, ContestMode: b.ContestMode}
		c, err := syncer.Create(ctx, u, req, b.Adopt)
		if err != nil {
			return nil, mapError("create connection", err)
		}
		enqueue(ctx, c.ID, true)
		return &gitConnectionOutput{Status: http.StatusCreated, Body: *c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-git-connections", Method: http.MethodGet, Path: "/api/git/connections",
		Summary: "List git connections",
	}, func(ctx context.Context, _ *struct{}) (*gitConnectionListOutput, error) {
		if _, err := requireUser(ctx); err != nil {
			return nil, err
		}
		cs, err := store.Connections(ctx)
		if err != nil {
			return nil, mapError("list connections", err)
		}
		out := &gitConnectionListOutput{}
		out.Body.Connections = cs
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-git-connection", Method: http.MethodGet, Path: "/api/git/connections/{id}",
		Summary: "Get a git connection with its skills and refusals",
	}, func(ctx context.Context, in *gitConnectionIDInput) (*gitConnectionDetailOutput, error) {
		if _, err := requireUser(ctx); err != nil {
			return nil, err
		}
		c, err := store.Connection(ctx, in.ID)
		if err != nil {
			return nil, mapError("get connection", err)
		}
		if c == nil {
			return nil, huma.Error404NotFound("connection not found")
		}
		bindings, err := store.Bindings(ctx, c.ID)
		if err != nil {
			return nil, mapError("get connection", err)
		}
		refusals, err := store.Refusals(ctx, c.ID)
		if err != nil {
			return nil, mapError("get connection", err)
		}
		return &gitConnectionDetailOutput{Body: GitConnectionDetail{
			Connection: *c, Bindings: bindings, Refusals: refusals, Webhooks: c.LastWebhookAt != nil,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-git-connection", Method: http.MethodPut, Path: "/api/git/connections/{id}",
		Summary: "Change when a connection's PRs get a contest",
	}, func(ctx context.Context, in *gitConnectionUpdateInput) (*gitConnectionOutput, error) {
		u, err := requireUser(ctx)
		if err != nil {
			return nil, err
		}
		c, err := syncer.SetContestMode(ctx, u, in.ID, in.Body.ContestMode)
		if err != nil {
			return nil, mapError("update connection", err)
		}
		if c == nil {
			return nil, huma.Error404NotFound("connection not found")
		}
		return &gitConnectionOutput{Status: http.StatusOK, Body: *c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-git-connection", Method: http.MethodDelete, Path: "/api/git/connections/{id}",
		Summary: "Remove a connection; its skills become native", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *gitConnectionIDInput) (*struct{}, error) {
		u, err := requireUser(ctx)
		if err != nil {
			return nil, err
		}
		ok, err := syncer.Delete(ctx, u, in.ID)
		if err != nil {
			return nil, mapError("delete connection", err)
		}
		if !ok {
			return nil, huma.Error404NotFound("connection not found")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "sync-git-connection", Method: http.MethodPost, Path: "/api/git/connections/{id}/sync",
		Summary: "Sync a connection now", DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *gitConnectionIDInput) (*struct{}, error) {
		u, err := requireUser(ctx)
		if err != nil {
			return nil, err
		}
		c, err := syncer.manageable(ctx, u, in.ID)
		if err != nil {
			return nil, mapError("sync connection", err)
		}
		if c == nil {
			return nil, huma.Error404NotFound("connection not found")
		}
		enqueue(ctx, c.ID, true)
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "adopt-git-skill", Method: http.MethodPost, Path: "/api/git/connections/{id}/adopt",
		Summary: "Bind a skill a sync refused as taken",
	}, func(ctx context.Context, in *gitConnectionAdoptInput) (*gitConnectionOutput, error) {
		u, err := requireUser(ctx)
		if err != nil {
			return nil, err
		}
		c, err := syncer.Adopt(ctx, u, in.ID, in.Body.Name)
		if err != nil {
			return nil, mapError("adopt skill", err)
		}
		if c == nil {
			return nil, huma.Error404NotFound("connection not found")
		}
		enqueue(ctx, c.ID, true)
		return &gitConnectionOutput{Status: http.StatusOK, Body: *c}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-skill-git", Method: http.MethodGet, Path: "/api/skills/{name}/git",
		Summary: "Show where a git-bound skill lives",
	}, func(ctx context.Context, in *gitSkillInput) (*gitSkillOutput, error) {
		b, err := store.BindingForSkill(ctx, in.Name)
		if err != nil {
			return nil, mapError("get skill binding", err)
		}
		out := &gitSkillOutput{}
		if b == nil {
			return out, nil
		}
		c, err := store.Connection(ctx, b.ConnectionID)
		if err != nil {
			return nil, mapError("get skill binding", err)
		}
		out.Body = GitSkillBinding{Bound: true, Binding: b, Connection: c}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-git-pulls", Method: http.MethodGet, Path: "/api/git/connections/{id}/pulls",
		Summary: "List the Skael checks on a connection's open pull requests",
	}, func(ctx context.Context, in *gitConnectionIDInput) (*gitPullsOutput, error) {
		if _, err := requireUser(ctx); err != nil {
			return nil, err
		}
		pulls, err := store.PullChecks(ctx, in.ID)
		if err != nil {
			return nil, mapError("list pull requests", err)
		}
		out := &gitPullsOutput{}
		out.Body.Pulls = pulls
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "request-git-pull-contest", Method: http.MethodPost,
		Path: "/api/git/connections/{id}/pulls/{number}/contest", DefaultStatus: http.StatusAccepted,
		Summary: "Ask for a contest on a pull request's newest head",
	}, func(ctx context.Context, in *gitPullContestInput) (*struct{}, error) {
		u, err := requireUser(ctx)
		if err != nil {
			return nil, err
		}
		c, err := syncer.manageable(ctx, u, in.ID)
		if err != nil {
			return nil, mapError("request a contest", err)
		}
		if c == nil {
			return nil, huma.Error404NotFound("connection not found")
		}
		if c.ContestMode == ContestOff {
			return nil, huma.Error409Conflict("contests are off for this connection; change its contest setting first")
		}
		ok, err := store.RequestContest(ctx, c.ID, in.Number, "")
		if err != nil {
			return nil, mapError("request a contest", err)
		}
		if !ok {
			return nil, huma.Error404NotFound("Skael has not checked this pull request yet")
		}
		enqueue(ctx, c.ID, false)
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-git-pr-head-archive", Method: http.MethodGet, Path: "/api/git/pr-heads/{id}/archive",
		Summary: "Download a stored PR head, for the worker running its contest",
	}, func(ctx context.Context, in *gitPRHeadInput) (*gitPRHeadOutput, error) {
		u := auth.UserFromContext(ctx)
		if u == nil || (!u.IsPrivileged() && !auth.HasScope(ctx, auth.ScopeEvalWork)) {
			return nil, huma.Error403Forbidden("a worker key with eval:work, or an owner or admin, reads PR heads")
		}
		raw, err := syncer.PRHeadArchive(ctx, in.ID)
		if err != nil {
			return nil, mapError("read PR head", err)
		}
		if raw == nil {
			return nil, huma.Error404NotFound("PR head not found")
		}
		return &gitPRHeadOutput{ContentType: "application/gzip", Body: raw}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-skill-changes", Method: http.MethodGet, Path: "/api/skills/{name}/changes",
		Summary: "List the pull requests Skael opened for a git-bound skill",
	}, func(ctx context.Context, in *gitSkillInput) (*gitChangesOutput, error) {
		cs, err := store.ChangesForSkill(ctx, in.Name)
		if err != nil {
			return nil, mapError("list changes", err)
		}
		out := &gitChangesOutput{}
		out.Body.Changes = cs
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "detach-git-skill", Method: http.MethodPost, Path: "/api/skills/{name}/detach",
		Summary: "Make a git-bound skill native", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *gitSkillInput) (*struct{}, error) {
		u, err := requireUser(ctx)
		if err != nil {
			return nil, err
		}
		if err := syncer.Detach(ctx, u, in.Name); err != nil {
			return nil, mapError("detach skill", err)
		}
		return nil, nil
	})
}
