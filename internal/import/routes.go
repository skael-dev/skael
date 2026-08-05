package skillimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"

	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
)

// RouteOptions carries the optional collaborators RegisterRoutes wires into
// the import handlers. Each is independently optional (nil disables the
// behavior it powers).
type RouteOptions struct {
	// External is the opt-in external scanner (Phase 2); nil disables it.
	External *scan.ExternalScanner
	// Queue, when set together with Suites, lets an import enqueue an
	// evaluation for a skill that has a registered suite. A queue outage
	// must never fail an import — mirrors the publish path.
	Queue evalqueue.Executor
	// Suites looks up the latest registered eval suite for a skill by name.
	Suites *evalsuite.Registry
	// QualityFloor is the minimum headline quality score a verified
	// evaluation must reach to clear a held version. Mirrors
	// skill.RouteOptions.QualityFloor; the zero value means any verified,
	// complete, contract-clean report clears.
	QualityFloor float64
}

// importQualityState mirrors the unexported type of the same name declared inside
// skill.RegisterRoutes's publish handler — same JSON shape, so the two
// responses read identically to a client, but declared separately since that
// one is a route-local type unreachable from this package.
type importQualityState struct {
	State string `json:"state,omitempty"`
	JobID string `json:"job_id,omitempty"`
}

