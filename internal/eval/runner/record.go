package runner

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/eval/agent"
	"github.com/skael-dev/skael/internal/eval/store"
)

// ErrEventsNotWritten is separable with errors.Is because events are the one
// artifact scoring and resume read back: losing the transcript costs secondary
// evidence, losing events.jsonl means the run cannot be scored at all.
var ErrEventsNotWritten = errors.New("runner: events.jsonl was not written")

// A digested event is small, but a Paths slice from a wide glob is not, and
// bufio.Scanner's default 64KiB buffer stops at the first longer line and
// returns no error, dropping the rest of the trajectory.
const eventScanBuffer = 1 << 20 // 1 MiB

// Shared by WriteArtifacts and loadArtifactMeta so the two cannot drift.
const gradingFileName = "grading.json"

// Artifacts locates the files WriteArtifacts produced for one run.
type Artifacts struct {
	Dir            string
	TranscriptPath string
	EventsPath     string
	GradingPath    string
	OutputsDir     string
}

// Grading is the record of one run: what identifies it, the session metadata,
// and its terminal status. It is what makes a surprising score checkable.
type Grading struct {
	Key        store.RunKey
	Meta       agent.Meta
	Status     string
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

// WriteArtifacts records one run's evidence trail into dir: transcript.raw,
// events.jsonl, grading.json and outputs/.
//
// skipDirs keeps the installed bundle out of outputs/ — it is already stored
// once as the published bundle, and sixty copies of it would be most of the
// disk an evaluation uses. A baseline installs no skill and passes none.
//
// Best-effort across all four: every failure is attempted and joined, so a
// caller denied its events still gets the transcript that did write.
func WriteArtifacts(dir string, raw []byte, events []agent.Event, g Grading, workspace string, skipDirs []string) (Artifacts, error) {
	a := Artifacts{
		Dir:            dir,
		TranscriptPath: filepath.Join(dir, "transcript.raw"),
		EventsPath:     filepath.Join(dir, "events.jsonl"),
		GradingPath:    filepath.Join(dir, gradingFileName),
		OutputsDir:     filepath.Join(dir, "outputs"),
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return a, fmt.Errorf("runner: creating artifact dir %s: %w", dir, err)
	}

	var errs []error

	// Verbatim: no normalization, no re-encoding, no truncation.
	if err := os.WriteFile(a.TranscriptPath, raw, 0o644); err != nil {
		errs = append(errs, fmt.Errorf("runner: writing transcript: %w", err))
	}

	if err := writeEvents(a.EventsPath, events); err != nil {
		errs = append(errs, fmt.Errorf("%w: %w", ErrEventsNotWritten, err))
	}

	if err := writeGrading(a.GradingPath, g); err != nil {
		errs = append(errs, fmt.Errorf("runner: writing grading: %w", err))
	}

	if err := copyOutputs(workspace, a.OutputsDir, skipDirs); err != nil {
		errs = append(errs, fmt.Errorf("runner: copying outputs: %w", err))
	}

	return a, errors.Join(errs...)
}

// One compact JSON object per line, in slice order, as LoadEvents expects.
func writeEvents(path string, events []agent.Event) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("runner: creating events file: %w", err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("runner: encoding event %d: %w", e.Seq, err)
		}
	}
	return nil
}

// Indented, for a human reading a surprising result.
func writeGrading(path string, g Grading) error {
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return fmt.Errorf("runner: marshalling grading: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("runner: writing grading: %w", err)
	}
	return nil
}

// copyOutputs preserves relative paths and skips non-regular files: a verifier's
// inputs are ordinary files, and a symlink could lead outside the workspace.
func copyOutputs(workspace, outDir string, skipDirs []string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("runner: creating outputs dir: %w", err)
	}

	return filepath.WalkDir(workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == workspace {
			return nil
		}
		rel, err := filepath.Rel(workspace, path)
		if err != nil {
			return fmt.Errorf("runner: relativizing %s: %w", path, err)
		}

		if d.IsDir() {
			if skipsEntry(rel, skipDirs) {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(outDir, rel), 0o755)
		}
		if skipsEntry(rel, skipDirs) || !d.Type().IsRegular() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("runner: reading %s: %w", path, err)
		}
		if err := os.WriteFile(filepath.Join(outDir, rel), data, 0o644); err != nil {
			return fmt.Errorf("runner: writing output %s: %w", rel, err)
		}
		return nil
	})
}

// skipsEntry matches a workspace-relative path against skipDirs, exactly or as a
// descendant.
func skipsEntry(rel string, skipDirs []string) bool {
	for _, s := range skipDirs {
		if s == "" {
			continue
		}
		if rel == s || strings.HasPrefix(rel, s+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// LoadEvents reads a trajectory written by WriteArtifacts.
func LoadEvents(path string) ([]agent.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), eventScanBuffer)

	var events []agent.Event
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e agent.Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("runner: decoding event: %w", err)
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("runner: scanning events: %w", err)
	}
	return events, nil
}

// grading.json is the only place all ten Meta fields survive. The columns the
// store persists cannot rebuild Model, NumTurns, VisibleSkills,
// PermissionDenials or IsError on resume.
func loadArtifactMeta(artifactDir string) (agent.Meta, error) {
	if artifactDir == "" {
		return agent.Meta{}, errors.New("no artifact directory recorded")
	}
	g, err := LoadGrading(filepath.Join(artifactDir, gradingFileName))
	if err != nil {
		return agent.Meta{}, err
	}
	return g.Meta, nil
}

// LoadGrading reads a grading.json written by WriteArtifacts.
func LoadGrading(path string) (*Grading, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var g Grading
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("runner: decoding grading: %w", err)
	}
	return &g, nil
}
