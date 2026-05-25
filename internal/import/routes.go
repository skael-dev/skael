package skillimport

import (
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
)

func RegisterRoutes(api huma.API, router chi.Router, importStore *Store, skillStore *skill.Store, storage *platform.Storage, fetcher *Fetcher) {
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
		Name       string `json:"name"`
		Version    int    `json:"version"`
		ScanStatus string `json:"scan_status"`
		Created    bool   `json:"created"`
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

			ver, created, err := importSingleSkill(ctx, result.Dir, ds, src, skillStore, importStore, storage)
			if err != nil {
				log.Warn().Err(err).Str("skill", ds.Name).Msg("import failed")
				out.Body.Failed = append(out.Body.Failed, failedSkill{Name: ds.Name, Error: err.Error()})
				continue
			}

			// Auto-create reverse alias if namespace was applied.
			if input.Body.Namespace != "" {
				skillStore.CreateAlias(ctx, originalName, ds.Name)
			}

			out.Body.Imported = append(out.Body.Imported, importedSkill{
				Name:       ds.Name,
				Version:    ver.Version,
				ScanStatus: ds.ScanStatus,
				Created:    created,
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
	router.Post("/api/import/upload", makeUploadHandler(skillStore, importStore, storage))
}

func importSingleSkill(
	ctx context.Context,
	rootDir string,
	ds DiscoveredSkill,
	src Source,
	skillStore *skill.Store,
	importStore *Store,
	storage *platform.Storage,
) (*skill.Version, bool, error) {
	skillDir := filepath.Join(rootDir, filepath.FromSlash(ds.Path))

	archive, checksum, manifest, err := skill.Pack(skillDir)
	if err != nil {
		return nil, false, fmt.Errorf("pack: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return nil, false, fmt.Errorf("read SKILL.md: %w", err)
	}
	fm, body, err := skill.ParseFrontmatter(string(data))
	if err != nil {
		return nil, false, fmt.Errorf("parse frontmatter: %w", err)
	}

	var fmJSON json.RawMessage
	if fm != nil {
		fmJSON, _ = json.Marshal(fm)
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
		return nil, false, fmt.Errorf("scan: %w", err)
	}
	scanJSON, _ := json.Marshal(report)

	sk, err := skillStore.GetByName(ctx, ds.Name)
	if err != nil {
		return nil, false, fmt.Errorf("get skill: %w", err)
	}
	if sk == nil {
		sk, err = skillStore.Create(ctx, ds.Name, "", description, body, fmJSON)
		if err != nil {
			return nil, false, fmt.Errorf("create skill: %w", err)
		}
	}

	if sk.LatestVersion > 0 {
		latest, err := skillStore.GetVersion(ctx, ds.Name, sk.LatestVersion)
		if err == nil && latest != nil && latest.Checksum == checksum {
			return latest, false, nil
		}
	}

	archiveName := fmt.Sprintf("%s/%s.tar.gz", ds.Name, checksum[:16])
	if _, err := storage.Write(archiveName, bytes.NewReader(archive)); err != nil {
		return nil, false, fmt.Errorf("store archive: %w", err)
	}

	ver, err := skillStore.CreateVersion(ctx, sk.ID, archiveName, checksum, changelog, fmJSON, manifest, scanJSON)
	if err != nil {
		_ = storage.Delete(archiveName)
		return nil, false, fmt.Errorf("create version: %w", err)
	}

	// Update skill metadata (non-fatal, same as publish).
	// Note: UpdateContent takes skill name, not ID.
	if err := skillStore.UpdateContent(ctx, ds.Name, description, body, fmJSON); err != nil {
		log.Warn().Err(err).Str("skill", ds.Name).Msg("import: update content failed (non-fatal)")
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

	return ver, true, nil
}

func makeUploadHandler(skillStore *skill.Store, importStore *Store, storage *platform.Storage) http.HandlerFunc {
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
			Name       string `json:"name"`
			Version    int    `json:"version"`
			ScanStatus string `json:"scan_status"`
			Created    bool   `json:"created"`
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
			ver, created, err := importSingleSkill(r.Context(), tmpDir, ds, src, skillStore, importStore, storage)
			if err != nil {
				resp.Failed = append(resp.Failed, failedSkill{Name: ds.Name, Error: err.Error()})
				continue
			}
			resp.Imported = append(resp.Imported, importedSkill{Name: ds.Name, Version: ver.Version, ScanStatus: ds.ScanStatus, Created: created})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(resp)
	}
}
