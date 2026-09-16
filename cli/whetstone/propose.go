package whetstone

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/whetstone/gen"
	"github.com/skael-dev/skael/internal/eval/llm"
	"github.com/skael-dev/skael/internal/eval/propose"
	"github.com/skael-dev/skael/internal/eval/store"
	"github.com/skael-dev/skael/internal/ui"
)

// ProposeRequest is one `whetstone propose` invocation.
type ProposeRequest struct {
	ContestID string
	// Candidate names whose losses to work from, as skill@version. Empty
	// resolves to the only candidate whose skill is in the working tree.
	Candidate string
	Endpoint  string
	APIKey    string
	// Apply writes the change. False prints the diff and writes nothing.
	Apply bool
}

// proseExtensions are the files a proposal may edit. A generator that can
// write a .sh puts machine-written code in a bundle an agent runs in a
// sandbox; the scanner would catch it, which is the wrong reason to generate
// it.
var proseExtensions = map[string]bool{".md": true, ".txt": true}

// RunProposeWith reads what a candidate missed and writes one change.
func RunProposeWith(ctx context.Context, st *store.Store, g llm.Gateway, req ProposeRequest) (*propose.Result, error) {
	if g == nil {
		return nil, fmt.Errorf("whetstone propose: needs an LLM gateway; run `whetstone doctor` to check your setup")
	}
	endpoint, apiKey, err := resolveRegistry(req.Endpoint, req.APIKey)
	if err != nil {
		return nil, err
	}

	contest, err := client.New(endpoint, apiKey).GetContest(req.ContestID)
	if err != nil {
		return nil, fmt.Errorf("whetstone propose: fetching contest %s: %w", req.ContestID, err)
	}

	label, skillName, err := pickCandidate(st, contest, req.Candidate)
	if err != nil {
		return nil, err
	}

	losses := lossesFor(contest, label)
	if len(losses) == 0 {
		return nil, fmt.Errorf("whetstone propose: %s recorded no loss in this contest, so there is nothing to work from", label)
	}

	skillDir, err := st.SkillDir(skillName)
	if err != nil {
		return nil, err
	}
	files, err := readProse(skillDir)
	if err != nil {
		return nil, err
	}

	in := propose.Input{Skill: skillName, Files: files, Losses: losses}
	sp, _, specErr := st.LoadSpec(skillName)
	if specErr == nil && sp != nil {
		in.Description = sp.Description
	}

	ui.Info("working from %s lost in contest %s", plural(len(losses), "task"), req.ContestID)
	res, err := propose.Run(ctx, g, in)
	if err != nil {
		return nil, err
	}

	printProposal(files, res)

	if !req.Apply {
		ui.Info("nothing written; rerun with %s to apply", ui.Code("--apply"))
		return res, nil
	}
	if err := applyProposal(st, skillDir, skillName, sp != nil && specErr == nil, res); err != nil {
		return nil, err
	}
	ui.Success("wrote the change to %s", skillDir)
	ui.Info("measure it with %s", ui.Code("skael publish && skael contest "+skillName+" "+label))
	return res, nil
}

// pickCandidate resolves whose losses to work from. With one candidate whose
// skill this working tree holds, there is nothing to ask.
func pickCandidate(st *store.Store, c *client.Contest, want string) (label, skillName string, err error) {
	if want != "" {
		for _, cand := range c.Candidates {
			if cand.Label == want {
				return cand.Label, cand.Skill, nil
			}
		}
		return "", "", fmt.Errorf("whetstone propose: %s is not a candidate in this contest", want)
	}

	var matches []client.ContestCandidate
	for _, cand := range c.Candidates {
		if inWorkspace(st, cand.Skill) {
			matches = append(matches, cand)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].Label, matches[0].Skill, nil
	case 0:
		return "", "", fmt.Errorf("whetstone propose: none of this contest's candidates is a skill in this workspace; name one with --candidate")
	default:
		var labels []string
		for _, m := range matches {
			labels = append(labels, m.Label)
		}
		return "", "", fmt.Errorf("whetstone propose: this workspace holds %s; name one with --candidate", strings.Join(labels, " and "))
	}
}

// inWorkspace reports whether this workspace holds the skill's bundle.
// store.SkillDir builds a path without checking it, so the directory has to be
// looked at: otherwise every candidate matches and the command asks which of
// two skills the author meant when it holds only one.
func inWorkspace(st *store.Store, skill string) bool {
	dir, err := st.SkillDir(skill)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "SKILL.md"))
	return err == nil
}

