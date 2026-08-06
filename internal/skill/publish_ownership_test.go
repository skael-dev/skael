package skill_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
)

type fakeOwners struct {
	state   gate.OwnerState
	claimed []string
}

func (f *fakeOwners) ResolveForPublish(_ context.Context, _ string, _ *auth.User) (gate.OwnerState, error) {
	return f.state, nil
}

func (f *fakeOwners) ClaimOnFirstPublish(_ context.Context, name string, _ *auth.User) error {
	f.claimed = append(f.claimed, name)
	return nil
}

// publishCleanBundle registers the publish routes with the given fake
// ownership resolver, creates skillName, publishes a clean bundle to it as an
// authenticated member, and returns the decoded response body.
func publishCleanBundle(t *testing.T, skillName string, owners skill.OwnerResolver) publishGateBody {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	store := skill.NewStore(pool)

	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	member := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "carol@acme.com", Role: auth.RoleMember}

	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			req = req.WithContext(auth.ContextWithUser(req.Context(), member))
			next.ServeHTTP(w, req)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	skill.RegisterRoutes(api, r, store, storage, skill.RouteOptions{Ownership: owners})

	createSkill(t, r, skillName, "a clean fixture")

	dir := t.TempDir()
	skillMD := strings.Join([]string{
		"---",
		"name: " + skillName,
		"description: a clean fixture",
		"---",
		"# " + skillName,
		"",
		"This is the skill body.",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0644))
	archiveBytes, _, _, err := skill.Pack(dir)
	require.NoError(t, err)

	rr := postArchive(t, r, "/api/skills/"+skillName+"/versions", archiveBytes)
	require.Equal(t, 201, rr.Code, "publish %q: %s", skillName, rr.Body.String())

	var body publishGateBody
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	return body
}

func TestPublishByNonOwnerIsHeldForOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	owners := &fakeOwners{state: gate.OwnerState{
		Evaluated: true, IsOwner: false, RulePattern: "payments:*",
		Owners: []gate.OwnerRef{{ID: "u1", Name: "Alice", Email: "alice@acme.com"}},
	}}

	body := publishCleanBundle(t, "payments:refunds", owners)

	if body.Decision.Outcome != gate.NeedsReview {
		t.Fatalf("outcome = %s, want needs_review", body.Decision.Outcome)
	}
	if body.Decision.Ownership == nil || len(body.Decision.Ownership.Owners) != 1 {
		t.Fatal("response does not name the owners; the publisher cannot tell who unblocks them")
	}
	if body.GateState != "needs_review" {
		t.Fatalf("gate_state = %q, want needs_review", body.GateState)
	}
}

func TestPublishByOwnerReleasesImmediately(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	owners := &fakeOwners{state: gate.OwnerState{Evaluated: true, IsOwner: true, RulePattern: "payments:*"}}
	body := publishCleanBundle(t, "payments:refunds", owners)
	if body.Decision.Outcome != gate.Allow {
		t.Fatalf("outcome = %s, want allow", body.Decision.Outcome)
	}
}

// O6: a brand new name claims its publisher as owner. This is what makes
// every skill published after the upgrade guarded from birth.
func TestFirstPublishClaimsOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	owners := &fakeOwners{state: gate.OwnerState{Evaluated: true, Unowned: true}}
	publishCleanBundle(t, "brand:new", owners)
	if len(owners.claimed) != 1 || owners.claimed[0] != "brand:new" {
		t.Fatalf("claimed = %v, want [brand:new]", owners.claimed)
	}
}

// O6 must only fire on a skill's first version. Gating on owner.Unowned alone
// (without also checking this is version 1) means the first person to
// publish an UPDATE to a pre-existing unowned skill — the exact situation on
// an upgraded instance with 200 pre-existing skills and no ownership rules
// yet — would silently become its sole owner, and the next contributor's
// publish would be held for review. That breaks the "unowned does not hold a
// publish" promise this feature depends on for upgrades being a no-op.
func TestSecondPublishToUnownedSkillDoesNotClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	owners := &fakeOwners{state: gate.OwnerState{Evaluated: true, Unowned: true}}

	pool := testutil.SetupTestDB(t)
	store := skill.NewStore(pool)

	storage, err := platform.NewLocalStorage(t.TempDir())
	require.NoError(t, err)

	member := &auth.User{ID: "00000000-0000-0000-0000-000000000003", Email: "carol@acme.com", Role: auth.RoleMember}

	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			req = req.WithContext(auth.ContextWithUser(req.Context(), member))
			next.ServeHTTP(w, req)
		})
	})
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	skill.RegisterRoutes(api, r, store, storage, skill.RouteOptions{Ownership: owners})

	skillName := "legacy:preexisting"
	createSkill(t, r, skillName, "a clean fixture")

	publishVersion := func(body string) publishGateBody {
		dir := t.TempDir()
		skillMD := strings.Join([]string{
			"---",
			"name: " + skillName,
			"description: a clean fixture",
			"---",
			"# " + skillName,
			"",
			body,
		}, "\n")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0644))
		archiveBytes, _, _, err := skill.Pack(dir)
		require.NoError(t, err)

		rr := postArchive(t, r, "/api/skills/"+skillName+"/versions", archiveBytes)
		require.Equal(t, 201, rr.Code, "publish %q: %s", skillName, rr.Body.String())

		var out publishGateBody
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out))
		return out
	}

	v1 := publishVersion("This is version one.")
	require.Equal(t, 1, v1.Version.Version)
	require.Len(t, owners.claimed, 1, "the FIRST version of a pre-existing unowned skill must still claim ownership")
	require.Equal(t, skillName, owners.claimed[0])

	v2 := publishVersion("This is version two.")
	require.Equal(t, 2, v2.Version.Version)
	require.Len(t, owners.claimed, 1,
		"a SECOND publish to an already-unowned skill must NOT claim ownership — "+
			"that would make an upgrade with pre-existing unowned skills silently mint an owner on the next update")
}

// O6's exception: if a rule already covers the name, the rule wins and the
// publisher must NOT be claimed as owner — otherwise anyone publishes
// payments:anything to get inside the payments namespace.
func TestFirstPublishUnderAnExistingRuleDoesNotClaim(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	owners := &fakeOwners{state: gate.OwnerState{
		Evaluated: true, IsOwner: false, RulePattern: "payments:*",
		Owners: []gate.OwnerRef{{ID: "u1", Email: "alice@acme.com"}},
	}}
	body := publishCleanBundle(t, "payments:brandnew", owners)
	if len(owners.claimed) != 0 {
		t.Fatalf("claimed = %v, want empty — a covering rule must win over first-publish", owners.claimed)
	}
	if body.Decision.Outcome != gate.NeedsReview {
		t.Fatalf("outcome = %s, want needs_review", body.Decision.Outcome)
	}
}
