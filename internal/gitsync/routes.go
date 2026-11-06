package gitsync

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/platform"
)

// Wire types carry the "github" prefix: Huma names schemas globally, and a
// collision panics the server at start.

type githubAppBody struct {
	Name       string    `json:"name"`
	Slug       string    `json:"slug"`
	Owner      string    `json:"owner"`
	HTMLURL    string    `json:"html_url"`
	InstallURL string    `json:"install_url"`
	WebBaseURL string    `json:"web_base_url"`
	CreatedBy  string    `json:"created_by"`
	CreatedAt  time.Time `json:"created_at"`
}

type githubStatusBody struct {
	// KeysReady is false until SKAEL_MASTER_KEYS is set; nothing can be
	// connected before then.
	KeysReady bool           `json:"keys_ready"`
	PublicURL string         `json:"public_url"`
	App       *githubAppBody `json:"app,omitempty"`
}

type githubStatusOutput struct {
	Body githubStatusBody
}

type githubManifestBody struct {
	Name         string `json:"name" minLength:"1" maxLength:"34"`
	Organization string `json:"organization,omitempty"`
	// WebBaseURL names a GitHub Enterprise Server, e.g. https://ghe.example.com.
	WebBaseURL string `json:"web_base_url,omitempty"`
	// APIBaseURL defaults to WebBaseURL + /api/v3 for an Enterprise Server.
	APIBaseURL string `json:"api_base_url,omitempty"`
}

type githubManifestInput struct {
	Body githubManifestBody
}

type githubManifestOutputBody struct {
	// PostURL takes the manifest as a form field named "manifest", posted by
	// the browser, so GitHub sees the admin's own session.
	PostURL  string `json:"post_url"`
	Manifest string `json:"manifest"`
}

type githubManifestOutput struct {
	Body githubManifestOutputBody
}

type githubCallbackInput struct {
	Code  string `query:"code"`
	State string `query:"state"`
}

type githubCallbackOutput struct {
	Status   int
	Location string `header:"Location"`
}

// RouteOptions are the dependencies the routes need beyond the store.
type RouteOptions struct {
	PublicURL string
	// HTTPClient reaches GitHub. Nil means a client with a 30 s timeout.
	HTTPClient *http.Client
}

func requirePrivileged(ctx context.Context) (*auth.User, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil, huma.Error401Unauthorized("not authenticated")
	}
	if !u.IsPrivileged() {
		return nil, huma.Error403Forbidden("owner or admin role required")
	}
	return u, nil
}

func toAppBody(a *App) *githubAppBody {
	return &githubAppBody{
		Name: a.Name, Slug: a.Slug, Owner: a.OwnerLogin, HTMLURL: a.HTMLURL, InstallURL: a.InstallURL(),
		WebBaseURL: a.WebBaseURL, CreatedBy: a.CreatedBy, CreatedAt: a.CreatedAt,
	}
}

