package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/runs"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/submissions"
)

// The request body is limited to 10 MB, but that still allows too many empty
// files to create on disk.
const maxSubmitFiles = 1000

func reportingTools(runStore *runs.Store, subs *submissions.Service) []tool {
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return []tool{
		{
			Name:        "report_outcome",
			Description: "Report how an automated run ended. Needs a service account key. A second report for one run replaces the first. An outcome never changes a headline and never releases a version.",
			InputSchema: objectSchema([]string{"external_run_id", "outcome"}, map[string]any{
				"external_run_id": text("Your id for the run."),
				"outcome":         map[string]any{"type": "string", "enum": []string{"succeeded", "failed", "cancelled"}},
				"repairs":         map[string]any{"type": "integer", "minimum": 0, "description": "How many repairs the run needed."},
				"review_verdict":  text("The verdict of the review of the run."),
				"evidence_url":    text("An http or https link to the evidence."),
				"set":             text("The set the run used."),
				"resolution_id":   text("The resolution the run used."),
			}),
			Annotations: map[string]any{},
			scope:       auth.ScopeOutcomesWrite,
			run: func(ctx context.Context, args json.RawMessage) (any, error) {
				var body runs.RunOutcomeBody
				if err := decodeParams(args, &body); err != nil {
					return nil, err
				}
				rec, err := runStore.Report(ctx, body)
				if err != nil {
					return nil, statusFailure(err)
				}
				return rec, nil
			},
		},
		{
			Name:        "submit_skill",
			Description: "Send a whole skill for human review. This publishes nothing: a person approves or rejects it. Give either archive_base64 or files, and files must include SKILL.md. Read the decision with get_submission.",
			InputSchema: objectSchema([]string{"name", "rationale"}, map[string]any{
				"name":            text("The skill name."),
				"rationale":       text("Why this skill or this change is needed."),
				"run_link":        text("An http or https link to the run that produced it."),
				"external_run_id": text("Your id for the run that produced it."),
				"archive_base64":  text("A gzipped tar of the whole skill, in base64."),
				"files": map[string]any{
					"type":        "array",
					"description": "The whole skill as text files. Each path is relative to the skill directory.",
					"items": objectSchema([]string{"path", "content"}, map[string]any{
						"path":    text("A relative path with forward slashes, such as SKILL.md."),
						"content": text("The text of the file."),
					}),
				},
			}),
			Annotations: map[string]any{},
			scope:       auth.ScopeSubmissionsWrite,
			run: func(ctx context.Context, args json.RawMessage) (any, error) {
				return submitSkill(ctx, subs, args)
			},
		},
		{
			Name:        "get_submission",
			Description: "Read the state of a submission and the reason a person gave for the decision.",
			InputSchema: objectSchema([]string{"id"}, map[string]any{"id": text("The submission id.")}),
			Annotations: map[string]any{},
			scope:       auth.ScopeSubmissionsWrite,
			run: func(ctx context.Context, args json.RawMessage) (any, error) {
				return getSubmission(ctx, subs, args)
			},
		},
	}
}

// statusFailure turns a service's client error into a result the calling model
// can act on. The services speak HTTP status errors, and a server error stays
// an internal error.
func statusFailure(err error) error {
	var status huma.StatusError
	if !errors.As(err, &status) || status.GetStatus() >= http.StatusInternalServerError {
		return err
	}
	msg := err.Error()
	var model *huma.ErrorModel
	if errors.As(err, &model) {
		for _, detail := range model.Errors {
			msg += ": " + detail.Message
		}
	}
	return toolFailure(msg)
}

type submitFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func submitSkill(ctx context.Context, subs *submissions.Service, raw json.RawMessage) (any, error) {
	var args struct {
		submissions.SubmissionCreateBody
		Files []submitFile `json:"files"`
	}
	if err := decodeParams(raw, &args); err != nil {
		return nil, err
	}
	if args.Name == "" || args.Rationale == "" {
		return nil, &rpcError{codeInvalidParams, "name and rationale are required"}
	}
	if (args.ArchiveBase64 == "") == (len(args.Files) == 0) {
		return nil, &rpcError{codeInvalidParams, "give either archive_base64 or files"}
	}
	if len(args.Files) > 0 {
		archive, err := packFiles(args.Files)
		if err != nil {
			return nil, err
		}
		args.ArchiveBase64 = archive
	}
	sub, err := subs.Submit(ctx, args.SubmissionCreateBody)
	if err != nil {
		return nil, statusFailure(err)
	}
	return sub, nil
}

// packFiles writes the agent's text to a temporary directory and packs it, so
// the archive matches what the CLI sends. A path comes from the caller and must
// stay inside that directory.
func packFiles(files []submitFile) (string, error) {
	if len(files) > maxSubmitFiles {
		return "", &rpcError{codeInvalidParams, fmt.Sprintf("files holds at most %d entries", maxSubmitFiles)}
	}
	dir, err := os.MkdirTemp("", "skael-mcp-submission-*")
	if err != nil {
		return "", fmt.Errorf("submit_skill: %w", err)
	}
	defer os.RemoveAll(dir)

	seen := make(map[string]bool, len(files))
	for _, f := range files {
		if !validFilePath(f.Path) {
			return "", &rpcError{codeInvalidParams, fmt.Sprintf("invalid file path %q: use a relative path with forward slashes", f.Path)}
		}
		if seen[f.Path] {
			return "", &rpcError{codeInvalidParams, fmt.Sprintf("duplicate file path %q", f.Path)}
		}
		seen[f.Path] = true
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", toolFailure(fmt.Sprintf("cannot write file %q: another file uses part of its path", f.Path))
		}
		if err := os.WriteFile(target, []byte(f.Content), 0o644); err != nil {
			return "", toolFailure(fmt.Sprintf("cannot write file %q: another file uses part of its path", f.Path))
		}
	}
	if !seen["SKILL.md"] {
		return "", &rpcError{codeInvalidParams, "files must include SKILL.md"}
	}
	archive, _, _, err := skill.Pack(dir)
	if err != nil {
		return "", fmt.Errorf("submit_skill: pack: %w", err)
	}
	return base64.StdEncoding.EncodeToString(archive), nil
}

func validFilePath(p string) bool {
	return p != "." && !strings.ContainsAny(p, "\\\x00") && path.Clean(p) == p && filepath.IsLocal(filepath.FromSlash(p))
}

// submissionDecision leaves out the scan result and manifest. The caller wants
// the decision here.
type submissionDecision struct {
	ID        string     `json:"id"`
	SkillName string     `json:"skill_name"`
	State     string     `json:"state"`
	Reason    string     `json:"reason"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	Version   *int       `json:"version,omitempty"`
}

func getSubmission(ctx context.Context, subs *submissions.Service, raw json.RawMessage) (any, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := decodeParams(raw, &args); err != nil {
		return nil, err
	}
	if args.ID == "" {
		return nil, &rpcError{codeInvalidParams, "id is required"}
	}
	sub, err := subs.Read(ctx, args.ID)
	if err != nil {
		return nil, statusFailure(err)
	}
	return submissionDecision{
		ID: sub.ID, SkillName: sub.SkillName, State: sub.State, Reason: sub.Reason,
		DecidedAt: sub.DecidedAt, Version: sub.Version,
	}, nil
}
