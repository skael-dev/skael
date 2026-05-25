package skill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
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
var validSkillName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// RegisterRoutes wires up all skill-related HTTP endpoints onto the provided
// Huma API and Chi router. The router is needed for the two raw-response
// routes (download + scan) that stream bytes rather than returning JSON.
func RegisterRoutes(api huma.API, router chi.Router, store *Store, storage *platform.Storage) {
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
		Limit  int `query:"limit"  default:"20" minimum:"1" maximum:"100"`
		Offset int `query:"offset" default:"0"  minimum:"0"`
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
		skills, total, err := store.List(ctx, limit, input.Offset)
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
		versions, _ := store.ListVersions(ctx, input.Name)
		for _, v := range versions {
			if v.ArchivePath != "" {
				_ = storage.Delete(v.ArchivePath)
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
	type publishInput struct {
		Name    string `path:"name"`
		RawBody []byte `contentType:"application/gzip,application/octet-stream"`
	}
	type publishBody struct {
		Version
		Created bool `json:"created"`
	}
	type publishOutput struct {
		Body *publishBody
	}
	huma.Register(api, huma.Operation{
		OperationID:   "publish-skill-version",
		Method:        http.MethodPost,
		Path:          "/api/skills/{name}/versions",
		Summary:       "Publish a new skill version",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *publishInput) (*publishOutput, error) {
		// 1. Look up the skill.
		sk, err := store.GetByName(ctx, input.Name)
		if err != nil {
			return nil, fmt.Errorf("publish: lookup skill: %w", err)
		}
		if sk == nil {
			return nil, huma.Error404NotFound(
				fmt.Sprintf("skill %q not found", input.Name))
		}

		// 2. Unpack archive to a temp dir.
		tmpDir, err := os.MkdirTemp("", "skael-publish-*")
		if err != nil {
			return nil, fmt.Errorf("publish: create temp dir: %w", err)
		}
		defer os.RemoveAll(tmpDir)

		if err := Unpack(bytes.NewReader(input.RawBody), tmpDir); err != nil {
			return nil, huma.Error400BadRequest(
				fmt.Sprintf("invalid archive: %s", err))
		}

		// 3. Security scan.
		report, err := scan.ScanDir(tmpDir)
		if err != nil {
			return nil, fmt.Errorf("publish: scan: %w", err)
		}
		if report.Status == "critical" || report.Status == "warn" {
			scanJSON, _ := json.Marshal(report)
			return nil, huma.NewError(
				http.StatusUnprocessableEntity,
				"archive rejected: critical security findings",
				fmt.Errorf("%s", scanJSON),
			)
		}

		// 4. Compute checksum and compare against latest version.
		h := sha256.Sum256(input.RawBody)
		checksum := hex.EncodeToString(h[:])

		if sk.LatestVersion > 0 {
			latest, err := store.GetVersion(ctx, input.Name, sk.LatestVersion)
			if err == nil && latest != nil && latest.Checksum == checksum {
				return &publishOutput{Body: &publishBody{Version: *latest, Created: false}}, nil
			}
		}

		// archiveName is content-addressable: different content → different filename,
		// so concurrent publishes with distinct payloads cannot overwrite each other.
		// storage.Write stores it relative to BasePath, and storage.Read reads it
		// the same way.
		archiveName := fmt.Sprintf("%s/%s.tar.gz", input.Name, checksum[:16])
		if _, err := storage.Write(archiveName, bytes.NewReader(input.RawBody)); err != nil {
			return nil, fmt.Errorf("publish: store archive: %w", err)
		}

		// 5. Read SKILL.md and extract frontmatter.
		skillMDPath := filepath.Join(tmpDir, "SKILL.md")
		skillMDBytes, err := os.ReadFile(skillMDPath)
		if err != nil {
			return nil, huma.Error400BadRequest("archive must contain SKILL.md")
		}
		fm, body, err := ParseFrontmatter(string(skillMDBytes))
		if err != nil {
			return nil, fmt.Errorf("publish: parse frontmatter: %w", err)
		}

		var fmJSON json.RawMessage
		if fm != nil {
			fmJSON, err = json.Marshal(fm)
			if err != nil {
				return nil, fmt.Errorf("publish: marshal frontmatter: %w", err)
			}
		} else {
			fmJSON = json.RawMessage(`{}`)
		}

		// Extract description from frontmatter.
		description := sk.Description
		if fm != nil {
			if d, ok := fm["description"].(string); ok && d != "" {
				description = d
			}
		}

		// Extract changelog from frontmatter.
		changelog := ""
		if fm != nil {
			if c, ok := fm["changelog"].(string); ok {
				changelog = c
			}
		}

		// Build manifest from the unpacked directory.
		var manifest []FileEntry
		if err := filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, err := filepath.Rel(tmpDir, path)
			if err != nil {
				return err
			}
			manifest = append(manifest, FileEntry{
				Path: filepath.ToSlash(rel),
				Size: info.Size(),
			})
			return nil
		}); err != nil {
			return nil, fmt.Errorf("publish: build manifest: %w", err)
		}

		// 6. Marshal scan result.
		scanJSON, err := json.Marshal(report)
		if err != nil {
			return nil, fmt.Errorf("publish: marshal scan result: %w", err)
		}

		// 7. Create version record. Store the relative archiveName so that
		// storage.Read can locate the file without needing the absolute basePath.
		ver, err := store.CreateVersion(ctx,
			sk.ID,
			archiveName,
			checksum,
			changelog,
			fmJSON,
			manifest,
			scanJSON,
		)
		if err != nil {
			_ = storage.Delete(archiveName)
			return nil, huma.Error500InternalServerError("creating version", err)
		}

		// 8. Update skill content and description.
		// Non-fatal: the version is already committed. A stale metadata entry
		// will be corrected on the next successful publish.
		if err := store.UpdateContent(ctx, input.Name, description, body, fmJSON); err != nil {
			log.Warn().Str("skill", input.Name).Err(err).Msg("publish: update skill metadata (non-fatal)")
		}

		return &publishOutput{Body: &publishBody{Version: *ver, Created: true}}, nil
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
	// Raw routes registered directly on the Chi router (streaming responses).
	// -----------------------------------------------------------------
	if router != nil {
		// GET /api/skills/{name}/versions/{version}/download
		router.Get("/api/skills/{name}/versions/{version}/download",
			makeDownloadHandler(store, storage))

		// GET /api/skills/{name}/scan — scan results for the latest version
		router.Get("/api/skills/{name}/scan", makeLatestScanHandler(store))
	}
}

// makeDownloadHandler returns a handler that streams the archive for a specific
// version of a skill.
func makeDownloadHandler(store *Store, storage *platform.Storage) http.HandlerFunc {
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

		rc, err := storage.Read(ver.ArchivePath)
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