// RegisterRoutes wires the GitHub App setup. Only owners and admins reach it,
// because the App can write to every repo it is installed on.
func RegisterRoutes(api huma.API, store *Store, opts RouteOptions) {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	huma.Register(api, huma.Operation{
		OperationID: "get-github-app",
		Method:      http.MethodGet,
		Path:        "/api/admin/github",
		Summary:     "Show whether this instance has a GitHub App",
	}, func(ctx context.Context, _ *struct{}) (*githubStatusOutput, error) {
		if _, err := requirePrivileged(ctx); err != nil {
			return nil, err
		}
		out := &githubStatusOutput{Body: githubStatusBody{KeysReady: store.KeysReady(), PublicURL: opts.PublicURL}}
		if !store.KeysReady() {
			return out, nil
		}
		a, err := store.App(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("get GitHub App: internal error", err)
		}
		if a != nil {
			out.Body.App = toAppBody(a)
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-github-app-manifest",
		Method:      http.MethodPost,
		Path:        "/api/admin/github/manifest",
		Summary:     "Start creating the GitHub App from a manifest",
	}, func(ctx context.Context, in *githubManifestInput) (*githubManifestOutput, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		if !store.KeysReady() {
			return nil, huma.Error409Conflict("set SKAEL_MASTER_KEYS before you connect GitHub; it encrypts the App's private key")
		}
		if opts.PublicURL == "" {
			return nil, huma.Error422UnprocessableEntity("set PUBLIC_URL first; GitHub sends its webhooks and its redirect there")
		}
		existing, err := store.App(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("start GitHub App: internal error", err)
		}
		if existing != nil {
			return nil, huma.Error409Conflict(ErrAppExists.Error())
		}

		web, apiBase, err := baseURLs(in.Body.WebBaseURL, in.Body.APIBaseURL)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		state, err := store.StartManifest(ctx, web, apiBase, u.Email)
		if err != nil {
			return nil, huma.Error500InternalServerError("start GitHub App: internal error", err)
		}
		postURL, manifest, err := Manifest(ManifestRequest{
			Name: strings.TrimSpace(in.Body.Name), Organization: in.Body.Organization,
			WebBaseURL: web, APIBaseURL: apiBase, PublicURL: opts.PublicURL,
		}, state)
		if err != nil {
			return nil, huma.Error500InternalServerError("start GitHub App: internal error", err)
		}
		return &githubManifestOutput{Body: githubManifestOutputBody{PostURL: postURL, Manifest: manifest}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "complete-github-app",
		Method:        http.MethodGet,
		Path:          "/api/admin/github/callback",
		Summary:       "GitHub returns here after it creates the App",
		DefaultStatus: http.StatusFound,
	}, func(ctx context.Context, in *githubCallbackInput) (*githubCallbackOutput, error) {
		if _, err := requirePrivileged(ctx); err != nil {
			return nil, err
		}
		back := func(q url.Values) *githubCallbackOutput {
			return &githubCallbackOutput{Status: http.StatusFound, Location: "/settings/github?" + q.Encode()}
		}
		if in.Code == "" || in.State == "" {
			return back(url.Values{"error": {"GitHub returned no code. Start the setup again."}}), nil
		}
		a, err := store.Complete(ctx, client, in.State, in.Code)
		if err != nil {
			msg := err.Error()
			if !errors.Is(err, ErrManifestState) && !errors.Is(err, ErrAppExists) && !errors.Is(err, platform.ErrNoMasterKey) {
				log.Error().Err(err).Msg("gitsync: completing the GitHub App failed")
			}
			return back(url.Values{"error": {msg}}), nil
		}
		log.Info().Str("app", a.Slug).Str("owner", a.OwnerLogin).Msg("gitsync: GitHub App connected")
		return back(url.Values{"connected": {a.Slug}}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "delete-github-app",
		Method:        http.MethodDelete,
		Path:          "/api/admin/github",
		Summary:       "Disconnect the GitHub App",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		ok, err := store.Delete(ctx, u.Email)
		if err != nil {
			return nil, huma.Error500InternalServerError("disconnect GitHub App: internal error", err)
		}
		if !ok {
			return nil, huma.Error404NotFound("this instance has no GitHub App")
		}
		return nil, nil
	})
}

// baseURLs resolves the web and API base URLs. Both empty means github.com.
func baseURLs(web, apiBase string) (string, string, error) {
	web, apiBase = strings.TrimSuffix(strings.TrimSpace(web), "/"), strings.TrimSuffix(strings.TrimSpace(apiBase), "/")
	if web == "" && apiBase == "" {
		return DefaultWebBaseURL, DefaultAPIBaseURL, nil
	}
	if web == "" {
		return "", "", errors.New("name the GitHub Enterprise web URL as well as its API URL")
	}
	u, err := url.Parse(web)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", "", errors.New("the GitHub web URL must be an https URL, e.g. https://ghe.example.com")
	}
	if apiBase == "" {
		apiBase = web + "/api/v3"
	}
	return web, apiBase, nil
}
