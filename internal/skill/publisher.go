package skill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/scan"
)

// PublishQuality reports whether a publish queued an evaluation.
type PublishQuality struct {
	State string `json:"state,omitempty"`
	JobID string `json:"job_id,omitempty"`
}

// PublishBody is the result of a publish.
type PublishBody struct {
	Version
	Created  bool           `json:"created"`
	Quality  PublishQuality `json:"quality"`
	Decision gate.Decision  `json:"decision"`
}

// Publisher runs an archive through the scan and the gate. It acts as the user
// in ctx, so every path that creates a version meets the same decision.
type Publisher struct {
	store   *Store
	storage platform.Storage
	opts    RouteOptions
}

// NewPublisher constructs a Publisher.
func NewPublisher(store *Store, storage platform.Storage, opts RouteOptions) *Publisher {
	return &Publisher{store: store, storage: storage, opts: opts}
}

// scanDir runs the native scan and the optional external scanner. dir is a
// throwaway unpack directory. Its absolute path means nothing to the publisher
// and leaks the server's filesystem layout, so it is rewritten before the
// report is persisted, decided on or returned.
func (p *Publisher) scanDir(ctx context.Context, dir string) (*scan.Report, error) {
	report, err := scan.ScanDir(dir)
	if err != nil {
		return nil, fmt.Errorf("publish: scan: %w", err)
	}
	scan.MergeExternal(ctx, p.opts.External, dir, report)
	scan.Relativize(report, dir)
	return report, nil
}

