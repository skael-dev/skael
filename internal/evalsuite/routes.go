package evalsuite

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

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
	Skill         string       `json:"skill" minLength:"1"`
	SpecVersion   int          `json:"spec_version"`
	Checks        []suiteCheck `json:"checks"`
	ArchiveBase64 string       `json:"archive_base64" minLength:"1"`
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
func RegisterRoutes(api huma.API, router chi.Router, reg *Registry, skills *skill.Store) {
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

		rec, err := reg.Put(ctx, input.Body.Skill, archive, checks, input.Body.SpecVersion, uploadedBy)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("upload eval suite: " + err.Error())
		}

		return &suiteOutput{
			Status: http.StatusCreated,
			Body: suiteOutputBody{
				Ref:       rec.Ref,
				TaskCount: rec.TaskCount,
			},
		}, nil
	})

	if router != nil {
		router.Get("/api/eval/suites/{ref}", makeDownloadHandler(reg))
	}
}

// makeDownloadHandler returns a handler that streams the archive for a suite
// ref.
func makeDownloadHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ref := chi.URLParam(r, "ref")

		archive, err := reg.Fetch(r.Context(), ref)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, ref))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(archive)
	}
}
