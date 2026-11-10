package cli

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/skael-dev/skael/cli/agents"
	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/pattern"
	"github.com/skael-dev/skael/internal/ui"
)

// Source kinds a followed source can have. A skill name has none.
const (
	sourcePattern = "pattern"
	sourceSet     = "set"
)

// sourceLabel names a followed source in messages and in state.
func sourceLabel(s config.SkillEntry) string {
	if s.Kind == sourceSet {
		return "set " + s.Name
	}
	return s.Name
}

// sourceOf reads a command's argument and --set flag as a followed source.
func sourceOf(arg, set, scope string) (config.SkillEntry, error) {
	switch {
	case set != "" && arg != "":
		return config.SkillEntry{}, fmt.Errorf("give a skill, a pattern or --set, not two of them")
	case set != "":
		return config.SkillEntry{Name: set, Kind: sourceSet, Scope: scope}, nil
	case arg == "":
		return config.SkillEntry{}, fmt.Errorf("give a skill name, a pattern such as payments:*, or --set <name>")
	case strings.Contains(arg, "*"):
		if err := pattern.Validate(arg); err != nil {
			return config.SkillEntry{}, err
		}
		return config.SkillEntry{Name: arg, Kind: sourcePattern, Scope: scope}, nil
	}
	return config.SkillEntry{Name: arg, Scope: scope}, nil
}

// skillTarget is one folder a skill is written to.
type skillTarget struct {
	Label string
	Base  string
}

// skillTargets is where skills go: .agents/skills always, which Codex, Cursor
// and OpenCode read, and .claude/skills when Claude Code is detected, because
// it reads nothing else. The same rule holds at user scope.
func skillTargets(detected []agents.Agent, scope Scope, home, root string) []skillTarget {
	base := root
	if scope == ScopeUser {
		base = home
	}
	out := []skillTarget{{Label: "agents", Base: filepath.Join(base, ".agents", "skills")}}
	for _, a := range detected {
		if a.Name() == (&agents.ClaudeCode{}).Name() {
			out = append(out, skillTarget{Label: a.Name(), Base: filepath.Join(base, ".claude", "skills")})
		}
	}
	return out
}

// wantedSkill is one skill the followed sources ask for.
type wantedSkill struct {
	Name, Checksum string
	Version        int
	Scope          Scope
	Sources        []string
}

// expansion is what the followed sources resolve to.
type expansion struct {
	Skills    map[string]*wantedSkill
	Conflicts []string
	Warnings  []string
}