// ScanArchive unpacks and scans an archive that creates no version, and returns
// what the gate would decide with no owner and no override.
func (p *Publisher) ScanArchive(ctx context.Context, rawBody []byte) (*scan.Report, gate.Decision, error) {
	dir, err := os.MkdirTemp("", "skael-scan-*")
	if err != nil {
		return nil, gate.Decision{}, fmt.Errorf("scan archive: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := Unpack(bytes.NewReader(rawBody), dir); err != nil {
		return nil, gate.Decision{}, huma.Error400BadRequest(fmt.Sprintf("invalid archive: %s", err))
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		return nil, gate.Decision{}, huma.Error400BadRequest("archive must contain SKILL.md")
	}
	report, err := p.scanDir(ctx, dir)
	if err != nil {
		return nil, gate.Decision{}, err
	}
	return report, DecidePublish(report, gate.OwnerState{}, p.opts.QualityFloor, false), nil
}

// Publish creates a version of the named skill from rawBody.
func (p *Publisher) Publish(ctx context.Context, name string, rawBody []byte, override bool) (*PublishBody, error) {
	// 1. Look up the skill.
	sk, err := p.store.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("publish: lookup skill: %w", err)
	}
	if sk == nil {
		return nil, huma.Error404NotFound(
			fmt.Sprintf("skill %q not found", name))
	}

	// 2. Unpack archive to a temp dir.
	tmpDir, err := os.MkdirTemp("", "skael-publish-*")
	if err != nil {
		return nil, fmt.Errorf("publish: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := Unpack(bytes.NewReader(rawBody), tmpDir); err != nil {
		return nil, huma.Error400BadRequest(
			fmt.Sprintf("invalid archive: %s", err))
	}

	// 3. Security scan.
	report, err := p.scanDir(ctx, tmpDir)
	if err != nil {
		return nil, err
	}

	// Resolve ownership before deciding. A nil resolver leaves the zero
	// OwnerState, which contributes nothing — that is what keeps every
	// test harness and the CLI's local pre-scan on the pre-ownership
	// behaviour.
	var owner gate.OwnerState
	if p.opts.Ownership != nil {
		owner, err = p.opts.Ownership.ResolveForPublish(ctx, name, auth.UserFromContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("publish: resolve ownership: %w", err)
		}
	}

	decision := DecidePublish(report, owner, p.opts.QualityFloor, publishOverrideAllowed(ctx, override))

	if decision.Outcome == gate.Block {
		payload, _ := json.Marshal(struct {
			Scan     *scan.Report  `json:"scan"`
			Decision gate.Decision `json:"decision"`
		}{report, decision})
		return nil, huma.NewError(
			http.StatusUnprocessableEntity,
			"archive rejected: it contains credential-theft or data-exfiltration findings, which are unappealable — no evaluation and no override clears them",
			fmt.Errorf("%s", payload),
		)
	}

	if decision.Outcome == gate.Allow && len(decision.Reasons) > 0 {
		// Reached only via an admin override, since a clean report has
		// no reasons and a quality state cannot exist at publish time.
		user := auth.UserFromContext(ctx)
		log.Warn().
			Str("skill", name).
			Str("user", user.Email).
			Str("role", user.Role).
			Str("scan_status", report.Status).
			Int("critical", report.Summary.Critical).
			Int("high", report.Summary.High).
			Msg("publish override: privileged user published a skill with findings that would otherwise hold it for review")
	}

	if decision.Outcome == gate.NeedsReview {
		log.Info().
			Str("skill", name).
			Int("reasons", len(decision.Reasons)).
			Msg("publish held for review: version created but not served until an evaluation or an admin clears it")
	}

	// 4. Compute checksum and compare against latest version.
	h := sha256.Sum256(rawBody)
	checksum := hex.EncodeToString(h[:])

	if sk.LatestVersion > 0 {
		latest, err := p.store.GetVersion(ctx, name, sk.LatestVersion)
		if err == nil && latest != nil && latest.Checksum == checksum {
			// Unchanged content: report the version's persisted gate
			// state, not a fresh recompute. `decision` above was
			// computed with nil quality evidence and no admin override
			// in play for *this* request — it is a hypothetical ("what
			// would this bundle decide right now"), not the version's
			// actual status. latest may have been approved or rejected
			// since it was first decided, and that stored outcome —
			// gate_decision, snapshotted at the time it was made — is
			// the only truth for a bundle nobody is re-deciding.
			// Nothing new was scored, so quality state is "none"; created
			// is false, so a client knows nothing new was gated either.
			persisted := decision
			if len(latest.GateDecision) > 0 {
				var stored gate.Decision
				if err := json.Unmarshal(latest.GateDecision, &stored); err != nil {
					return nil, fmt.Errorf("publish: unmarshal persisted gate decision: %w", err)
				}
				persisted = stored
			}
			return &PublishBody{
				Version: *latest, Created: false,
				Quality: PublishQuality{State: "none"}, Decision: persisted,
			}, nil
		}
	}

	// archiveName is content-addressable: different content → different filename,
	// so concurrent publishes with distinct payloads cannot overwrite each other.
	// p.storage.Write stores it relative to BasePath, and p.storage.Read reads it
	// the same way.
	archiveName := fmt.Sprintf("%s/%s.tar.gz", name, checksum[:16])

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

	// 7. Determine publisher identity from auth context.
	publishedBy := "system"
	if u := auth.UserFromContext(ctx); u != nil {
		publishedBy = u.Email
	}

	// 8. Write archive to p.storage. All validation has passed; write last so
	// that no orphaned blobs are left behind when earlier steps fail.
	if _, err := p.storage.Write(ctx, archiveName, bytes.NewReader(rawBody)); err != nil {
		return nil, fmt.Errorf("publish: store archive: %w", err)
	}

	// 9. Create version record. Store the relative archiveName so that
	// p.storage.Read can locate the file without needing the absolute basePath.
	ver, err := p.store.CreateVersion(ctx,
		sk.ID,
		archiveName,
		checksum,
		changelog,
		description,
		body,
		fmJSON,
		manifest,
		scanJSON,
		publishedBy,
		decision,
	)
	if err != nil {
		_ = p.storage.Delete(ctx, archiveName)
		return nil, huma.Error500InternalServerError("creating version", err)
	}

	// 9. Extract and persist spec-compliance metadata — but only for a
	// version that is actually being served. A held version must not
	// publish its own metadata onto the skill row while the gate is
	// withholding its archive; the held version's frontmatter lives on
	// its own skill_versions row, which is what a review UI renders.
	if !decision.Held() {
		spec := ValidateSpec(fm, sk.Name)
		_ = p.store.UpdateSpecFields(ctx, sk.Name, spec.Author, spec.License, spec.Compat, spec.Compliance, spec.DisplayName, spec.Tags)
	}

	// 10. Enqueue an evaluation when a suite exists for this skill. A queue
	// outage must not fail a publish: the version is already durable, and
	// an unscored version is a state the product already models.
	quality := PublishQuality{State: "none"}
	if p.opts.Queue != nil && p.opts.Suites != nil {
		rec, err := p.opts.Suites.LatestForSkill(ctx, name)
		if err != nil {
			// SuiteLookup's contract is (nil, nil) for "no suite
			// registered" — any non-nil error here is an infrastructure
			// failure (lookup, not absence), and reporting "none" without
			// a trace would leave a skill that genuinely has a suite
			// permanently unscored with no operator signal.
			log.Warn().Err(err).Str("skill", name).Msg("publish: suite lookup failed, skipping enqueue")
		} else if rec != nil {
			id, err := p.opts.Queue.Submit(ctx, EvalJobRequest{
				SkillID: sk.ID, SkillName: name, Version: ver.Version,
				SuiteRef: rec.Ref, Tier: "full", RequestedBy: publishedBy,
			})
			if err != nil {
				log.Warn().Err(err).Str("skill", name).Msg("publish: could not enqueue evaluation")
			} else {
				quality = PublishQuality{State: "pending", JobID: id}
			}
		}
	}

	// O6. A new name claims its publisher — unless a rule already covers it,
	// in which case the rule wins and this version is held like any other
	// proposal. Without the exception, publishing payments:anything is a way
	// to become an owner inside someone else's namespace. This must only
	// fire on the skill's first version: gating on owner.Unowned alone would
	// mean the first person to publish an UPDATE to any pre-existing unowned
	// skill (e.g. on an upgraded instance with no ownership rules yet)
	// silently becomes its sole owner, breaking the "upgrade is a no-op"
	// promise. ver.Version == 1 mirrors import's newSkill check.
	if p.opts.Ownership != nil && owner.Unowned && ver.Version == 1 {
		if err := p.opts.Ownership.ClaimOnFirstPublish(ctx, name, auth.UserFromContext(ctx)); err != nil {
			// The version is already durable and served. Failing the publish
			// here would be a worse outcome than an unowned skill, which is
			// a state the product already models.
			log.Warn().Err(err).Str("skill", name).
				Msg("publish: could not claim first-publish ownership")
		}
	}

	return &PublishBody{Version: *ver, Created: true, Quality: quality, Decision: decision}, nil
}