func RegisterRoutes(api huma.API, router chi.Router, importStore *Store, skillStore *skill.Store, storage platform.Storage, fetcher *Fetcher, opts RouteOptions) {
	external := opts.External

	// Rate limit: 10 requests per minute for the resolve endpoint.
	resolveLimiter := rate.NewLimiter(rate.Every(time.Minute/10), 1)

	// POST /api/import/resolve — preview skills from a URL
	type resolveBody struct {
		URL string `json:"url" minLength:"1"`
	}
	type resolveInput struct {
		Body resolveBody
	}
	type resolveOutput struct {
		Body struct {
			Source     Source            `json:"source"`
			Skills     []DiscoveredSkill `json:"skills"`
			PluginName string            `json:"plugin_name,omitempty"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "import-resolve",
		Method:      http.MethodPost,
		Path:        "/api/import/resolve",
		Summary:     "Preview skills available for import from a URL",
	}, func(ctx context.Context, input *resolveInput) (*resolveOutput, error) {
		if !resolveLimiter.Allow() {
			return nil, huma.Error429TooManyRequests("import resolve rate limited (max 10/min)")
		}

		src, err := ResolveURL(input.Body.URL)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("invalid URL: %v", err))
		}

		result, err := fetcher.Fetch(src)
		if err != nil {
			return nil, huma.Error502BadGateway(fmt.Sprintf("fetch failed: %v", err))
		}
		defer os.RemoveAll(result.Dir)

		src.CommitSHA = result.CommitSHA

		skills, err := Discover(result.Dir, src.Path)
		if err != nil {
			return nil, fmt.Errorf("discover: %w", err)
		}

		for i := range skills {
			existing, err := skillStore.GetByName(ctx, skills[i].Name)
			if err == nil && existing != nil {
				skills[i].ExistingVersion = existing.LatestVersion
			}
		}

		pluginName := DetectPluginName(result.Dir)

		out := &resolveOutput{}
		out.Body.Source = src
		out.Body.Skills = skills
		out.Body.PluginName = pluginName
		if out.Body.Skills == nil {
			out.Body.Skills = []DiscoveredSkill{}
		}
		return out, nil
	})

	// POST /api/import — execute import for selected skills
	type importBody struct {
		Source    Source   `json:"source"`
		Skills    []string `json:"skills" minItems:"1"`
		Namespace string   `json:"namespace,omitempty"`
	}
	type importInput struct {
		Body importBody
	}
	type importedSkill struct {
		Name       string             `json:"name"`
		Version    int                `json:"version"`
		ScanStatus string             `json:"scan_status"`
		Created    bool               `json:"created"`
		Quality    importQualityState `json:"quality"`
		Decision   gate.Decision      `json:"decision"`
	}
	type failedSkill struct {
		Name  string `json:"name"`
		Error string `json:"error"`
	}
	type importOutput struct {
		Body struct {
			Imported []importedSkill `json:"imported"`
			Failed   []failedSkill   `json:"failed"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID:   "import-skills",
		Method:        http.MethodPost,
		Path:          "/api/import",
		Summary:       "Import selected skills from a resolved source",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *importInput) (*importOutput, error) {
		src := input.Body.Source

		result, err := fetcher.Fetch(src)
		if err != nil {
			return nil, huma.Error502BadGateway(fmt.Sprintf("fetch failed: %v", err))
		}
		defer os.RemoveAll(result.Dir)

		if src.CommitSHA == "" {
			src.CommitSHA = result.CommitSHA
		}

		discovered, err := Discover(result.Dir, src.Path)
		if err != nil {
			return nil, fmt.Errorf("discover: %w", err)
		}

		selected := map[string]bool{}
		for _, name := range input.Body.Skills {
			selected[name] = true
		}

		out := &importOutput{}
		out.Body.Imported = []importedSkill{}
		out.Body.Failed = []failedSkill{}

		for _, ds := range discovered {
			if !selected[ds.Name] {
				continue
			}

			originalName := ds.Name
			if input.Body.Namespace != "" {
				ds.Name = input.Body.Namespace + ":" + ds.Name
			}

			ver, created, quality, decision, err := importSingleSkill(ctx, result.Dir, ds, src, skillStore, importStore, storage, external, opts.Queue, opts.Suites, opts.QualityFloor)
			if err != nil {
				log.Warn().Err(err).Str("skill", ds.Name).Msg("import failed")
				out.Body.Failed = append(out.Body.Failed, failedSkill{Name: ds.Name, Error: err.Error()})
				continue
			}

			// Auto-create reverse alias if namespace was applied.
			if input.Body.Namespace != "" {
				if err := skillStore.CreateAlias(ctx, originalName, ds.Name); err != nil {
					log.Warn().Err(err).Str("skill", ds.Name).Msg("import: create reverse alias failed (non-fatal)")
				}
			}

			out.Body.Imported = append(out.Body.Imported, importedSkill{
				Name:       ds.Name,
				Version:    ver.Version,
				ScanStatus: ds.ScanStatus,
				Created:    created,
				Quality:    quality,
				Decision:   decision,
			})
		}

		return out, nil
	})

	// GET /api/import/sources — list all import provenance
	type sourcesOutput struct {
		Body []ImportSource
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-import-sources",
		Method:      http.MethodGet,
		Path:        "/api/import/sources",
		Summary:     "List all imported skills with source provenance",
	}, func(ctx context.Context, input *struct{}) (*sourcesOutput, error) {
		sources, err := importStore.ListAll(ctx)
		if err != nil {
			return nil, fmt.Errorf("list sources: %w", err)
		}
		return &sourcesOutput{Body: sources}, nil
	})

	// GET /api/skills/{name}/source — get import provenance for a single skill
	type skillSourceInput struct {
		Name string `path:"name"`
	}
	type skillSourceOutput struct {
		Body *ImportSource
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-skill-import-source",
		Method:      http.MethodGet,
		Path:        "/api/skills/{name}/source",
		Summary:     "Get import source for a skill",
	}, func(ctx context.Context, input *skillSourceInput) (*skillSourceOutput, error) {
		src, err := importStore.GetBySkillName(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("get skill source: %w", err)
		}
		if src == nil {
			return &skillSourceOutput{Body: nil}, nil
		}
		return &skillSourceOutput{Body: src}, nil
	})

	// POST /api/import/upload — local upload for CLI
	router.Post("/api/import/upload", makeUploadHandler(skillStore, importStore, storage, external, opts.Queue, opts.Suites, opts.QualityFloor))
}

