package sets

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/platform"
)

var setNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)

// checkKeySet applies a set-limited key's limit. A deleted set gives 403, so
// the key never becomes unrestricted and never learns which sets exist.
func checkKeySet(ctx context.Context, set *Set) error {
	key := auth.KeyFromContext(ctx)
	if key == nil || key.SetID == "" {
		return nil
	}
	if set == nil || set.ID != key.SetID {
		return huma.Error403Forbidden("this key is limited to another set")
	}
	return nil
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

func validateEntries(ctx context.Context, store *Store, entries []SetEntry) error {
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Name] {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("skill %q appears twice", e.Name))
		}
		seen[e.Name] = true
		if e.Version < 0 {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("skill %q has a negative version", e.Name))
		}
		ok, err := store.SkillVersionExists(ctx, e.Name, e.Version)
		if err != nil {
			return err
		}
		if !ok {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("skill %q has no version %s", e.Name, requested(e)))
		}
	}
	return nil
}

// SetDetail is a set with who last used it.
type SetDetail struct {
	Set
	Callers []SetCaller `json:"callers"`
}

// RegisterRoutes wires the set endpoints.
func RegisterRoutes(api huma.API, router chi.Router, store *Store, storage platform.Storage) {
	type setsListBody struct {
		Sets []Set `json:"sets"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-sets", Method: http.MethodGet, Path: "/api/sets", Summary: "List sets",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body setsListBody }, error) {
		if key := auth.KeyFromContext(ctx); key != nil && key.SetID != "" {
			set, err := store.GetByID(ctx, key.SetID)
			if err != nil {
				return nil, err
			}
			if err := checkKeySet(ctx, set); err != nil {
				return nil, err
			}
			return &struct{ Body setsListBody }{Body: setsListBody{Sets: []Set{*set}}}, nil
		}
		list, err := store.List(ctx)
		if err != nil {
			return nil, err
		}
		return &struct{ Body setsListBody }{Body: setsListBody{Sets: list}}, nil
	})

	type setPathInput struct {
		Name string `path:"name"`
	}
	load := func(ctx context.Context, name string) (*Set, error) {
		set, err := store.Get(ctx, name)
		if err != nil {
			return nil, err
		}
		if err := checkKeySet(ctx, set); err != nil {
			return nil, err
		}
		if set == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("set %q not found", name))
		}
		return set, nil
	}

	huma.Register(api, huma.Operation{
		OperationID: "get-set", Method: http.MethodGet, Path: "/api/sets/{name}", Summary: "Get a set",
	}, func(ctx context.Context, in *setPathInput) (*struct{ Body SetDetail }, error) {
		set, err := load(ctx, in.Name)
		if err != nil {
			return nil, err
		}
		callers, err := store.Callers(ctx, set)
		if err != nil {
			return nil, err
		}
		return &struct{ Body SetDetail }{Body: SetDetail{Set: *set, Callers: callers}}, nil
	})

	type setCreateBody struct {
		Name        string     `json:"name" minLength:"1" maxLength:"128"`
		Description string     `json:"description,omitempty" maxLength:"1000"`
		Entries     []SetEntry `json:"entries"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-set", Method: http.MethodPost, Path: "/api/sets",
		Summary: "Create a set", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct{ Body setCreateBody }) (*struct{ Body Set }, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		if !setNameRe.MatchString(in.Body.Name) {
			return nil, huma.Error422UnprocessableEntity("name must use lowercase letters, digits, colons, dots and hyphens")
		}
		if err := validateEntries(ctx, store, in.Body.Entries); err != nil {
			return nil, err
		}
		set, err := store.Create(ctx, in.Body.Name, in.Body.Description, in.Body.Entries, u.Email)
		if errors.Is(err, ErrNameTaken) {
			return nil, huma.Error409Conflict(fmt.Sprintf("set %q already exists", in.Body.Name))
		}
		if err != nil {
			return nil, err
		}
		return &struct{ Body Set }{Body: *set}, nil
	})

	type setUpdateBody struct {
		Description string     `json:"description,omitempty" maxLength:"1000"`
		Entries     []SetEntry `json:"entries"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "update-set", Method: http.MethodPut, Path: "/api/sets/{name}", Summary: "Replace a set's description and entries",
	}, func(ctx context.Context, in *struct {
		Name string `path:"name"`
		Body setUpdateBody
	}) (*struct{ Body Set }, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		if err := validateEntries(ctx, store, in.Body.Entries); err != nil {
			return nil, err
		}
		set, err := store.Update(ctx, in.Name, in.Body.Description, in.Body.Entries, u.Email)
		if err != nil {
			return nil, err
		}
		if set == nil {
			return nil, huma.Error404NotFound(fmt.Sprintf("set %q not found", in.Name))
		}
		return &struct{ Body Set }{Body: *set}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-set", Method: http.MethodDelete, Path: "/api/sets/{name}",
		Summary: "Delete a set", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *setPathInput) (*struct{}, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		ok, err := store.Delete(ctx, in.Name, u.Email)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, huma.Error404NotFound(fmt.Sprintf("set %q not found", in.Name))
		}
		return nil, nil
	})

	type setResolveInput struct {
		Name            string `path:"name"`
		IncludeExcluded bool   `query:"include_excluded"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "resolve-set", Method: http.MethodGet, Path: "/api/sets/{name}/resolve",
		Summary: "Resolve a set to exact versions and digests",
	}, func(ctx context.Context, in *setResolveInput) (*struct{ Body Resolution }, error) {
		set, err := load(ctx, in.Name)
		if err != nil {
			return nil, err
		}
		res, err := ResolveAndRecord(ctx, store, set)
		if err != nil {
			return nil, err
		}
		if !in.IncludeExcluded {
			res.Excluded = nil
		}
		return &struct{ Body Resolution }{Body: *res}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-set-resolution", Method: http.MethodGet, Path: "/api/sets/{name}/resolutions/{id}",
		Summary: "Get a stored resolution",
	}, func(ctx context.Context, in *struct {
		Name string `path:"name"`
		ID   string `path:"id"`
	}) (*struct{ Body Resolution }, error) {
		set, err := load(ctx, in.Name)
		if err != nil {
			return nil, err
		}
		res, err := store.GetResolution(ctx, set, in.ID)
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, huma.Error404NotFound("resolution not found")
		}
		return &struct{ Body Resolution }{Body: *res}, nil
	})

	router.Get("/api/sets/{name}/bundle", bundleHandler(store, storage))
}

