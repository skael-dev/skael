package gitsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/contest"
	"github.com/skael-dev/skael/internal/evalqueue"
	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/gate"
	skillimport "github.com/skael-dev/skael/internal/import"
	"github.com/skael-dev/skael/internal/ownership"
	"github.com/skael-dev/skael/internal/skill"
)

// ContestLabel on a pull request asks for a contest. Adding a label needs
// write access to the repo, so it is a request a stranger cannot make.
const ContestLabel = "skael:contest"

// checkName is the check run's name on GitHub.
const checkName = "Skael"

// PRHead is one skill folder at one pull request head.
type PRHead struct {
	ID          string        `json:"id"`
	SkillName   string        `json:"skill_name"`
	Digest      string        `json:"digest"`
	Decision    gate.Decision `json:"decision"`
	ContestID   string        `json:"contest_id,omitempty"`
	archivePath string
	skillID     string
}

// PullCheck is the Skael check on one pull request head.
type PullCheck struct {
	PRNumber         int       `json:"pr_number"`
	PRURL            string    `json:"pr_url"`
	Title            string    `json:"title"`
	HeadSHA          string    `json:"head_sha"`
	Conclusion       string    `json:"conclusion"`
	Fork             bool      `json:"fork"`
	ContestRequested bool      `json:"contest_requested"`
	Heads            []PRHead  `json:"heads"`
	UpdatedAt        time.Time `json:"updated_at"`
	checkRunID       int64
	summary          string
}

func (s *Store) heads(ctx context.Context, connID string, pr int, sha string) ([]PRHead, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, skill_name, digest, decision, COALESCE(contest_id::text, ''), archive_path, COALESCE(skill_id::text, '')
		FROM git_pr_heads WHERE connection_id = $1 AND pr_number = $2 AND head_sha = $3 ORDER BY skill_name`,
		connID, pr, sha)
	if err != nil {
		return nil, fmt.Errorf("gitsync: PR heads: %w", err)
	}
	defer rows.Close()
	out := []PRHead{}
	for rows.Next() {
		var h PRHead
		var decision []byte
		if err := rows.Scan(&h.ID, &h.SkillName, &h.Digest, &decision, &h.ContestID, &h.archivePath, &h.skillID); err != nil {
			return nil, fmt.Errorf("gitsync: PR heads: %w", err)
		}
		_ = json.Unmarshal(decision, &h.Decision)
		out = append(out, h)
	}
	return out, rows.Err()
}

// PullChecks returns the newest check of each open pull request on a connection.
func (s *Store) PullChecks(ctx context.Context, connID string) ([]PullCheck, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (pr_number) pr_number, pr_url, title, head_sha, conclusion, fork, contest_requested,
			updated_at, check_run_id, summary
		FROM git_pr_checks WHERE connection_id = $1 AND conclusion <> 'none'
		ORDER BY pr_number DESC, created_at DESC`, connID)
	if err != nil {
		return nil, fmt.Errorf("gitsync: PR checks: %w", err)
	}
	var out []PullCheck
	for rows.Next() {
		var c PullCheck
		if err := rows.Scan(&c.PRNumber, &c.PRURL, &c.Title, &c.HeadSHA, &c.Conclusion, &c.Fork,
			&c.ContestRequested, &c.UpdatedAt, &c.checkRunID, &c.summary); err != nil {
			rows.Close()
			return nil, fmt.Errorf("gitsync: PR checks: %w", err)
		}
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		h, err := s.heads(ctx, connID, out[i].PRNumber, out[i].HeadSHA)
		if err != nil {
			return nil, err
		}
		out[i].Heads = h
	}
	if out == nil {
		out = []PullCheck{}
	}
	return out, nil
}

