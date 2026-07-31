package skill_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/skill"
)

// publishGateBody mirrors the publish endpoint's response body. It is declared
// here rather than exported from the package so that a field silently
// disappearing from the wire shape fails these tests.
type publishGateBody struct {
	skill.Version
	Created  bool          `json:"created"`
	Decision gate.Decision `json:"decision"`
}

// packSkill writes SKILL.md with the given body and packs it, after asserting
// that the scanner classifies it the way the test depends on. The assertion is
// the point: if a rule stops matching, or its class changes, these tests must
// fail loudly rather than quietly testing the clean path three times over.
func packSkill(t *testing.T, name, description, mdBody string, wantClass scan.Class, wantSeverity string) []byte {
	t.Helper()

	dir := t.TempDir()
	skillMD := strings.Join([]string{
		"---",
		"name: " + name,
		"description: " + description,
		"---",
		"# " + name,
		"",
		mdBody,
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0644))

	report, err := scan.ScanDir(dir)
	require.NoError(t, err)

	if wantClass == "" {
		require.Empty(t, report.Findings,
			"the clean fixture must produce no findings; it produced %+v", report.Findings)
	} else {
		found := false
		for _, f := range report.Findings {
			if scan.Class(f.Class) == wantClass && f.Severity == wantSeverity {
				found = true
			}
		}
		require.True(t, found,
			"fixture %q must produce a %s/%s finding for this test to mean anything; it produced %+v",
			name, wantClass, wantSeverity, report.Findings)
	}

	archiveBytes, _, _, err := skill.Pack(dir)
	require.NoError(t, err)
	return archiveBytes
}

// appealableBundle trips a high-severity execution finding: a guess the scanner
// makes from shape alone, which a sandboxed evaluation could overturn. The
// brief calls this the "heuristic" bundle; the rule set classes dynamic eval as
// `execution`, which is on the same appealable side of the line.
func appealableBundle(t *testing.T, name string) []byte {
	t.Helper()
	return packSkill(t, name, "an appealable fixture",
		"```sh\neval \"$(cat /tmp/payload)\"\n```",
		scan.ClassExecution, "high")
}

// secretBundle trips an unappealable secret finding.
func secretBundle(t *testing.T, name string) []byte {
	t.Helper()
	return packSkill(t, name, "an unappealable fixture",
		"Configure the client with AKIAIOSFODNN7EXAMPLE before running.",
		scan.ClassSecret, "critical")
}

// cleanBundle trips nothing at all.
func cleanBundle(t *testing.T, name string) []byte {
	t.Helper()
	return packSkill(t, name, "a clean fixture", "This is the skill body.", "", "")
}

// getSkill fetches a skill through the API.
func getSkill(t *testing.T, handler http.Handler, name string) skill.Skill {
	t.Helper()
	var sk skill.Skill
	rr := doJSON(t, handler, http.MethodGet, "/api/skills/"+name, nil, &sk)
	require.Equal(t, http.StatusOK, rr.Code, "get skill %q: %s", name, rr.Body.String())
	return sk
}

func TestPublishGate(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}

	var caller *auth.User
	handler := setupTestAPIAsUser(t, &caller)

	owner := &auth.User{ID: "00000000-0000-0000-0000-000000000001", Email: "owner@example.com", Role: auth.RoleOwner}
	member := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}

	t.Run("an appealable finding holds the version instead of refusing it", func(t *testing.T) {
		caller = member
		createSkill(t, handler, "gated", "gated")
		rr := postArchive(t, handler, "/api/skills/gated/versions", appealableBundle(t, "gated"))
		require.Equal(t, http.StatusCreated, rr.Code,
			"a held publish still created the version: %s", rr.Body.String())

		var body publishGateBody
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
		assert.Equal(t, gate.NeedsReview, body.Decision.Outcome)
		require.NotEmpty(t, body.Decision.Reasons)
		assert.NotEmpty(t, body.Decision.Reasons[0].Clears)
		assert.Equal(t, "needs_review", body.GateState)
		assert.Equal(t, 1, body.Version.Version, "the version number still advances")

		sk := getSkill(t, handler, "gated")
		assert.Equal(t, 0, sk.LatestVersion, "a held version is not served")
	})

	t.Run("an unappealable finding is refused even with an owner override", func(t *testing.T) {
		caller = owner
		createSkill(t, handler, "leaky", "leaky")
		rr := postArchive(t, handler, "/api/skills/leaky/versions?override=true", secretBundle(t, "leaky"))
		assert.Equal(t, http.StatusUnprocessableEntity, rr.Code, rr.Body.String())
		assert.Contains(t, rr.Body.String(), "unappealable")

		sk := getSkill(t, handler, "leaky")
		assert.Equal(t, 0, sk.LatestVersion)
		versions := listVersionNumbers(t, handler, "leaky")
		assert.Empty(t, versions, "a blocked publish creates no version row")
	})

	t.Run("a clean bundle publishes exactly as before", func(t *testing.T) {
		caller = member
		createSkill(t, handler, "fine", "fine")
		rr := postArchive(t, handler, "/api/skills/fine/versions", cleanBundle(t, "fine"))
		require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

		var body publishGateBody
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
		assert.Equal(t, gate.Allow, body.Decision.Outcome)
		assert.Empty(t, body.Decision.Reasons)
		assert.Equal(t, "released", body.GateState)

		sk := getSkill(t, handler, "fine")
		assert.Equal(t, 1, sk.LatestVersion, "the ordinary path is unchanged")
	})

	t.Run("an owner override releases an appealable finding", func(t *testing.T) {
		caller = owner
		createSkill(t, handler, "forced", "forced")
		rr := postArchive(t, handler, "/api/skills/forced/versions?override=true", appealableBundle(t, "forced"))
		require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

		var body publishGateBody
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
		assert.Equal(t, gate.Allow, body.Decision.Outcome)
		assert.NotEmpty(t, body.Decision.Reasons, "the findings are still reported, they are just not held on")
		assert.Equal(t, "released", body.GateState)

		sk := getSkill(t, handler, "forced")
		assert.Equal(t, 1, sk.LatestVersion)
	})

	t.Run("a held version is visible to its publisher in the version list", func(t *testing.T) {
		var listed struct {
			Versions []skill.Version `json:"versions"`
		}
		rr := doJSON(t, handler, http.MethodGet, "/api/skills/gated/versions", nil, &listed)
		require.Equal(t, http.StatusOK, rr.Code)
		require.Len(t, listed.Versions, 1)
		assert.Equal(t, "needs_review", listed.Versions[0].GateState)
		assert.NotEmpty(t, listed.Versions[0].GateDecision,
			"the list route must serialise the gate fields, not project a subset")
	})
}

