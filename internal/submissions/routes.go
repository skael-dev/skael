package submissions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/runs"
	"github.com/skael-dev/skael/internal/skill"
)

var skillNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9:.-]*[a-z0-9])?$`)

// SubmissionCreateBody is the request of POST /api/submissions and MCP submit_skill.
type SubmissionCreateBody struct {
	Name          string `json:"name" minLength:"1" maxLength:"128"`
	Rationale     string `json:"rationale" minLength:"1" maxLength:"4000"`
	RunLink       string `json:"run_link,omitempty" maxLength:"2048"`
	ExternalRunID string `json:"external_run_id,omitempty" maxLength:"255"`
	ArchiveBase64 string `json:"archive_base64" minLength:"1" doc:"A gzipped tar of the whole skill."`
}

// Service is the submission workflow, shared by HTTP and MCP.
type Service struct {
	store     *Store
	skills    *skill.Store
	storage   platform.Storage
	publisher *skill.Publisher
	owners    skill.OwnerResolver
}

// NewService constructs a Service.
func NewService(store *Store, skills *skill.Store, storage platform.Storage, publisher *skill.Publisher, owners skill.OwnerResolver) *Service {
	return &Service{store: store, skills: skills, storage: storage, publisher: publisher, owners: owners}
}

// Submit scans and stores an archive. An exfiltration or secret finding returns
// 422 and stores nothing. Other findings go to the reviewer.
func (s *Service) Submit(ctx context.Context, body SubmissionCreateBody) (*Submission, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil, huma.Error401Unauthorized("not authenticated")
	}
	if !skillNameRe.MatchString(body.Name) {
		return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("invalid skill name %q", body.Name))
	}
	// MCP has no request schema, so the limits of the HTTP schema repeat here.
	if len(body.Name) > 128 || body.Rationale == "" || len(body.Rationale) > 4000 || len(body.RunLink) > 2048 || len(body.ExternalRunID) > 255 {
		return nil, huma.Error422UnprocessableEntity("rationale is required, and a field is too long")
	}
	if !runs.ValidHTTPURL(body.RunLink) {
		return nil, huma.Error422UnprocessableEntity("run_link must be an http or https URL")
	}
	archive, err := base64.StdEncoding.DecodeString(body.ArchiveBase64)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("archive_base64 is not valid base64")
	}
	report, decision, err := s.publisher.ScanArchive(ctx, archive)
	if err != nil {
		return nil, err
	}
	if decision.Outcome == gate.Block {
		payload, _ := json.Marshal(struct {
			Scan     any           `json:"scan"`
			Decision gate.Decision `json:"decision"`
		}{report, decision})
		return nil, huma.NewError(http.StatusUnprocessableEntity,
			"archive rejected: it contains credential-theft or data-exfiltration findings, which are unappealable",
			fmt.Errorf("%s", payload))
	}
	scanJSON, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("submit: %w", err)
	}
	manifest, err := archiveManifest(archive)
	if err != nil {
		return nil, huma.Error400BadRequest(fmt.Sprintf("invalid archive: %s", err))
	}

	sum := sha256.Sum256(archive)
	checksum := hex.EncodeToString(sum[:])
	// The leading dot cannot begin a skill name, so this never collides with a
	// skill's own archives.
	archivePath := fmt.Sprintf(".submissions/%s.tar.gz", checksum[:32])
	if _, err := s.storage.Write(ctx, archivePath, bytes.NewReader(archive)); err != nil {
		return nil, fmt.Errorf("submit: store archive: %w", err)
	}
	return s.store.Create(ctx, &Submission{
		SkillName: body.Name, Consumer: u.Name, SubmittedBy: u.Email, Rationale: body.Rationale,
		RunLink: body.RunLink, ExternalRunID: body.ExternalRunID,
		ArchivePath: archivePath, Checksum: checksum, FileManifest: manifest, ScanResult: scanJSON,
	})
}

// Read returns a submission for its reader. A person reads any submission. A
// restricted key reads only what its own consumer sent.
func (s *Service) Read(ctx context.Context, id string) (*Submission, error) {
	if uuid.Validate(id) != nil {
		return nil, huma.Error404NotFound("submission not found")
	}
	sub, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	u := auth.UserFromContext(ctx)
	if sub == nil || (auth.KeyFromContext(ctx).Restricted() && (u == nil || sub.Consumer != u.Name)) {
		return nil, huma.Error404NotFound("submission not found")
	}
	return sub, nil
}

// SubmissionEdit replaces the text of one file before approval.
type SubmissionEdit struct {
	Path    string `json:"path" minLength:"1"`
	Content string `json:"content"`
}

func (s *Service) canDecide(ctx context.Context, name string, u *auth.User) (bool, error) {
	if u.IsPrivileged() {
		return true, nil
	}
	if s.owners == nil {
		return false, nil
	}
	st, err := s.owners.ResolveForPublish(ctx, name, u)
	if err != nil {
		return false, fmt.Errorf("resolve ownership: %w", err)
	}
	return st.IsOwner && !st.Unowned, nil
}

func (s *Service) reject(ctx context.Context, id, reason string, u *auth.User) (*Submission, error) {
	return s.store.Decide(ctx, id, func(tx pgx.Tx, sub *Submission) error {
		sub.State, sub.DecidedBy, sub.Reason = StateRejected, u.Email, reason
		return audit.Record(ctx, tx, u.Email, "submission.reject", "submission:"+id, map[string]any{"skill": sub.SkillName, "reason": reason})
	})
}

// approve publishes through the normal gate as the approving person, so a
// blocking finding still holds the version.
func (s *Service) approve(ctx context.Context, id, note string, edits []SubmissionEdit, u *auth.User) (*Submission, error) {
	return s.store.Decide(ctx, id, func(tx pgx.Tx, sub *Submission) error {
		archive, err := readAll(ctx, s.storage, sub.ArchivePath)
		if err != nil {
			return fmt.Errorf("approve: read archive: %w", err)
		}
		if len(edits) > 0 {
			if archive, err = applyEdits(archive, sub.FileManifest, edits); err != nil {
				return err
			}
			sub.Edited = true
		}
		existing, err := s.skills.GetByName(ctx, sub.SkillName)
		if err != nil {
			return fmt.Errorf("approve: look up skill: %w", err)
		}
		if existing == nil {
			if _, err := s.skills.Create(ctx, sub.SkillName, "", "", "", json.RawMessage(`{}`)); err != nil {
				return fmt.Errorf("approve: create skill: %w", err)
			}
		}
		res, err := s.publisher.Publish(ctx, sub.SkillName, archive, false)
		if err != nil {
			if existing == nil {
				_ = s.skills.Delete(ctx, sub.SkillName)
			}
			return err
		}
		if res.Created {
			if _, err := tx.Exec(ctx, `UPDATE skill_versions SET submission_id = $1 WHERE id = $2`, sub.ID, res.ID); err != nil {
				return fmt.Errorf("approve: link version: %w", err)
			}
		}
		version := res.Version.Version
		sub.State, sub.DecidedBy, sub.Reason, sub.Version = StateApproved, u.Email, note, &version
		return audit.Record(ctx, tx, u.Email, "submission.approve", "submission:"+id, map[string]any{
			"skill": sub.SkillName, "version": version, "edited": sub.Edited, "gate_state": res.GateState,
		})
	})
}

func applyEdits(archive []byte, manifest []skill.FileEntry, edits []SubmissionEdit) ([]byte, error) {
	known := make(map[string]bool, len(manifest))
	for _, f := range manifest {
		known[f.Path] = true
	}
	dir, err := os.MkdirTemp("", "skael-submission-*")
	if err != nil {
		return nil, fmt.Errorf("approve: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := skill.Unpack(bytes.NewReader(archive), dir); err != nil {
		return nil, fmt.Errorf("approve: unpack: %w", err)
	}
	for _, e := range edits {
		clean := path.Clean(e.Path)
		if !known[clean] {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("the submission has no file %q", e.Path))
		}
		target := filepath.Join(dir, filepath.FromSlash(clean))
		info, err := os.Stat(target)
		if err != nil {
			return nil, fmt.Errorf("approve: edit %s: %w", clean, err)
		}
		if err := os.WriteFile(target, []byte(e.Content), info.Mode().Perm()); err != nil {
			return nil, fmt.Errorf("approve: edit %s: %w", clean, err)
		}
	}
	packed, _, _, err := skill.Pack(dir)
	if err != nil {
		return nil, fmt.Errorf("approve: pack: %w", err)
	}
	return packed, nil
}

// RegisterRoutes wires the submission endpoints.
func RegisterRoutes(api huma.API, svc *Service) {
	huma.Register(api, huma.Operation{
		OperationID: "create-submission", Method: http.MethodPost, Path: "/api/submissions",
		Summary: "Submit a skill archive for human review", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct{ Body SubmissionCreateBody }) (*struct{ Body Submission }, error) {
		sub, err := svc.Submit(ctx, in.Body)
		if err != nil {
			return nil, err
		}
		return &struct{ Body Submission }{Body: *sub}, nil
	})

	type submissionListBody struct {
		Submissions []Submission `json:"submissions"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-submissions", Method: http.MethodGet, Path: "/api/submissions",
		Summary: "List submissions",
	}, func(ctx context.Context, in *struct {
		State string `query:"state" enum:"pending,approved,rejected,"`
		Limit int    `query:"limit" default:"100" minimum:"1" maximum:"500"`
	}) (*struct{ Body submissionListBody }, error) {
		list, err := svc.store.List(ctx, in.State, in.Limit)
		if err != nil {
			return nil, err
		}
		return &struct{ Body submissionListBody }{Body: submissionListBody{Submissions: list}}, nil
	})

	type submissionPath struct {
		ID string `path:"id"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-submission", Method: http.MethodGet, Path: "/api/submissions/{id}",
		Summary: "Get a submission and its decision",
	}, func(ctx context.Context, in *submissionPath) (*struct{ Body Submission }, error) {
		sub, err := svc.Read(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body Submission }{Body: *sub}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-submission-diff", Method: http.MethodGet, Path: "/api/submissions/{id}/diff",
		Summary: "Diff a submission against the served version",
	}, func(ctx context.Context, in *submissionPath) (*struct{ Body SubmissionDiff }, error) {
		sub, err := svc.Read(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		diff, err := svc.Diff(ctx, sub)
		if err != nil {
			return nil, err
		}
		return &struct{ Body SubmissionDiff }{Body: *diff}, nil
	})

	type submissionDecisionBody struct {
		Action string           `json:"action" enum:"approve,reject"`
		Reason string           `json:"reason,omitempty" maxLength:"4000" doc:"Required to reject."`
		Edits  []SubmissionEdit `json:"edits,omitempty" doc:"Text changes to publish instead of the submitted files."`
	}
	huma.Register(api, huma.Operation{
		OperationID: "decide-submission", Method: http.MethodPost, Path: "/api/submissions/{id}/decision",
		Summary: "Approve or reject a submission",
	}, func(ctx context.Context, in *struct {
		ID   string `path:"id"`
		Body submissionDecisionBody
	}) (*struct{ Body Submission }, error) {
		u := auth.UserFromContext(ctx)
		if u == nil {
			return nil, huma.Error401Unauthorized("not authenticated")
		}
		sub, err := svc.Read(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		allowed, err := svc.canDecide(ctx, sub.SkillName, u)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, huma.Error403Forbidden("only an owner of this skill, or an instance owner or admin, may decide this submission")
		}
		var decided *Submission
		switch in.Body.Action {
		case "reject":
			if strings.TrimSpace(in.Body.Reason) == "" {
				return nil, huma.Error422UnprocessableEntity("a rejection needs a reason")
			}
			decided, err = svc.reject(ctx, in.ID, in.Body.Reason, u)
		case "approve":
			decided, err = svc.approve(ctx, in.ID, in.Body.Reason, in.Body.Edits, u)
		default:
			return nil, huma.Error422UnprocessableEntity("action must be approve or reject")
		}
		if errors.Is(err, ErrDecided) {
			return nil, huma.Error409Conflict("this submission is already decided")
		}
		if err != nil {
			return nil, err
		}
		return &struct{ Body Submission }{Body: *decided}, nil
	})
}