// RequestContest records a request for a contest on a pull request's newest
// head. headSHA empty means the newest head Skael checked.
func (s *Store) RequestContest(ctx context.Context, connID string, pr int, headSHA string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE git_pr_checks SET contest_requested = TRUE, updated_at = now()
		WHERE connection_id = $1 AND pr_number = $2 AND ($3 = '' OR head_sha = $3)
		  AND created_at = (SELECT max(created_at) FROM git_pr_checks
		                    WHERE connection_id = $1 AND pr_number = $2 AND ($3 = '' OR head_sha = $3))`,
		connID, pr, headSHA)
	if err != nil {
		return false, fmt.Errorf("gitsync: request contest: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// checkPulls checks every open pull request into the branch once per head,
// starts the contests the setting and the requests allow, reports finished
// contests, and drops what closed pull requests left behind.
func (r *run) checkPulls(ctx context.Context, gh *GitHub) error {
	pulls, err := gh.OpenPulls(ctx, r.c.InstallationID, r.c.RepoOwner, r.c.RepoName, r.c.Branch)
	if err != nil {
		if unreachable(err) {
			return nil
		}
		return err
	}
	open := map[int]string{}
	for i := range pulls {
		pr := &pulls[i]
		open[pr.Number] = pr.Head.SHA
		var exists bool
		if err := r.s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM git_pr_checks
			WHERE connection_id = $1 AND pr_number = $2 AND head_sha = $3)`, r.c.ID, pr.Number, pr.Head.SHA).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if err := r.checkPull(ctx, gh, pr); err != nil {
				return err
			}
		}
		if err := r.maybeContest(ctx, gh, pr); err != nil {
			return err
		}
		if err := r.reportContests(ctx, gh, pr.Number, pr.Head.SHA); err != nil {
			return err
		}
	}
	return r.dropClosed(ctx, open)
}

// checkPull scans every skill folder a pull request touches at its head and
// posts one check run. A pull request that touches no skill folder gets none.
func (r *run) checkPull(ctx context.Context, gh *GitHub, pr *OpenPull) error {
	files, err := gh.PullFiles(ctx, r.c.InstallationID, r.c.RepoOwner, r.c.RepoName, pr.Number)
	if err != nil {
		return err
	}
	sn, err := r.s.fetch(ctx, gh, r.c.InstallationID, r.c.RepoOwner, r.c.RepoName, pr.Head.SHA, r.c.RootPath)
	if err != nil {
		var ue *UserError
		if errors.As(err, &ue) {
			return r.recordCheck(ctx, gh, pr, "none", "", false)
		}
		return err
	}
	defer sn.close()

	counts := map[string]int{}
	for _, f := range sn.folders {
		counts[f.Name]++
	}
	var lines []string
	conclusion, contestable := "success", false
	for _, f := range sn.folders {
		if !touches(files, f.Path) {
			continue
		}
		switch {
		case !ownership.Matches(r.c.Prefix, f.Name):
			lines = append(lines, fmt.Sprintf("- `%s` (%s): outside the prefix `%s`. The merge does not publish it.", f.Name, f.Path, r.c.Prefix))
			conclusion = worse(conclusion, "neutral")
			continue
		case counts[f.Name] > 1:
			lines = append(lines, fmt.Sprintf("- `%s` (%s): another folder declares this name. The merge does not publish it.", f.Name, f.Path))
			conclusion = worse(conclusion, "neutral")
			continue
		}
		h, err := r.storeHead(ctx, pr, sn, f)
		if err != nil {
			return err
		}
		switch h.Decision.Outcome {
		case gate.Block:
			conclusion = "failure"
			lines = append(lines, fmt.Sprintf("- `%s`: **blocked**. %s", h.SkillName, reasons(h.Decision)))
		case gate.NeedsReview:
			conclusion = worse(conclusion, "neutral")
			lines = append(lines, fmt.Sprintf("- `%s`: held for review on merge. %s", h.SkillName, reasons(h.Decision)))
		default:
			lines = append(lines, fmt.Sprintf("- `%s`: passes the scan.", h.SkillName))
		}
		if h.Decision.Outcome != gate.Block && h.skillID != "" {
			contestable = true
		}
	}
	if len(lines) == 0 {
		return r.recordCheck(ctx, gh, pr, "none", "", false)
	}
	return r.recordCheck(ctx, gh, pr, conclusion, strings.Join(lines, "\n"), contestable)
}

