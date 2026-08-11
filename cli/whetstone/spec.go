package whetstone

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/internal/eval/spec"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/ui"
)

var specCmd = &cobra.Command{
	Use:   "spec",
	Short: "Inspect, edit, and approve stored skill specifications",
}

var specShowCmd = &cobra.Command{
	Use:   "show <skill>",
	Short: "Print the latest stored spec",
	Args:  cobra.ExactArgs(1),
	RunE:  func(cmd *cobra.Command, args []string) error { return RunSpecShow(args[0]) },
}

var specEditCmd = &cobra.Command{
	Use:   "edit <skill>",
	Short: "Open the spec in $EDITOR and store the result as a new version",
	Args:  cobra.ExactArgs(1),
	RunE:  func(cmd *cobra.Command, args []string) error { return RunSpecEdit(args[0]) },
}

var specApproveCmd = &cobra.Command{
	Use:   "approve <skill>",
	Short: "Mark the latest stored spec version approved",
	Args:  cobra.ExactArgs(1),
	RunE:  func(cmd *cobra.Command, args []string) error { return RunSpecApprove(args[0]) },
}

// RunSpecShow prints the latest stored spec for a skill.
func RunSpecShow(skill string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	sp, version, err := st.LoadSpec(skill)
	if err != nil {
		return err
	}

	if ui.JSONMode {
		return ui.PrintJSON(struct {
			Version  int             `json:"version"`
			Approved bool            `json:"approved"`
			Spec     *spec.SkillSpec `json:"spec"`
		}{Version: version, Approved: isApproved(st, skill, version), Spec: sp})
	}

	ui.Info("%s spec version %d (%s)", skill, version, approvalWord(isApproved(st, skill, version)))
	return sp.Save(os.Stdout)
}

// RunSpecEdit opens the spec YAML in $EDITOR. It stores the edited result as
// a new, approved version. Approval is per version, so a prior version's
// approval never carries forward to one nobody has reviewed.
func RunSpecEdit(skill string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	// Load through the store rather than reading the file directly, so editing
	// works even when the on-disk YAML is missing — the database row is the
	// record of truth for what the spec currently is.
	sp, _, err := st.LoadSpec(skill)
	if err != nil {
		return err
	}
	path, err := st.SpecPath(skill)
	if err != nil {
		return err
	}
	// Restore the file only when it is absent. SaveSpec rewrites it after
	// every store, so an existing file is either identical to the stored spec
	// or carries hand edits the author has not saved yet — and rewriting it
	// unconditionally would silently discard the second case.
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if err := writeSpecFile(path, sp); err != nil {
			return err
		}
	}

	if err := runEditor(path); err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("whetstone spec edit: %w", err)
	}
	edited, err := spec.Load(f)
	_ = f.Close()
	if err != nil {
		return fmt.Errorf("whetstone spec edit: the edited spec does not parse: %w", err)
	}
	if edited.Name != skill {
		return fmt.Errorf("whetstone spec edit: the edited spec is named %q, not %q; renaming a skill this way would orphan its history",
			edited.Name, skill)
	}

	// An edit that changed nothing must not store a version. Approval is per
	// version, so storing one would revoke the current approval and demand a
	// re-review of a document nobody touched.
	same, err := sameSpec(sp, edited)
	if err != nil {
		return err
	}
	if same {
		if ui.JSONMode {
			return ui.PrintJSON(map[string]any{"skill": skill, "unchanged": true})
		}
		ui.Info("%s spec is unchanged; nothing stored", skill)
		return nil
	}

	version, err := st.SaveSpec(edited)
	if err != nil {
		return err
	}
	// The person who edited this document is the author, and `whetstone new`
	// approves a model-drafted spec without asking. Refusing to approve a
	// hand-edited one is backwards, and it makes `whetstone eval` refuse a
	// spec that carries more review than the drafted one it accepts.
	if err := st.ApproveSpec(edited.Name, version); err != nil {
		return err
	}

	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"skill": skill, "version": version, "approved": true})
	}
	ui.Success("stored and approved %s spec version %d", skill, version)
	return nil
}

// RunSpecApprove marks the latest stored version of a spec approved.
func RunSpecApprove(skill string) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	_, version, err := st.LoadSpec(skill)
	if err != nil {
		return err
	}
	if err := st.ApproveSpec(skill, version); err != nil {
		return err
	}

	if ui.JSONMode {
		return ui.PrintJSON(map[string]any{"skill": skill, "version": version, "approved": true})
	}
	ui.Success("approved %s spec version %d", skill, version)
	return nil
}

// loadApprovedSpec returns the latest stored spec only if that exact version
// is approved. Generation and suite drafting both run from it, and running
// either from an unapproved spec is how an unreviewed change reaches a bundle.
func loadApprovedSpec(st *store.Store, skill string) (*spec.SkillSpec, error) {
	sp, version, err := st.LoadSpec(skill)
	if err != nil {
		return nil, err
	}
	if !isApproved(st, skill, version) {
		return nil, fmt.Errorf("%s spec version %d is not approved; review it with `whetstone spec show %s` and approve it with `whetstone spec approve %s`",
			skill, version, skill, skill)
	}
	return sp, nil
}

// isApproved reports whether a specific stored version is approved. A history
// lookup that fails is reported as unapproved: the gate must fail closed.
func isApproved(st *store.Store, skill string, version int) bool {
	history, err := st.SpecHistory(skill)
	if err != nil {
		return false
	}
	for _, r := range history {
		if r.Version == version {
			return r.Approved
		}
	}
	return false
}

func approvalWord(approved bool) string {
	if approved {
		return "approved"
	}
	return "not approved"
}

// sameSpec compares two specs by their rendered YAML, which is what the store
// keeps and what the author edited — comparing the structs directly would call
// a reordered but semantically identical document a change.
func sameSpec(a, b *spec.SkillSpec) (bool, error) {
	var ay, by bytes.Buffer
	if err := a.Save(&ay); err != nil {
		return false, err
	}
	if err := b.Save(&by); err != nil {
		return false, err
	}
	return bytes.Equal(ay.Bytes(), by.Bytes()), nil
}

// writeSpecFile rewrites the human-editable YAML from the stored spec.
func writeSpecFile(path string, sp *spec.SkillSpec) error {
	var buf bytes.Buffer
	if err := sp.Save(&buf); err != nil {
		return err
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("whetstone spec edit: writing %s: %w", path, err)
	}
	return nil
}

// runEditor opens path in $EDITOR, wired to the terminal so an interactive
// editor works.
func runEditor(path string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		return fmt.Errorf("whetstone spec edit: neither $EDITOR nor $VISUAL is set; edit %s directly", path)
	}

	// $EDITOR is conventionally a command line ("code --wait"), not a bare
	// binary, so the first field is the program and the rest are its flags.
	fields := strings.Fields(editor)
	args := make([]string, 0, len(fields))
	args = append(args, fields[1:]...)
	args = append(args, path)

	cmd := exec.Command(fields[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("whetstone spec edit: %s: %w", editor, err)
	}
	return nil
}

func init() {
	specCmd.AddCommand(specShowCmd, specEditCmd, specApproveCmd)
	rootCmd.AddCommand(specCmd)
}