// listVersionNumbers returns the version numbers a skill has, through the API.
func listVersionNumbers(t *testing.T, handler http.Handler, name string) []int {
	t.Helper()
	var listed struct {
		Versions []skill.Version `json:"versions"`
	}
	rr := doJSON(t, handler, http.MethodGet, "/api/skills/"+name+"/versions", nil, &listed)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	nums := make([]int, 0, len(listed.Versions))
	for _, v := range listed.Versions {
		nums = append(nums, v.Version)
	}
	return nums
}

// TestPublishHeldDoesNotOverwriteServedContent is the content-leak guard. The
// gate withholds the archive; it must also withhold the prose and metadata,
// or GET /api/skills/{name} serves the held version's body beside a
// latest_version that still points at the previous release.
func TestPublishHeldDoesNotOverwriteServedContent(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}

	var caller *auth.User
	handler := setupTestAPIAsUser(t, &caller)
	caller = &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "member@example.com", Role: auth.RoleMember}

	createSkill(t, handler, "leaky-body", "initial")

	// A released v1 establishes the served content and metadata. The author
	// and tags come from UpdateSpecFields, which the publish handler must also
	// skip for a held version.
	releasedDir := t.TempDir()
	releasedMD := strings.Join([]string{
		"---",
		"name: leaky-body",
		"description: the released description",
		"author: Released Author",
		"license: MIT",
		"tags: [released]",
		"---",
		"# leaky-body",
		"",
		"The released body.",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(releasedDir, "SKILL.md"), []byte(releasedMD), 0644))
	releasedArchive, _, _, err := skill.Pack(releasedDir)
	require.NoError(t, err)

	rr := postArchive(t, handler, "/api/skills/leaky-body/versions", releasedArchive)
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	released := getSkill(t, handler, "leaky-body")
	require.Equal(t, 1, released.LatestVersion)
	require.Contains(t, released.Content, "The released body.",
		"a released publish must update the served content")
	require.Equal(t, "the released description", released.Description)
	require.Equal(t, "Released Author", released.Author)
	require.Equal(t, []string{"released"}, released.Tags)
	releasedFrontmatter := string(released.Frontmatter)

	// A held v2 must change none of it.
	rr = postArchive(t, handler, "/api/skills/leaky-body/versions",
		appealableBundle(t, "leaky-body"))
	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	held := getSkill(t, handler, "leaky-body")
	assert.Equal(t, 1, held.LatestVersion, "a held version does not advance the pointer")
	assert.Equal(t, released.Content, held.Content,
		"a held version must not replace the served body")
	assert.NotContains(t, held.Content, "eval",
		"the held version's prose must not be served")
	assert.Equal(t, released.Description, held.Description,
		"a held version must not replace the served description")
	assert.Equal(t, releasedFrontmatter, string(held.Frontmatter),
		"a held version must not replace the served frontmatter")
	assert.Equal(t, "Released Author", held.Author,
		"a held version must not rewrite the skill's spec metadata")
	assert.Equal(t, []string{"released"}, held.Tags,
		"a held version must not rewrite the skill's tags")
}