// lossesFor keeps the named candidate's losses. A task lost is recorded
// whatever the verdict was, so a candidate that won still has something to
// work from — often the most useful moment to improve a skill.
func lossesFor(c *client.Contest, label string) []propose.Loss {
	var out []propose.Loss
	for _, l := range c.Losses {
		if l.Label != label {
			continue
		}
		out = append(out, propose.Loss{TaskID: l.TaskID, Missed: l.Missed, Evidence: l.Evidence})
	}
	return out
}

// readProse collects the bundle's prose. A contest compares whole bundles, so
// a skill often loses a task because a reference is wrong.
func readProse(dir string) ([]propose.File, error) {
	var files []propose.File
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !proseExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, propose.File{Path: filepath.ToSlash(rel), Body: string(body)})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("whetstone propose: reading %s: %w", dir, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// applyProposal writes the spec before the bundle. The generator renders the
// frontmatter from the spec, so the next `whetstone gen` overwrites a bundle
// patched on its own.
func applyProposal(st *store.Store, skillDir, skillName string, hasSpec bool, res *propose.Result) error {
	if hasSpec && res.Description != "" {
		sp, _, err := st.LoadSpec(skillName)
		if err != nil {
			return err
		}
		sp.Description = res.Description
		version, err := st.SaveSpec(sp)
		if err != nil {
			return err
		}
		if err := st.ApproveSpec(skillName, version); err != nil {
			return err
		}
		if err := gen.RewriteDescription(skillDir, res.Description); err != nil {
			return err
		}
		ui.Info("stored and approved %s spec version %d", skillName, version)
	}

	for _, c := range res.Changes {
		path := filepath.Join(skillDir, filepath.FromSlash(c.Path))
		if err := os.WriteFile(path, []byte(c.Body), 0o644); err != nil {
			return fmt.Errorf("whetstone propose: writing %s: %w", c.Path, err)
		}
	}
	return nil
}

func printProposal(before []propose.File, res *propose.Result) {
	current := map[string]string{}
	for _, f := range before {
		current[f.Path] = f.Body
	}

	fmt.Fprintf(os.Stderr, "\n%s\n\n", res.Rationale)
	for _, c := range res.Changes {
		fmt.Fprintf(os.Stderr, "%s\n", ui.Bold(c.Path))
		fmt.Fprint(os.Stderr, diff(current[c.Path], c.Body))
		fmt.Fprintln(os.Stderr)
	}
	if res.Description != "" {
		fmt.Fprintf(os.Stderr, "%s\n  %s\n\n", ui.Bold("description"), res.Description)
	}
}

// diff prints changed lines with context. A whole-file dump hides the change,
// and this is read rather than applied by a machine.
func diff(before, after string) string {
	oldLines := strings.Split(before, "\n")
	newLines := strings.Split(after, "\n")

	keep := map[string]int{}
	for _, l := range oldLines {
		keep[l]++
	}

	var b strings.Builder
	for _, l := range newLines {
		if keep[l] > 0 {
			keep[l]--
			continue
		}
		fmt.Fprintf(&b, "  %s %s\n", ui.Accent("+"), l)
	}

	seen := map[string]int{}
	for _, l := range newLines {
		seen[l]++
	}
	for _, l := range oldLines {
		if seen[l] > 0 {
			seen[l]--
			continue
		}
		fmt.Fprintf(&b, "  - %s\n", l)
	}
	return b.String()
}

var proposeReq ProposeRequest

var proposeCmd = &cobra.Command{
	Use:   "propose <contest-id>",
	Short: "Write one change from what a candidate missed in a contest",
	Long: "Read the expectations a candidate failed, and the grader's evidence, and write\n" +
		"one change to the skill's prose that addresses all of them. It publishes\n" +
		"nothing and starts nothing: measure the change with `skael contest`.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		proposeReq.ContestID = args[0]
		return RunPropose(cmd.Context(), proposeReq)
	},
}

func init() {
	proposeCmd.Flags().StringVar(&proposeReq.Candidate, "candidate", "", "Which candidate's losses to work from, as skill@version")
	proposeCmd.Flags().StringVar(&proposeReq.Endpoint, "endpoint", "", "Registry URL (defaults to SKAEL_ENDPOINT or ~/.skael/config.json)")
	proposeCmd.Flags().StringVar(&proposeReq.APIKey, "api-key", "", "Registry API key (defaults to SKAEL_API_KEY or ~/.skael/config.json)")
	proposeCmd.Flags().BoolVar(&proposeReq.Apply, "apply", false, "Write the change to the working tree")
	rootCmd.AddCommand(proposeCmd)
}

// RunPropose opens the workspace and gateway, then proposes.
func RunPropose(ctx context.Context, req ProposeRequest) error {
	st, err := openStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	g, err := newGateway(st.Cache())
	if err != nil {
		return err
	}
	_, err = RunProposeWith(ctx, st, g, req)
	return err
}
