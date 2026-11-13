package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/skael-dev/skael/cli/agents"
	"github.com/skael-dev/skael/cli/client"
	"github.com/skael-dev/skael/cli/config"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/ui"
	"github.com/spf13/cobra"
)

const (
	bundleRoot         = ".agents/skills"
	bundleManifestName = "manifest.json"
	// These caps bound the memory and time a hostile bundle can cost.
	maxBundleArchive  = 64 << 20
	maxBundleUnpacked = 4 << 30
)

var pullCmd = &cobra.Command{
	Use:   "pull --set <name>",
	Short: "Resolve a set and write its skills into a directory",
	Long: `Resolve a set to exact versions, download its bundle, verify each archive
against the resolved digest, and write the skills under <dir>/.agents/skills/,
and under <dir>/.claude/skills/ when Claude Code is detected. Pull writes
once: it records nothing, and sync never follows it. Use "skael add --set"
to follow a set.

Other entries in that directory stay. Skills named by the previous pull's
manifest and absent from this one are removed.

Exit codes: 10 means the set is incomplete, 11 means a digest did not match.
Both write nothing.`,
	Args: cobra.NoArgs,
	RunE: runPull,
}

var (
	pullSet     string
	pullDir     string
	pullNoHooks bool
)

func init() {
	pullCmd.Flags().StringVar(&pullSet, "set", "", "Name of the set to pull")
	pullCmd.Flags().StringVar(&pullDir, "dir", ".", "Directory that receives .agents/skills")
	pullCmd.Flags().BoolVar(&pullNoHooks, "no-hooks", false, "Write files only and change no agent config")
	_ = pullCmd.MarkFlagRequired("set")
	rootCmd.AddCommand(pullCmd)
}

func runPull(cmd *cobra.Command, args []string) error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return notConfigured(err)
	}
	c := client.New(cfg.Endpoint, cfg.APIKey)

	resolved, err := c.ResolveSet(pullSet)
	if err != nil {
		return apiFailure(err)
	}
	res := resolved.Value
	if !res.Complete {
		return incompleteSet(&res)
	}

	body, err := c.DownloadBundle(pullSet, res.ID)
	if err != nil {
		return apiFailure(err)
	}
	defer body.Close()
	manifest, archives, err := readBundle(body, &res)
	if err != nil {
		return apiFailure(fmt.Errorf("read bundle: %w", err))
	}
	if ee := verifyBundle(&res, manifest, archives); ee != nil {
		return ee
	}

	target, err := filepath.Abs(filepath.Join(pullDir, filepath.FromSlash(bundleRoot)))
	if err != nil {
		return usageError("invalid_path", fmt.Sprintf("invalid --dir: %s", err), "")
	}
	if err := installBundle(target, &res, manifest, archives); err != nil {
		return failure("write_error", fmt.Sprintf("write %s: %s", target, err), "")
	}
	// Claude Code reads only .claude/skills, so it gets the same bundle there.
	if home, err := os.UserHomeDir(); err == nil && (&agents.ClaudeCode{}).Detected(home) {
		claude, err := filepath.Abs(filepath.Join(pullDir, ".claude", "skills"))
		if err != nil {
			return usageError("invalid_path", fmt.Sprintf("invalid --dir: %s", err), "")
		}
		if err := installBundle(claude, &res, manifest, archives); err != nil {
			return failure("write_error", fmt.Sprintf("write %s: %s", claude, err), "")
		}
	}

	hookFailures := 0
	if !pullNoHooks {
		hookFailures, err = installPullHooks(cfg)
		if err != nil {
			return failure("hook_error", err.Error(), "")
		}
	}

	if ui.JSONMode {
		if err := ui.PrintJSON(struct {
			Set          string                 `json:"set"`
			ResolutionID string                 `json:"resolution_id"`
			Dir          string                 `json:"dir"`
			Skills       []client.ResolvedEntry `json:"skills"`
		}{res.Set, res.ID, target, res.Entries}); err != nil {
			return err
		}
	} else {
		ui.Success("Pulled %s: %d %s to %s", res.Set, len(res.Entries), plural(len(res.Entries), "skill", "skills"), target)
	}
	if hookFailures > 0 {
		return failure("hook_error", fmt.Sprintf("files written, but %d hook %s failed", hookFailures, plural(hookFailures, "step", "steps")), "skael hook install")
	}
	return nil
}

func incompleteSet(res *client.Resolution) *exitError {
	lines := make([]string, len(res.Excluded))
	for i, e := range res.Excluded {
		lines[i] = fmt.Sprintf("%s@%s: %s", e.Name, e.Requested, e.Reason)
	}
	return &exitError{
		exit: exitIncompleteSet, code: "incomplete_set",
		msg:     fmt.Sprintf("set %q is incomplete: nothing was written", res.Set),
		context: strings.Join(lines, "\n    "),
		extra:   map[string]any{"excluded": res.Excluded},
	}
}

func bundleArchiveName(e client.ResolvedEntry) string {
	return fmt.Sprintf("%s/.archives/%s@%d.tar.gz", bundleRoot, e.Name, e.Version)
}

// readBundle keeps the manifest and the archives the resolution names. It skips
// the unpacked trees, because nothing can verify them.
func readBundle(r io.Reader, res *client.Resolution) ([]byte, map[string][]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, nil, err
	}
	defer gz.Close()

	wanted := make(map[string]string, len(res.Entries))
	for _, e := range res.Entries {
		wanted[bundleArchiveName(e)] = e.Name
	}
	manifestName := path.Join(bundleRoot, bundleManifestName)

	var manifest []byte
	archives := make(map[string][]byte, len(res.Entries))
	tr := tar.NewReader(io.LimitReader(gz, maxBundleUnpacked))
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return manifest, archives, nil
		}
		if err != nil {
			return nil, nil, err
		}
		skillName, isArchive := wanted[hdr.Name]
		if hdr.Name != manifestName && !isArchive {
			continue
		}
		if hdr.Size > maxBundleArchive {
			return nil, nil, fmt.Errorf("%s is %d bytes, over the %d byte limit", hdr.Name, hdr.Size, int64(maxBundleArchive))
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, nil, err
		}
		if isArchive {
			archives[skillName] = data
		} else {
			manifest = data
		}
	}
}

