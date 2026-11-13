package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
)

// registry is a fake server: released skills at one version each, and sets
// that resolve to fixed versions.
type registry struct {
	archives map[string][]byte // "name@version" -> archive
	latest   map[string]int
	sets     map[string][][2]any // set -> [name, version]
}

func newRegistry(t *testing.T) *registry {
	return &registry{archives: map[string][]byte{}, latest: map[string]int{}, sets: map[string][][2]any{}}
}

func (r *registry) release(t *testing.T, name string, version int) {
	r.archives[fmt.Sprintf("%s@%d", name, version)] = packSkill(t, fmt.Sprintf("---\nname: %s\ndescription: v%d\n---\nbody %d\n", name, version, version))
	if version > r.latest[name] {
		r.latest[name] = version
	}
}

func (r *registry) sum(name string, version int) string {
	return fmt.Sprintf("%x", sha256.Sum256(r.archives[fmt.Sprintf("%s@%d", name, version)]))
}

func (r *registry) serve(t *testing.T) *client.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/api/sync/manifest":
			var out []client.ManifestEntry
			for n, v := range r.latest {
				out = append(out, client.ManifestEntry{Name: n, Version: v, Checksum: r.sum(n, v)})
			}
			_ = json.NewEncoder(w).Encode(out)
		case strings.HasPrefix(req.URL.Path, "/api/sets/") && strings.HasSuffix(req.URL.Path, "/resolve"):
			name := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/sets/"), "/resolve")
			res := client.Resolution{ID: "r", Set: name, Complete: true}
			for _, e := range r.sets[name] {
				n, v := e[0].(string), e[1].(int)
				res.Entries = append(res.Entries, client.ResolvedEntry{Name: n, Version: v, SHA256: r.sum(n, v)})
			}
			_ = json.NewEncoder(w).Encode(res)
		case strings.HasSuffix(req.URL.Path, "/download"):
			parts := strings.Split(req.URL.Path, "/")
			_, _ = w.Write(r.archives[parts[3]+"@"+parts[5]])
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, "k")
}

// machine is a home with Claude Code detected and a repo as the working dir.
func machine(t *testing.T) (home, repo, dir string) {
	home, repo = t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".git"), 0o755))
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(repo)
	return home, repo, filepath.Join(home, ".skael")
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// A pattern follows every released skill under it, into .agents/skills and,
// with Claude Code detected, .claude/skills.
func TestReconcile_APatternInstallsEveryMatchIntoBothFolders(t *testing.T) {
	_, repo, dir := machine(t)
	reg := newRegistry(t)
	reg.release(t, "payments:refunds", 1)
	reg.release(t, "search:index", 1)
	c := reg.serve(t)
	cfg := &config.Config{Skills: []config.SkillEntry{{Name: "payments:*", Kind: sourcePattern}}}

	res, err := reconcile(c, cfg, dir, reconcileOptions{Quiet: true})
	require.NoError(t, err)
	require.Equal(t, 1, res.Synced)
	for _, base := range []string{".agents", ".claude"} {
		require.True(t, exists(filepath.Join(repo, base, "skills", config.SkillFolder("payments:refunds"), "SKILL.md")), base)
		require.False(t, exists(filepath.Join(repo, base, "skills", config.SkillFolder("search:index"))), base)
	}

	reg.release(t, "payments:payouts", 1)
	res, err = reconcile(c, cfg, dir, reconcileOptions{Quiet: true})
	require.NoError(t, err)
	require.Equal(t, 1, res.Synced, "a new skill under a followed pattern arrives on the next sync")
	require.True(t, exists(filepath.Join(repo, ".claude", "skills", config.SkillFolder("payments:payouts"), "SKILL.md")))
}

// Removing a set keeps a skill another source still wants.
func TestReconcile_ASkillLeavesOnlyWhenNoSourceWantsIt(t *testing.T) {
	_, repo, dir := machine(t)
	reg := newRegistry(t)
	reg.release(t, "alpha", 1)
	reg.release(t, "beta", 1)
	reg.sets["team"] = [][2]any{{"alpha", 1}, {"beta", 1}}
	c := reg.serve(t)
	cfg := &config.Config{Skills: []config.SkillEntry{{Name: "team", Kind: sourceSet}, {Name: "alpha"}}}
	_, err := reconcile(c, cfg, dir, reconcileOptions{Quiet: true})
	require.NoError(t, err)

	state, err := config.ReadState(dir)
	require.NoError(t, err)
	for _, s := range state.Skills {
		if s.Name == "alpha" {
			require.ElementsMatch(t, []string{"set team", "alpha"}, s.Sources)
		}
	}

	cfg.RemoveSource(sourceSet, "team")
	res, err := reconcile(c, cfg, dir, reconcileOptions{Quiet: true})
	require.NoError(t, err)
	require.Equal(t, 1, res.Pruned)
	require.True(t, exists(filepath.Join(repo, ".agents", "skills", "alpha")))
	require.False(t, exists(filepath.Join(repo, ".agents", "skills", "beta")))
	require.False(t, exists(filepath.Join(repo, ".claude", "skills", "beta")))
}

// Two sources pinning different versions of one skill are a conflict, named.
func TestExpand_NamesAVersionConflict(t *testing.T) {
	machine(t)
	reg := newRegistry(t)
	reg.release(t, "alpha", 1)
	reg.release(t, "alpha", 2)
	reg.sets["pinned"] = [][2]any{{"alpha", 1}}
	c := reg.serve(t)
	cfg := &config.Config{Skills: []config.SkillEntry{{Name: "alpha"}, {Name: "pinned", Kind: sourceSet}}}

	exp, err := expand(c, cfg, "")
	require.NoError(t, err)
	require.Len(t, exp.Conflicts, 1)
	require.Contains(t, exp.Conflicts[0], "alpha: alpha wants v2, set pinned wants v1")
}

// A copy an older version placed in an agent's own folder moves on sync.
func TestReconcile_MovesAnOldPlacement(t *testing.T) {
	_, repo, dir := machine(t)
	reg := newRegistry(t)
	reg.release(t, "alpha", 1)
	c := reg.serve(t)
	old := filepath.Join(repo, ".cursor", "skills", "alpha")
	require.NoError(t, os.MkdirAll(old, 0o755))
	require.NoError(t, config.WriteState(dir, &config.SyncState{Skills: []config.SyncedSkill{{
		Name: "alpha", Version: 1, Checksum: reg.sum("alpha", 1),
		Placements: []config.Placement{{Agent: "cursor", Path: old, Scope: "project"}},
	}}}))

	_, err := reconcile(c, &config.Config{Skills: []config.SkillEntry{{Name: "alpha"}}}, dir, reconcileOptions{Quiet: true})
	require.NoError(t, err)
	require.False(t, exists(old), "the old copy must go")
	require.True(t, exists(filepath.Join(repo, ".agents", "skills", "alpha", "SKILL.md")))
}