// expand resolves every followed source into skills. A name or a pattern
// resolves against the released skills the caller can read; a set resolves on
// the server. Two sources that ask for different versions of one skill are a
// conflict, and the first source keeps the skill.
func expand(c *client.Client, cfg *config.Config, scopeFlag string) (*expansion, error) {
	out := &expansion{Skills: map[string]*wantedSkill{}}
	var manifest []client.ManifestEntry
	loadManifest := func() error {
		if manifest != nil {
			return nil
		}
		m, err := c.GetManifest()
		if err != nil {
			return err
		}
		manifest = m
		return nil
	}
	want := func(src config.SkillEntry, name string, version int, checksum string) {
		label := sourceLabel(src)
		w, ok := out.Skills[name]
		if !ok {
			out.Skills[name] = &wantedSkill{Name: name, Version: version, Checksum: checksum,
				Scope: resolveScope(src.Scope, firstNonEmpty(scopeFlag, cfg.Scope)), Sources: []string{label}}
			return
		}
		if w.Version != version || w.Checksum != checksum {
			out.Conflicts = append(out.Conflicts, fmt.Sprintf("%s: %s wants v%d, %s wants v%d",
				name, strings.Join(w.Sources, ", "), w.Version, label, version))
			return
		}
		if !slices.Contains(w.Sources, label) {
			w.Sources = append(w.Sources, label)
		}
	}
	for _, src := range cfg.Skills {
		switch src.Kind {
		case sourceSet:
			res, err := c.ResolveSet(src.Name)
			if err != nil {
				return nil, fmt.Errorf("resolve set %s: %w", src.Name, err)
			}
			for _, e := range res.Value.Excluded {
				out.Warnings = append(out.Warnings, fmt.Sprintf("set %s leaves out %s@%s: %s", src.Name, e.Name, e.Requested, e.Reason))
			}
			for _, e := range res.Value.Entries {
				want(src, e.Name, e.Version, e.SHA256)
			}
		case sourcePattern:
			if err := loadManifest(); err != nil {
				return nil, err
			}
			for _, m := range manifest {
				if pattern.Matches(src.Name, m.Name) {
					want(src, m.Name, m.Version, m.Checksum)
				}
			}
		default:
			if err := loadManifest(); err != nil {
				return nil, err
			}
			i := slices.IndexFunc(manifest, func(m client.ManifestEntry) bool { return m.Name == src.Name })
			if i < 0 {
				out.Warnings = append(out.Warnings, fmt.Sprintf("skill %q has no released version you can read; skipping", src.Name))
				continue
			}
			want(src, manifest[i].Name, manifest[i].Version, manifest[i].Checksum)
		}
	}
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// reconcileOptions are a reconcile's switches.
type reconcileOptions struct {
	DryRun bool
	Quiet  bool
	// Agent limits writes to one target: "claude-code" for .claude/skills,
	// any other agent name for .agents/skills.
	Agent string
	Scope string
}

// reconcileResult is what a reconcile did.
type reconcileResult struct {
	Synced, Failed, Pruned, Total int
	Targets                       []skillTarget
	Scope                         Scope
	Conflicts                     []string
}

// reconcile makes the machine match the followed sources: it writes each
// wanted skill to every target, moves a skill an older version placed
// elsewhere, and removes what no source wants any more. State records each
// skill's placements and sources.
func reconcile(c *client.Client, cfg *config.Config, dir string, o reconcileOptions) (*reconcileResult, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot determine working directory: %w", err)
	}
	root := gitRoot(wd)
	detected := agents.DetectIn(home)
	defaultScope := resolveScope(o.Scope, cfg.Scope)

	exp, err := expand(c, cfg, o.Scope)
	if err != nil {
		return nil, err
	}
	if !o.Quiet {
		for _, w := range exp.Warnings {
			ui.Warn("%s", w)
		}
	}

	targetsFor := func(scope Scope) []skillTarget {
		all := skillTargets(detected, scope, home, root)
		if o.Agent == "" {
			return all
		}
		var picked []skillTarget
		for _, t := range all {
			if (o.Agent == "claude-code") == (t.Label == "claude-code") {
				picked = append(picked, t)
			}
		}
		return picked
	}

	state, err := config.ReadState(dir)
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	local := map[string]config.SyncedSkill{}
	for _, s := range state.Skills {
		local[s.Name] = s
	}

	res := &reconcileResult{Total: len(exp.Skills), Targets: targetsFor(defaultScope), Scope: defaultScope, Conflicts: exp.Conflicts}
	var kept []config.SyncedSkill
	names := make([]string, 0, len(exp.Skills))
	for n := range exp.Skills {
		names = append(names, n)
	}
	slices.Sort(names)

	for _, name := range names {
		w := exp.Skills[name]
		targets := targetsFor(w.Scope)
		paths := make([]string, len(targets))
		for i, t := range targets {
			paths[i] = filepath.Join(t.Base, name)
		}
		prev, had := local[name]
		if had && prev.Version == w.Version && prev.Checksum == w.Checksum && samePaths(prev.Placements, paths) {
			prev.Sources = w.Sources
			kept = append(kept, prev)
			continue
		}
		ver := fmt.Sprintf("v%d", w.Version)
		if o.DryRun {
			if !o.Quiet {
				if had {
					ui.Download(name, ver)
				} else {
					ui.New(name, ver)
				}
			}
			if had {
				kept = append(kept, prev)
			}
			continue
		}
		archive, err := c.DownloadVersion(name, w.Version)
		if err != nil {
			ui.Errorf("download %s %s: %s", name, ver, err)
			res.Failed++
			if had {
				kept = append(kept, prev)
			}
			continue
		}
		if sum := fmt.Sprintf("%x", sha256.Sum256(archive)); w.Checksum != "" && sum != w.Checksum {
			ui.Errorf("checksum mismatch for %s (expected %s, got %s)", name, w.Checksum[:16], sum[:16])
			res.Failed++
			if had {
				kept = append(kept, prev)
			}
			continue
		}
		var placements []config.Placement
		for i, t := range targets {
			if err := extractSkillAtomically(archive, paths[i]); err != nil {
				ui.Errorf("extract %s to %s: %s", name, t.Base, err)
				continue
			}
			placements = append(placements, config.Placement{Agent: t.Label, Path: paths[i], Scope: string(w.Scope)})
		}
		if len(placements) == 0 {
			res.Failed++
			if had {
				kept = append(kept, prev)
			}
			continue
		}
		// A skill an older version placed in an agent's own folder moves here.
		for _, p := range prev.Placements {
			if !slices.Contains(paths, p.Path) {
				if err := os.RemoveAll(p.Path); err != nil {
					ui.Warn("remove the old copy of %s at %s: %s", name, p.Path, err)
				}
			}
		}
		if !o.Quiet {
			if had {
				ui.Download(name, ver)
			} else {
				ui.New(name, ver)
			}
		}
		res.Synced++
		kept = append(kept, config.SyncedSkill{Name: name, Version: w.Version, Checksum: w.Checksum, Placements: placements, Sources: w.Sources})
	}

	for _, s := range state.Skills {
		if _, wanted := exp.Skills[s.Name]; wanted {
			continue
		}
		if o.DryRun {
			if !o.Quiet {
				ui.Warn("would remove %s", s.Name)
			}
			kept = append(kept, s)
			continue
		}
		for _, p := range s.Placements {
			if err := os.RemoveAll(p.Path); err != nil {
				ui.Errorf("remove %s from %s: %s", s.Name, p.Path, err)
			} else if !o.Quiet {
				ui.Warn("removed %s from %s", s.Name, p.Path)
			}
		}
		res.Pruned++
	}

	if o.DryRun {
		return res, nil
	}
	if err := config.WriteState(dir, &config.SyncState{LastSync: time.Now().UTC().Format(time.RFC3339), Skills: kept}); err != nil {
		return nil, fmt.Errorf("write state: %w", err)
	}
	return res, nil
}

func samePaths(placements []config.Placement, paths []string) bool {
	if len(placements) != len(paths) {
		return false
	}
	for _, p := range placements {
		if !slices.Contains(paths, p.Path) {
			return false
		}
	}
	return true
}