// storeHead packs, scans and stores one folder at a pull request head.
func (r *run) storeHead(ctx context.Context, pr *OpenPull, sn *snapshot, f skillimport.DiscoveredSkill) (*PRHead, error) {
	raw, digest, _, err := skill.Pack(filepath.Join(sn.dir, filepath.FromSlash(f.Path)))
	if err != nil {
		return nil, fmt.Errorf("gitsync: pack %s at PR #%d: %w", f.Name, pr.Number, err)
	}
	report, decision, err := r.s.publisher.ScanArchive(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("gitsync: scan %s at PR #%d: %w", f.Name, pr.Number, err)
	}
	if owner, ok, err := r.requesterOwnership(ctx, pr.Number, f.Name); err != nil {
		return nil, err
	} else if ok {
		decision = skill.DecidePublish(report, owner, 0, false)
	}
	path := fmt.Sprintf("pr-heads/%s/%d/%s/%s.tar.gz", r.c.ID, pr.Number, pr.Head.SHA,
		branchSafe.ReplaceAllString(strings.ToLower(f.Name), "-"))
	if _, err := r.s.deps.Storage.Write(ctx, path, bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("gitsync: store PR head: %w", err)
	}
	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		return nil, err
	}
	h := &PRHead{SkillName: f.Name, Digest: digest, Decision: decision, archivePath: path}
	var skillID *string
	if sk, err := r.s.skills.GetByName(ctx, f.Name); err != nil {
		return nil, err
	} else if sk != nil {
		skillID, h.skillID = &sk.ID, sk.ID
	}
	err = r.s.pool.QueryRow(ctx, `
		INSERT INTO git_pr_heads (connection_id, skill_id, skill_name, pr_number, head_sha, digest, archive_path, decision)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (connection_id, pr_number, head_sha, skill_name) DO UPDATE SET decision = EXCLUDED.decision
		RETURNING id`, r.c.ID, skillID, f.Name, pr.Number, pr.Head.SHA, digest, path, decisionJSON).Scan(&h.ID)
	if err != nil {
		return nil, fmt.Errorf("gitsync: record PR head: %w", err)
	}
	return h, nil
}

// requesterOwnership resolves the requester of an outbound change against the
// skill's ownership rule. ok is false for a pull request a person opened in
// git, where review in git covers ownership.
func (r *run) requesterOwnership(ctx context.Context, pr int, name string) (gate.OwnerState, bool, error) {
	if r.s.deps.Owners == nil || r.s.deps.Users == nil {
		return gate.OwnerState{}, false, nil
	}
	var requester string
	err := r.s.pool.QueryRow(ctx, `SELECT requested_by FROM git_changes WHERE connection_id = $1 AND pr_number = $2
		ORDER BY created_at DESC LIMIT 1`, r.c.ID, pr).Scan(&requester)
	if errors.Is(err, pgx.ErrNoRows) {
		return gate.OwnerState{}, false, nil
	}
	if err != nil {
		return gate.OwnerState{}, false, err
	}
	row, err := r.s.deps.Users.GetByEmail(ctx, requester)
	if err != nil || row == nil {
		return gate.OwnerState{}, false, err
	}
	owner, err := r.s.deps.Owners.ResolveForPublish(ctx, name, &auth.User{ID: row.ID, Email: row.Email, Role: row.Role, Kind: row.Kind})
	return owner, err == nil, err
}

func touches(files []string, dir string) bool {
	for _, f := range files {
		if strings.HasPrefix(f, dir+"/") {
			return true
		}
	}
	return false
}