func importSingleSkill(
	ctx context.Context,
	rootDir string,
	ds DiscoveredSkill,
	src Source,
	skillStore *skill.Store,
	importStore *Store,
	storage platform.Storage,
	external *scan.ExternalScanner,
	queue evalqueue.Executor,
	suites *evalsuite.Registry,
	qualityFloor float64,
) (*skill.Version, bool, importQualityState, gate.Decision, error) {
	skillDir := filepath.Join(rootDir, filepath.FromSlash(ds.Path))

	archive, checksum, manifest, err := skill.Pack(skillDir)
	if err != nil {
		return nil, false, importQualityState{}, gate.Decision{}, fmt.Errorf("pack: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return nil, false, importQualityState{}, gate.Decision{}, fmt.Errorf("read SKILL.md: %w", err)
	}
	fm, body, err := skill.ParseFrontmatter(string(data))
	if err != nil {
		return nil, false, importQualityState{}, gate.Decision{}, fmt.Errorf("parse frontmatter: %w", err)
	}

	var fmJSON json.RawMessage
	if fm != nil {
		var err error
		fmJSON, err = json.Marshal(fm)
		if err != nil {
			return nil, false, importQualityState{}, gate.Decision{}, fmt.Errorf("import: marshal frontmatter: %w", err)
		}
	} else {
		fmJSON = json.RawMessage(`{}`)
	}

	description := ds.Description
	changelog := ""
	if fm != nil {
		if c, ok := fm["changelog"].(string); ok {
			changelog = c
		}
	}

	report, err := scan.ScanDir(skillDir)
	if err != nil {
		return nil, false, importQualityState{}, gate.Decision{}, fmt.Errorf("scan: %w", err)
	}
	scan.MergeExternal(ctx, external, skillDir, report)
	// skillDir lives under a throwaway checkout; findings are persisted and
	// rendered, so they must name the file inside the bundle, not on disk.
	scan.Relativize(report, skillDir)
	scanJSON, err := json.Marshal(report)
	if err != nil {
		return nil, false, importQualityState{}, gate.Decision{}, fmt.Errorf("import: marshal scan result: %w", err)
	}

	// Import has no interactive override — unlike publish, there is no
	// ?override=true equivalent, and this must not gain one here. Imports
	// are the less trusted path, not the more: an imported skill faces
	// exactly the decision a published one does.
	decision := skill.DecidePublish(report, gate.OwnerState{}, qualityFloor, false)

	if decision.Outcome == gate.Block {
		return nil, false, importQualityState{}, decision, fmt.Errorf(
			"archive rejected: it contains credential-theft or data-exfiltration findings, which are unappealable — no evaluation and no override clears them")
	}

	if decision.Outcome == gate.NeedsReview {
		log.Info().
			Str("skill", ds.Name).
			Int("reasons", len(decision.Reasons)).
			Msg("import held for review: version created but not served until an evaluation or an admin clears it")
	}

	sk, err := skillStore.GetByName(ctx, ds.Name)
	if err != nil {
		return nil, false, importQualityState{}, decision, fmt.Errorf("get skill: %w", err)
	}
	if sk == nil {
		// A first version that the gate is holding must create the skill row
		// empty. Create writes description/content/frontmatter unconditionally,
		// which routes straight around the CASE WHEN in CreateVersion that
		// stops a held publish from serving its own prose: without this, the
		// full held body — cradle and all — is served by GET /api/skills/{name}
		// while the archive is withheld. ReleaseVersion backfills all three
		// from the version row if the hold is ever cleared.
		createDesc, createBody := description, body
		createFM := fmJSON
		if decision.Held() {
			createDesc, createBody, createFM = "", "", json.RawMessage(`{}`)
		}
		sk, err = skillStore.Create(ctx, ds.Name, "", createDesc, createBody, createFM)
		if err != nil {
			return nil, false, importQualityState{}, decision, fmt.Errorf("create skill: %w", err)
		}
	}

	if sk.LatestVersion > 0 {
		latest, err := skillStore.GetVersion(ctx, ds.Name, sk.LatestVersion)
		if err == nil && latest != nil && latest.Checksum == checksum {
			return latest, false, importQualityState{State: "none"}, decision, nil
		}
	}

	archiveName := fmt.Sprintf("%s/%s.tar.gz", ds.Name, checksum[:16])
	if _, err := storage.Write(ctx, archiveName, bytes.NewReader(archive)); err != nil {
		return nil, false, importQualityState{}, decision, fmt.Errorf("store archive: %w", err)
	}

	ver, err := skillStore.CreateVersion(ctx, sk.ID, archiveName, checksum, changelog,
		description, body, fmJSON, manifest, scanJSON, "import", decision)
	if err != nil {
		_ = storage.Delete(ctx, archiveName)
		return nil, false, importQualityState{}, decision, fmt.Errorf("create version: %w", err)
	}

	// Persist spec-derived metadata. UpdateSpecFields is the only writer of the
	// tags, author, license, compatibility and spec_compliance columns, and the
	// dashboard's tag filter and tag list endpoint read tags directly — an
	// imported skill that skips this is invisible to both. But only for a
	// version that is actually being served: a held version must not publish
	// its own metadata onto the skill row while the gate is withholding its
	// archive, mirroring publish's step 9.
	if !decision.Held() {
		spec := skill.ValidateSpec(fm, sk.Name)
		if err := skillStore.UpdateSpecFields(ctx, sk.Name,
			spec.Author, spec.License, spec.Compat, spec.Compliance, spec.DisplayName, spec.Tags,
		); err != nil {
			log.Warn().Err(err).Str("skill", ds.Name).Msg("import: persist spec metadata failed (non-fatal)")
		}
	}

	// Enqueue an evaluation when a suite exists for this skill, mirroring
	// publish's step 10. A queue outage must not fail an import: the version
	// is already durable by this point. This runs regardless of decision.Held():
	// the evaluation is what could clear a held version.
	quality := importQualityState{State: "none"}
	if queue != nil && suites != nil {
		rec, err := suites.LatestForSkill(ctx, ds.Name)
		if err != nil && !errors.Is(err, evalsuite.ErrNotFound) {
			// LatestForSkill reports "no suite registered" as an error
			// wrapping ErrNotFound — anything else is an infrastructure
			// failure (lookup, not absence) and must not be silently
			// swallowed as "this skill has no suite".
			log.Warn().Err(err).Str("skill", ds.Name).Msg("import: suite lookup failed, skipping enqueue")
		} else if rec != nil {
			id, err := queue.Submit(ctx, evalqueue.Job{
				SkillID: sk.ID, SkillName: ds.Name, Version: ver.Version,
				SuiteRef: rec.Ref, Tier: "full", RequestedBy: "import",
			})
			if err != nil {
				log.Warn().Err(err).Str("skill", ds.Name).Msg("import: could not enqueue evaluation")
			} else {
				quality = importQualityState{State: "pending", JobID: string(id)}
			}
		}
	}

	// Record provenance.
	if err := importStore.Upsert(ctx, ImportSource{
		SkillID:    sk.ID,
		SourceType: src.Type,
		SourceURL:  fmt.Sprintf("https://github.com/%s/%s", src.Owner, src.Repo),
		SourcePath: ds.Path,
		SourceRef:  src.Ref,
		CommitSHA:  src.CommitSHA,
	}); err != nil {
		log.Warn().Err(err).Str("skill", ds.Name).Msg("import: record provenance failed (non-fatal)")
	}

	return ver, true, quality, decision, nil
}

func makeUploadHandler(skillStore *skill.Store, importStore *Store, storage platform.Storage, external *scan.ExternalScanner, queue evalqueue.Executor, suites *evalsuite.Registry, qualityFloor float64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 50<<20))
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}

		tmpDir, err := os.MkdirTemp("", "skael-upload-*")
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		defer os.RemoveAll(tmpDir)

		if err := skill.Unpack(bytes.NewReader(body), tmpDir); err != nil {
			http.Error(w, "invalid archive: "+err.Error(), http.StatusBadRequest)
			return
		}

		discovered, err := Discover(tmpDir, "")
		if err != nil {
			http.Error(w, "discover: "+err.Error(), http.StatusInternalServerError)
			return
		}

		src := Source{Type: "upload"}

		type importedSkill struct {
			Name       string             `json:"name"`
			Version    int                `json:"version"`
			ScanStatus string             `json:"scan_status"`
			Created    bool               `json:"created"`
			Quality    importQualityState `json:"quality"`
			Decision   gate.Decision      `json:"decision"`
		}
		type failedSkill struct {
			Name  string `json:"name"`
			Error string `json:"error"`
		}
		type response struct {
			Imported []importedSkill `json:"imported"`
			Failed   []failedSkill   `json:"failed"`
		}

		resp := response{
			Imported: []importedSkill{},
			Failed:   []failedSkill{},
		}

		for _, ds := range discovered {
			ver, created, quality, decision, err := importSingleSkill(r.Context(), tmpDir, ds, src, skillStore, importStore, storage, external, queue, suites, qualityFloor)
			if err != nil {
				resp.Failed = append(resp.Failed, failedSkill{Name: ds.Name, Error: err.Error()})
				continue
			}
			resp.Imported = append(resp.Imported, importedSkill{
				Name: ds.Name, Version: ver.Version, ScanStatus: ds.ScanStatus, Created: created,
				Quality: quality, Decision: decision,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Warn().Err(err).Msg("import upload: encode response failed (headers already sent)")
		}
	}
}
