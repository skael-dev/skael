package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/sets"
	"github.com/skael-dev/skael/internal/skill"
)

const defaultSearchLimit = 20

type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`

	scope string
	run   func(ctx context.Context, args json.RawMessage) (any, error)
}

// toolFailure is an outcome the calling model can act on, such as an unknown
// name. It travels in the result with isError set, as the protocol asks.
type toolFailure string

func (f toolFailure) Error() string { return string(f) }

func objectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func newTools(skills *skill.Store, setStore *sets.Store) []tool {
	readOnly := map[string]any{"readOnlyHint": true}
	return []tool{
		{
			Name:        "search_skills",
			Description: "Search the skill registry by text and fuzzy name match. Returns released skill data.",
			InputSchema: objectSchema([]string{"query"}, map[string]any{
				"query": map[string]any{"type": "string", "minLength": 1, "description": "The search text."},
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": defaultSearchLimit},
			}),
			Annotations: readOnly,
			scope:       auth.ScopeSkillsRead,
			run: func(ctx context.Context, args json.RawMessage) (any, error) {
				return searchSkills(ctx, skills, args)
			},
		},
		{
			Name:        "get_skill",
			Description: "Get one skill: the description and content of its latest released version, and metadata of all its versions.",
			InputSchema: objectSchema([]string{"name"}, map[string]any{
				"name": map[string]any{"type": "string", "minLength": 1, "description": "The skill name."},
			}),
			Annotations: readOnly,
			scope:       auth.ScopeSkillsRead,
			run: func(ctx context.Context, args json.RawMessage) (any, error) {
				return getSkill(ctx, skills, args)
			},
		},
		{
			Name:        "list_sets",
			Description: "List the named sets of skills this key can read.",
			InputSchema: objectSchema(nil, map[string]any{}),
			Annotations: readOnly,
			scope:       auth.ScopeSetsRead,
			run: func(ctx context.Context, _ json.RawMessage) (any, error) {
				return listSets(ctx, setStore)
			},
		},
		{
			Name:        "resolve_set",
			Description: "Resolve a set to exact skill versions and digests. An entry that cannot be served makes complete false.",
			InputSchema: objectSchema([]string{"name"}, map[string]any{
				"name":             map[string]any{"type": "string", "minLength": 1, "description": "The set name."},
				"include_excluded": map[string]any{"type": "boolean", "default": false, "description": "List each excluded entry and its reason."},
			}),
			Annotations: readOnly,
			scope:       auth.ScopeSetsRead,
			run: func(ctx context.Context, args json.RawMessage) (any, error) {
				return resolveSet(ctx, setStore, args)
			},
		},
	}
}

func authorize(ctx context.Context, t *tool) error {
	if !auth.Allowed(ctx, t.scope) {
		return &rpcError{codeForbidden, "forbidden: this key lacks " + t.scope}
	}
	if key := auth.KeyFromContext(ctx); key != nil && key.SetID != "" && t.scope != auth.ScopeSetsRead {
		return &rpcError{codeForbidden, "forbidden: this key is limited to one set"}
	}
	return nil
}

func (s *server) callTool(r *http.Request, raw json.RawMessage) (any, error) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := decodeParams(raw, &params); err != nil {
		return nil, err
	}
	var t *tool
	for i := range s.tools {
		if s.tools[i].Name == params.Name {
			t = &s.tools[i]
		}
	}
	if t == nil {
		return nil, &rpcError{codeInvalidParams, "unknown tool: " + params.Name}
	}
	ctx := r.Context()
	if err := authorize(ctx, t); err != nil {
		return nil, err
	}
	out, err := t.run(ctx, params.Arguments)
	var failure toolFailure
	if errors.As(err, &failure) {
		return map[string]any{
			"content": []map[string]any{{"type": "text", "text": failure.Error()}},
			"isError": true,
		}, nil
	}
	if err != nil {
		return nil, err
	}
	text, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal %s result: %w", t.Name, err)
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": out,
	}, nil
}

func searchSkills(ctx context.Context, store *skill.Store, raw json.RawMessage) (any, error) {
	args := struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}{Limit: defaultSearchLimit}
	if err := decodeParams(raw, &args); err != nil {
		return nil, err
	}
	if args.Query == "" {
		return nil, &rpcError{codeInvalidParams, "query is required"}
	}
	if args.Limit < 1 || args.Limit > 100 {
		return nil, &rpcError{codeInvalidParams, "limit must be between 1 and 100"}
	}
	found, err := store.Search(ctx, args.Query, args.Limit)
	if err != nil {
		return nil, err
	}
	if found == nil {
		found = []skill.Skill{}
	}
	return map[string]any{"skills": found}, nil
}

// versionMeta leaves out the frontmatter, scan result and gate decision, which
// carry or quote a held version's text.
type versionMeta struct {
	Version     int       `json:"version"`
	Checksum    string    `json:"checksum"`
	Changelog   string    `json:"changelog,omitempty"`
	GateState   string    `json:"gate_state"`
	HoldReasons []string  `json:"hold_reasons,omitempty"`
	PublishedBy string    `json:"published_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type skillDetail struct {
	skill.Skill
	Versions []versionMeta `json:"versions"`
}