// ResolveAndRecord resolves a set, stores the resolution and notes the caller.
// Only an API key counts as a caller: a person browsing the UI consumes nothing.
func ResolveAndRecord(ctx context.Context, store *Store, set *Set) (*Resolution, error) {
	res, err := Resolve(ctx, store.pool, set)
	if err != nil {
		return nil, err
	}
	if err := store.SaveResolution(ctx, set, res); err != nil {
		return nil, err
	}
	recordCaller(ctx, store, set, res.ID)
	return res, nil
}

func recordCaller(ctx context.Context, store *Store, set *Set, resolutionID string) {
	user := auth.UserFromContext(ctx)
	if user == nil || auth.KeyFromContext(ctx) == nil {
		return
	}
	// The response must not fail because a usage note did.
	if err := store.RecordCaller(ctx, set, user.ID, user.Name, resolutionID); err != nil {
		log.Warn().Err(err).Str("set", set.Name).Msg("record set caller")
	}
}

func bundleHandler(store *Store, storage platform.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		name := chi.URLParam(r, "name")
		set, err := store.Get(ctx, name)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if checkKeySet(ctx, set) != nil {
			http.Error(w, "this key is limited to another set", http.StatusForbidden)
			return
		}
		if set == nil {
			http.Error(w, "set not found", http.StatusNotFound)
			return
		}

		var res *Resolution
		if id := r.URL.Query().Get("resolved"); id != "" {
			if res, err = store.GetResolution(ctx, set, id); err == nil && res != nil {
				recordCaller(ctx, store, set, res.ID)
			}
		} else {
			res, err = ResolveAndRecord(ctx, store, set)
		}
		if err != nil {
			log.Error().Err(err).Str("set", name).Msg("resolve for bundle")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if res == nil {
			http.Error(w, "resolution not found", http.StatusNotFound)
			return
		}

		paths, err := store.CheckAvailable(ctx, res)
		var gone *GoneError
		if errors.As(err, &gone) {
			http.Error(w, gone.Error(), http.StatusGone)
			return
		}
		if err != nil {
			log.Error().Err(err).Str("set", name).Msg("check bundle")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, name))
		w.Header().Set("X-Skael-Resolution", res.ID)
		w.Header().Set("X-Skael-Complete", fmt.Sprintf("%t", res.Complete))
		if err := WriteBundle(ctx, w, storage, res, paths); err != nil {
			// The status is already sent. Abort so a client sees a broken
			// transfer rather than a short archive that looks complete.
			log.Error().Err(err).Str("set", name).Msg("write bundle")
			panic(http.ErrAbortHandler)
		}
	}
}
