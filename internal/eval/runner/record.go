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
	"github.com/skael-dev/skael/internal/eval/trajectory"
)

// ErrEventsNotWritten wraps a failure to write events.jsonl specifically, so
// a caller can tell that failure apart from a transcript, grading, or
// outputs failure with errors.Is rather than by parsing a message. Events
// are the one artifact scoring and resume actually read back — losing the
// transcript or a verifier's output files is a loss of secondary evidence,
// but losing events.jsonl means the run cannot be scored or resumed at all.
var ErrEventsNotWritten = errors.New("runner: events.jsonl was not written")

// eventScanBuffer is the maximum size of a single events.jsonl line LoadEvents
// will accept. A digested event is small, but a Paths slice from a wide glob
// is not, and bufio.Scanner's default 64KiB buffer silently stops (returning
// no error) at the first line that exceeds it, dropping the rest of the
// trajectory rather than failing.
const eventScanBuffer = 1 << 20 // 1 MiB

// Artifacts locates the files WriteArtifacts produced for one run.
type Artifacts struct {
	Dir            string
	TranscriptPath string
	EventsPath     string
	GradingPath    string
	OutputsDir     string
}

// Grading is the human- and machine-readable record of how one run was
// graded: the key that identifies it, what the verifier reported, the
// adapter's session metadata, and its terminal status. It is what a report
// drills into and what makes a surprising score checkable.
type Grading struct {
	Key store.RunKey
	// VerifierExit is nil when the verifier never ran — see
	// store.RunOutcome.VerifierExit, which this mirrors.
	VerifierExit *int
	Meta         agent.Meta
	Status       string
	Error        string
	StartedAt    time.Time
	FinishedAt   time.Time
}

// WriteArtifacts records the evidence trail for one run into dir:
// transcript.raw (the agent's native stream, byte for byte), events.jsonl
// (the normalized trajectory, one compact JSON object per line, in order),
// grading.json (the Grading record, indented for a human), and outputs/ (a
// copy of the workspace's regular files a verifier inspected, skipping any
// entry under a directory listed in skipDirs).
//
// skipDirs exists so the installed skill bundle — and, for a trigger probe,
// the distractor pack alongside it — is never copied into outputs/: it is
// already stored once as the published bundle, and sixty copies of it under
// runs/ would be most of the disk an evaluation uses for zero evidentiary
// value. A baseline session installs no skill, so its caller passes no
// skipDirs and its real outputs are copied in full.
//
// WriteArtifacts is best-effort across the four artifacts: it attempts every
// one rather than stopping at the first failure, so a caller that only cares
// about (say) the events failure is not also denied the transcript that did
// write successfully. Every failure is accumulated and returned via
// errors.Join; a failure to write events.jsonl specifically is wrapped in
// ErrEventsNotWritten so a caller can single it out with errors.Is without
// parsing the message.
func WriteArtifacts(dir string, raw []byte, events []trajectory.Event, g Grading, workspace string, skipDirs []string) (Artifacts, error) {
	a := Artifacts{
		Dir:            dir,
		TranscriptPath: filepath.Join(dir, "transcript.raw"),
		EventsPath:     filepath.Join(dir, "events.jsonl"),
		GradingPath:    filepath.Join(dir, "grading.json"),
		OutputsDir:     filepath.Join(dir, "outputs"),
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return a, fmt.Errorf("runner: creating artifact dir %s: %w", dir, err)
	}

	var errs []error

	// The transcript is the record of what the CLI actually said. Written
	// verbatim: no normalization, no re-encoding, no truncation.
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

// writeEvents writes one compact JSON object per line, in slice order — the
// format loadProbeEvents and LoadEvents both expect.
func writeEvents(path string, events []trajectory.Event) error {
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

// writeGrading writes g as indented JSON, for a human reading a surprising
// result.
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

// copyOutputs copies every regular file under workspace into outDir,
// preserving relative paths, skipping any entry under a directory listed in
// skipDirs and any non-regular file (a symlink, device, or similar — a
// verifier's inputs are ordinary files, and copying a symlink verbatim could
// follow it outside the workspace).
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

// skipsEntry reports whether rel — a workspace-relative path — falls under
// one of skipDirs, either as an exact match or as a descendant.
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

// LoadEvents reads a newline-delimited JSON trajectory written by
// WriteArtifacts.
func LoadEvents(path string) ([]trajectory.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), eventScanBuffer)

	var events []trajectory.Event
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e trajectory.Event
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