// worse returns the more severe of two conclusions.
func worse(a, b string) string {
	rank := map[string]int{"success": 0, "neutral": 1, "failure": 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func reasons(d gate.Decision) string {
	var out []string
	for _, r := range d.Reasons {
		line := r.Message
		if r.File != "" {
			line = fmt.Sprintf("%s (%s:%d)", r.Message, r.File, r.Line)
		}
		out = append(out, line)
	}
	return strings.Join(out, " ")
}

// recordCheck stores a check and posts it to GitHub. A pull request that
// touches no skill is recorded as "none" so it is not read again at this head.
func (r *run) recordCheck(ctx context.Context, gh *GitHub, pr *OpenPull, conclusion, summary string, contestable bool) error {
	var runID int64
	if conclusion != "none" {
		run := CheckRun{
			Name: checkName, HeadSHA: pr.Head.SHA, Status: "completed", Conclusion: conclusion,
			Output: &CheckRunOutput{Title: checkTitle(conclusion), Summary: summary},
		}
		if contestable && r.c.ContestMode != ContestOff {
			run.Actions = []CheckRunAction{{Label: "Run contest", Description: "Compare with the released version", Identifier: "contest"}}
		}
		id, err := gh.CreateCheckRun(ctx, r.c.InstallationID, r.c.RepoOwner, r.c.RepoName, run)
		if err != nil && !unreachable(err) {
			return err
		}
		runID = id
	}
	_, err := r.s.pool.Exec(ctx, `
		INSERT INTO git_pr_checks (connection_id, pr_number, head_sha, check_run_id, conclusion, summary, pr_url, title, fork)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) ON CONFLICT DO NOTHING`,
		r.c.ID, pr.Number, pr.Head.SHA, runID, conclusion, summary, pr.HTMLURL, pr.Title, pr.Fork())
	return err
}

func checkTitle(conclusion string) string {
	switch conclusion {
	case "failure":
		return "Blocked by the scan"
	case "neutral":
		return "Held for review when merged"
	}
	return "Passes the scan"
}

// maybeContest starts a contest for each contestable head of a pull request
// when the setting allows one and something asked for it. A fork never gets a
// contest without a request: strangers must not spend the LLM budget.
func (r *run) maybeContest(ctx context.Context, gh *GitHub, pr *OpenPull) error {
	if r.c.ContestMode == ContestOff || r.s.deps.Contests == nil || r.s.deps.Queue == nil {
		return nil
	}
	var requested bool
	err := r.s.pool.QueryRow(ctx, `SELECT contest_requested FROM git_pr_checks
		WHERE connection_id = $1 AND pr_number = $2 AND head_sha = $3`, r.c.ID, pr.Number, pr.Head.SHA).Scan(&requested)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	requested = requested || pr.HasLabel(ContestLabel)
	auto := r.c.ContestMode == ContestReady && !pr.Draft && !pr.Fork()
	if !requested && !auto {
		return nil
	}
	heads, err := r.s.store.heads(ctx, r.c.ID, pr.Number, pr.Head.SHA)
	if err != nil {
		return err
	}
	started := false
	for _, h := range heads {
		if h.ContestID != "" || h.Decision.Outcome == gate.Block {
			continue
		}
		if h.skillID == "" {
			// The head was checked before its skill existed, such as on a
			// connection's first sync, which checks pull requests first.
			if err := r.s.pool.QueryRow(ctx, `
				UPDATE git_pr_heads SET skill_id = s.id FROM skills s
				WHERE git_pr_heads.id = $1 AND s.name = git_pr_heads.skill_name
				RETURNING s.id::text`, h.ID).Scan(&h.skillID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				return err
			}
		}
		id, err := r.startContest(ctx, pr, h)
		if err != nil {
			return err
		}
		if id != "" {
			started = true
		}
	}
	if started {
		return r.reportContests(ctx, gh, pr.Number, pr.Head.SHA)
	}
	return nil
}

// startContest runs a PR head against the skill's released version, on the
// skill's latest suite. A skill with no released version has nothing to
// compare against.
func (r *run) startContest(ctx context.Context, pr *OpenPull, h PRHead) (string, error) {
	sk, err := r.s.skills.GetByName(ctx, h.SkillName)
	if err != nil || sk == nil || sk.LatestVersion == 0 {
		return "", err
	}
	c := &contest.Contest{
		Tier: "full", Attempts: contest.DefaultAttempts, RequestedBy: r.saEmail,
		SuiteProvenance: contest.ProvenanceDerivedAll,
		Candidates: []contest.Candidate{
			{SkillID: sk.ID, SkillName: sk.Name, Version: sk.LatestVersion, Label: contest.Label(sk.Name, sk.LatestVersion)},
			{SkillID: sk.ID, SkillName: sk.Name, Label: fmt.Sprintf("%s@pr%d", sk.Name, pr.Number), PRHeadID: h.ID},
		},
	}
	if r.s.deps.Suites != nil {
		rec, err := r.s.deps.Suites.LatestForSkill(ctx, sk.Name)
		// A skill with no suite is a contest the worker derives a suite for.
		if errors.Is(err, evalsuite.ErrNotFound) {
			rec, err = nil, nil
		}
		if err != nil {
			return "", err
		}
		if rec != nil {
			c.SuiteRef = rec.Ref
			if rec.Origin == evalsuite.OriginAuthored {
				c.SuiteProvenance, c.SuiteReviewedBy = contest.ProvenanceAuthored, rec.ReviewedBy
			} else {
				c.SuiteProvenance, c.SuiteDerivedFrom = contest.ProvenanceDerivedOne, rec.SkillName
			}
		}
	}
	if err := r.s.deps.Contests.Create(ctx, c); err != nil {
		return "", err
	}
	jobID, err := r.s.deps.Queue.Submit(ctx, evalqueue.Job{
		SkillID: sk.ID, SkillName: sk.Name, Version: sk.LatestVersion, SuiteRef: c.SuiteRef,
		Tier: c.Tier, RequestedBy: r.saEmail, ContestID: c.ID,
	})
	if err != nil {
		_ = r.s.deps.Contests.Fail(ctx, c.ID, "could not enqueue: "+err.Error())
		return "", err
	}
	if err := r.s.deps.Contests.AttachJob(ctx, c.ID, string(jobID)); err != nil {
		return "", err
	}
	if _, err := r.s.pool.Exec(ctx, `UPDATE git_pr_heads SET contest_id = $2 WHERE id = $1`, h.ID, c.ID); err != nil {
		return "", err
	}
	log.Info().Str("skill", sk.Name).Int("pr", pr.Number).Str("contest", c.ID).Msg("gitsync: started a PR contest")
	return c.ID, nil
}

// reportContests rewrites the check run's summary with each contest's state.
// The conclusion stays the scan's: a contest holds nothing.
func (r *run) reportContests(ctx context.Context, gh *GitHub, pr int, sha string) error {
	if r.s.deps.Contests == nil {
		return nil
	}
	heads, err := r.s.store.heads(ctx, r.c.ID, pr, sha)
	if err != nil {
		return err
	}
	var lines []string
	pending := false
	for _, h := range heads {
		if h.ContestID == "" {
			continue
		}
		var reported bool
		if err := r.s.pool.QueryRow(ctx, `SELECT contest_reported FROM git_pr_heads WHERE id = $1`, h.ID).Scan(&reported); err != nil {
			return err
		}
		c, err := r.s.deps.Contests.Get(ctx, h.ContestID)
		if err != nil || c == nil {
			return err
		}
		link := strings.TrimSuffix(r.s.deps.publicURL(), "/") + "/contests/" + c.ID
		switch c.Status {
		case contest.StatusDone:
			lines = append(lines, fmt.Sprintf("- `%s`: %s. [Contest](%s)", h.SkillName, c.Detail, link))
			if !reported {
				pending = true
				if _, err := r.s.pool.Exec(ctx, `UPDATE git_pr_heads SET contest_reported = TRUE WHERE id = $1`, h.ID); err != nil {
					return err
				}
			}
		case contest.StatusFailed:
			lines = append(lines, fmt.Sprintf("- `%s`: the contest failed: %s. [Contest](%s)", h.SkillName, c.LastError, link))
			if !reported {
				pending = true
				if _, err := r.s.pool.Exec(ctx, `UPDATE git_pr_heads SET contest_reported = TRUE WHERE id = $1`, h.ID); err != nil {
					return err
				}
			}
		default:
			lines = append(lines, fmt.Sprintf("- `%s`: contest running against the released version. [Contest](%s)", h.SkillName, link))
			if !reported && c.JobID != "" {
				pending = true
			}
		}
	}
	if len(lines) == 0 || !pending {
		return nil
	}
	var runID int64
	var conclusion, summary string
	if err := r.s.pool.QueryRow(ctx, `SELECT check_run_id, conclusion, summary FROM git_pr_checks
		WHERE connection_id = $1 AND pr_number = $2 AND head_sha = $3`, r.c.ID, pr, sha).Scan(&runID, &conclusion, &summary); err != nil {
		return err
	}
	if runID == 0 {
		return nil
	}
	err = gh.UpdateCheckRun(ctx, r.c.InstallationID, r.c.RepoOwner, r.c.RepoName, runID, CheckRun{
		Conclusion: conclusion, Status: "completed",
		Output: &CheckRunOutput{Title: checkTitle(conclusion), Summary: summary + "\n\n**Contests**\n\n" + strings.Join(lines, "\n")},
	})
	if err != nil && !unreachable(err) {
		return err
	}
	return nil
}

// dropClosed removes the checks of closed pull requests and every PR head no
// contest references that is not the current head of an open pull request.
func (r *run) dropClosed(ctx context.Context, open map[int]string) error {
	rows, err := r.s.pool.Query(ctx, `SELECT id, pr_number, head_sha, archive_path FROM git_pr_heads
		WHERE connection_id = $1 AND contest_id IS NULL`, r.c.ID)
	if err != nil {
		return err
	}
	type stale struct{ id, path string }
	var drop []stale
	for rows.Next() {
		var id, sha, path string
		var pr int
		if err := rows.Scan(&id, &pr, &sha, &path); err != nil {
			rows.Close()
			return err
		}
		if open[pr] != sha {
			drop = append(drop, stale{id, path})
		}
	}
	rows.Close()
	for _, d := range drop {
		if _, err := r.s.pool.Exec(ctx, `DELETE FROM git_pr_heads WHERE id = $1`, d.id); err != nil {
			return err
		}
		if err := r.s.deps.Storage.Delete(ctx, d.path); err != nil {
			log.Warn().Err(err).Str("path", d.path).Msg("gitsync: delete a PR head archive")
		}
	}
	numbers := make([]int, 0, len(open))
	for n := range open {
		numbers = append(numbers, n)
	}
	_, err = r.s.pool.Exec(ctx, `DELETE FROM git_pr_checks WHERE connection_id = $1 AND NOT (pr_number = ANY($2))`,
		r.c.ID, numbers)
	return err
}

// linkContest records on a new version the contest that evaluated its exact
// content as a PR head. The version still gets its normal evaluation: a
// contest has no baseline and no headline.
func (r *run) linkContest(ctx context.Context, versionID, skillID, digest string) error {
	_, err := r.s.pool.Exec(ctx, `
		UPDATE skill_versions SET contest_id = (
			SELECT contest_id FROM git_pr_heads
			WHERE skill_id = $2 AND digest = $3 AND contest_id IS NOT NULL
			ORDER BY created_at DESC LIMIT 1)
		WHERE id = $1`, versionID, skillID, digest)
	return err
}

// PRHeadArchive returns a stored PR head's archive, or nil when there is none.
func (s *Syncer) PRHeadArchive(ctx context.Context, id string) ([]byte, error) {
	var path string
	err := s.pool.QueryRow(ctx, `SELECT archive_path FROM git_pr_heads WHERE id = $1`, id).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rc, err := s.deps.Storage.Read(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("gitsync: read PR head archive: %w", err)
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