func getSkill(ctx context.Context, store *skill.Store, raw json.RawMessage) (any, error) {
	var args struct {
		Name string `json:"name"`
	}
	if err := decodeParams(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return nil, &rpcError{codeInvalidParams, "name is required"}
	}
	sk, err := store.GetByName(ctx, args.Name)
	if err != nil {
		return nil, err
	}
	if sk == nil {
		return nil, toolFailure(fmt.Sprintf("skill %q not found", args.Name))
	}
	versions, err := store.ListVersions(ctx, args.Name)
	if err != nil {
		return nil, err
	}

	detail := skillDetail{Skill: *sk, Versions: make([]versionMeta, 0, len(versions))}
	detail.Description, detail.Content = "", ""
	for _, v := range versions {
		meta := versionMeta{
			Version: v.Version, Checksum: v.Checksum, GateState: v.GateState,
			HoldReasons: v.HoldReasons, PublishedBy: v.PublishedBy, CreatedAt: v.CreatedAt,
		}
		if v.GateState == "released" {
			meta.Changelog = v.Changelog
			if v.Version == sk.LatestVersion {
				detail.Description, detail.Content = v.Description, v.Content
			}
		}
		detail.Versions = append(detail.Versions, meta)
	}
	return detail, nil
}

func listSets(ctx context.Context, store *sets.Store) (any, error) {
	if key := auth.KeyFromContext(ctx); key != nil && key.SetID != "" {
		set, err := store.GetByID(ctx, key.SetID)
		if err != nil {
			return nil, err
		}
		if sets.CheckKeySet(ctx, set) != nil {
			return nil, &rpcError{codeForbidden, "forbidden: this key is limited to another set"}
		}
		return map[string]any{"sets": []sets.Set{*set}}, nil
	}
	list, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sets": list}, nil
}

func resolveSet(ctx context.Context, store *sets.Store, raw json.RawMessage) (any, error) {
	var args struct {
		Name            string `json:"name"`
		IncludeExcluded bool   `json:"include_excluded"`
	}
	if err := decodeParams(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" {
		return nil, &rpcError{codeInvalidParams, "name is required"}
	}
	set, err := store.Get(ctx, args.Name)
	if err != nil {
		return nil, err
	}
	// The limit comes first, so a set-limited key never learns which sets exist.
	if sets.CheckKeySet(ctx, set) != nil {
		return nil, &rpcError{codeForbidden, "forbidden: this key is limited to another set"}
	}
	if set == nil {
		return nil, toolFailure(fmt.Sprintf("set %q not found", args.Name))
	}
	res, err := sets.ResolveAndRecord(ctx, store, set)
	if err != nil {
		return nil, err
	}
	if !args.IncludeExcluded {
		res.Excluded = nil
	}
	return res, nil
}