func digestMismatch(skillName, expected, actual string) *exitError {
	return &exitError{
		exit: exitDigestMismatch, code: "digest_mismatch",
		msg:   fmt.Sprintf("digest mismatch for %s: expected %s, got %s: nothing was written", skillName, expected, actual),
		extra: map[string]any{"skill": skillName, "expected": expected, "actual": actual},
	}
}

// verifyBundle checks the bundle against the resolve result, which is the only
// source of truth here. An archive or manifest entry that is missing cannot be
// verified, so it counts as a mismatch.
func verifyBundle(res *client.Resolution, manifestRaw []byte, archives map[string][]byte) *exitError {
	for _, e := range res.Entries {
		data, ok := archives[e.Name]
		if !ok {
			return digestMismatch(e.Name, e.SHA256, "no archive in bundle")
		}
		sum := sha256.Sum256(data)
		if actual := hex.EncodeToString(sum[:]); actual != e.SHA256 {
			return digestMismatch(e.Name, e.SHA256, actual)
		}
		if !filepath.IsLocal(e.Name) || strings.ContainsAny(e.Name, `/\`) || e.Name == bundleManifestName {
			return failure("invalid_skill_name", fmt.Sprintf("resolution names an unsafe skill %q", e.Name), "")
		}
	}

	var manifest client.BundleManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		return digestMismatch(bundleManifestName, res.ID, "unreadable manifest")
	}
	if manifest.ResolutionID != res.ID {
		return digestMismatch(bundleManifestName, res.ID, manifest.ResolutionID)
	}
	if len(manifest.Skills) != len(res.Entries) {
		return digestMismatch(bundleManifestName, fmt.Sprintf("%d skills", len(res.Entries)), fmt.Sprintf("%d skills", len(manifest.Skills)))
	}
	for i, s := range manifest.Skills {
		e := res.Entries[i]
		if s.Name != e.Name || s.Version != e.Version || s.SHA256 != e.SHA256 {
			return digestMismatch(e.Name, e.SHA256, fmt.Sprintf("manifest says %s@%d %s", s.Name, s.Version, s.SHA256))
		}
	}
	return nil
}

// installBundle builds the whole new directory beside target and swaps it in
// with two renames, so a failure at any earlier step leaves target untouched.
func installBundle(target string, res *client.Resolution, manifest []byte, archives map[string][]byte) error {
	// A rename would replace a symlinked target rather than write through it.
	if real, err := filepath.EvalSymlinks(target); err == nil {
		target = real
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".skills-staging-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0o755); err != nil {
		return err
	}

	for _, e := range res.Entries {
		dest := filepath.Join(stage, e.Name)
		if err := os.Mkdir(dest, 0o755); err != nil {
			return err
		}
		if err := skill.Unpack(bytes.NewReader(archives[e.Name]), dest); err != nil {
			return fmt.Errorf("%s: %w", e.Name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(stage, bundleManifestName), manifest, 0o644); err != nil {
		return err
	}
	if err := keepForeignEntries(target, stage); err != nil {
		return err
	}

	backup := stage + ".old"
	if err := os.Rename(target, backup); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Rename(stage, target)
	}
	if err := os.Rename(stage, target); err != nil {
		return errors.Join(err, os.Rename(backup, target))
	}
	if err := os.RemoveAll(backup); err != nil {
		ui.Warn("remove %s: %s", backup, err)
	}
	return nil
}

// keepForeignEntries copies whatever the previous pull did not write into stage,
// so `skael init` and hand-placed skills survive a pull.
func keepForeignEntries(target, stage string) error {
	entries, err := os.ReadDir(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	previous := map[string]bool{bundleManifestName: true}
	if raw, err := os.ReadFile(filepath.Join(target, bundleManifestName)); err == nil {
		var m client.BundleManifest
		if json.Unmarshal(raw, &m) == nil {
			for _, s := range m.Skills {
				previous[s.Name] = true
			}
		}
	}

	for _, entry := range entries {
		dest := filepath.Join(stage, entry.Name())
		if previous[entry.Name()] {
			continue
		}
		if _, err := os.Lstat(dest); err == nil {
			continue
		}
		if err := copyEntry(filepath.Join(target, entry.Name()), dest, entry); err != nil {
			return err
		}
	}
	return nil
}

func copyEntry(src, dest string, entry os.DirEntry) error {
	switch {
	case entry.Type()&os.ModeSymlink != 0:
		link, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if os.Symlink(link, dest) == nil {
			return nil
		}
		// Windows refuses a symlink without Developer Mode: keep a copy.
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.CopyFS(dest, os.DirFS(src))
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, info.Mode().Perm())
	case entry.IsDir():
		return os.CopyFS(dest, os.DirFS(src))
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, info.Mode().Perm())
}

// installPullHooks installs activation tracking only. Auto-sync is left out
// because it runs `skael sync`, which manages a different skill list.
func installPullHooks(cfg *config.Config) (int, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, fmt.Errorf("cannot determine home directory: %w", err)
	}
	detected := agents.DetectIn(home)
	if len(detected) == 0 {
		ui.Info("No agents detected: no hooks installed")
		return 0, nil
	}
	return installActivationHooks(config.DefaultDir(), home, detected), nil
}
