package gate_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/gate"
	"github.com/skael-dev/skael/internal/scan"
)

// The corpus under testdata/corpus is excerpted from anthropics/skills at
// b29e7cf — real, first-party, not written to prove anything about this gate,
// which is exactly why it is here. Importing it is what caught two rules that
// blocked ordinary material unappealably: a proximity-matching credential rule
// that refused security documentation, and the secret-env-var rule that
// refused an authenticated API call.
//
// It is vendored rather than fetched. A test that reaches github.com is a
// flaky test, and the valuable artefact was never the network call — it was
// the outcome table. Each directory carries the file that actually drives the
// decision (trimmed to the relevant lines) plus its SKILL.md, so the fixtures
// stay small while remaining real text nobody rewrote.
//
// A change here means a class or severity moved. That may be correct — but it
// changes what a public skill corpus can publish through this registry, so it
// wants a ruling rather than a fixture update.
func TestPublicCorpusOutcomes(t *testing.T) {
	cases := []struct {
		dir  string
		want gate.Outcome
		why  string
	}{
		{"brand-guidelines", gate.Allow, "prose only; nothing should fire"},
		{"pdf", gate.Allow, "scripted document handling is not a security finding"},
		{"algorithmic-art", gate.NeedsReview,
			"dynamic code execution in a template is a guess a sandbox can settle"},
		{"skill-creator", gate.NeedsReview,
			"eval() in a bundled viewer is appealable, not credential theft"},
		{"claude-api-curl-auth", gate.NeedsReview,
			"an authenticated API call must be reviewable: this rule cannot tell " +
				"a skill using a key from one stealing it"},
		// This bundle used to Block, and it was the sole reason
		// anthropics/skills' claude-api could not be imported at all: two
		// SECRET_EXPOSURE hits (class secret, unappealable) on
		// `access_token: "xoxp-..."`, a redacted placeholder with the ellipsis
		// literally in the file, and on a getenv() call. The secret class and
		// its unappealability did not change — the detection did.
		{"claude-api-sdk-readme", gate.AllowWithWarning,
			"a redacted placeholder and an env lookup are not hardcoded secrets"},
		// The corpus must prove the gate still blocks, not only that it lets
		// things through. A real key of two different shapes, unappealable.
		{"hardcoded-secret", gate.Block,
			"a genuine hardcoded credential is the one thing this gate exists to refuse"},
	}

	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			rep, err := scan.ScanDir("testdata/corpus/" + tc.dir)
			require.NoError(t, err)

			d := gate.Decide(*rep, nil, gate.OwnerState{}, gate.Policy{})
			assert.Equal(t, tc.want, d.Outcome, "%s: %+v", tc.why, d.Reasons)

			// Anything not expected to Block must also carry no unappealable
			// finding: an unappealable reason inside a held version is a
			// latent Block waiting for one more finding to arrive.
			if tc.want == gate.Block {
				var unappealable bool
				for _, r := range d.Reasons {
					if r.Class == string(gate.ClassSecret) || r.Class == string(gate.ClassExfiltration) {
						unappealable = true
					}
				}
				assert.True(t, unappealable,
					"%s must block on an unappealable finding, not an appealable pile-up: %+v", tc.dir, d.Reasons)
			}
			if tc.want != gate.Block {
				for _, r := range d.Reasons {
					assert.NotEqual(t, string(gate.ClassExfiltration), r.Class,
						"%s: an unappealable finding makes this skill permanently unpublishable: %+v", tc.dir, r)
					assert.NotEqual(t, string(gate.ClassSecret), r.Class,
						"%s: an unappealable finding makes this skill permanently unpublishable: %+v", tc.dir, r)
				}
			}
		})
	}
}
