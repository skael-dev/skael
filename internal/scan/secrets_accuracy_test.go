package scan_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/scan"
)

// secretFindings returns the SECRET_EXPOSURE findings for one line of content.
func secretFindings(t *testing.T, line string) []scan.Finding {
	t.Helper()
	rep := scan.ScanContent("README.md", line+"\n")
	var out []scan.Finding
	for _, f := range rep.Findings {
		if f.Rule == "SECRET_EXPOSURE" {
			out = append(out, f)
		}
	}
	return out
}

// TestRealSecretsStillBlock is the direction that must never regress. Every
// shape the rule set detects is listed here, one case per pattern, with a
// realistic value. A false negative here is the failure the whole gate exists
// to prevent — unlike a false positive, nothing downstream catches it.
func TestRealSecretsStillBlock(t *testing.T) {
	// Provider-prefixed fixture values are assembled from fragments at
	// runtime rather than written as contiguous literals. The assembled
	// string reaching the scanner is byte-for-byte identical to a real
	// token of that shape — GitHub's push-protection scanner (and others)
	// key on the vendor prefix, so splitting after the first character of
	// the prefix is enough to keep no detectable pattern in the source
	// file while changing nothing about what gets tested.
	openAIKey := "sk-" + "proj-" + "Ab3xK9pQ2rTvWyZ1mN4jH7gF5dS8aE6cB0uI"
	anthropicKey := "sk-" + "ant-api03-" + "Ab3xK9pQ2rTvWyZ1mN4jH7gF5dS8aE6cB0"
	awsKeyID := "A" + "KIA3XK9PQ2RTVWYZ1MN"
	githubPAT := "g" + "hp_A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8"
	githubFineGrainedPAT := "github_" + "pat_11ABCDEFG0aBcDeFgHiJkLmNoPqRsTuVwXyZ"
	gitlabPAT := "g" + "lpat-Ab3xK9pQ2rTvWyZ1mN4j"
	stripeKey := "sk_" + "live_51Ab3xK9pQ2rTvWyZ1mN4jH7gF"
	googleKey := "A" + "IzaSyA1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q"
	slackToken := "xoxp-" + "2468013579-1357924680-Ab3xK9pQ2rTv"

	cases := []struct {
		name string
		line string
	}{
		{"openai project key", `OPENAI = "` + openAIKey + `"`},
		{"anthropic key", `key = "` + anthropicKey + `"`},
		{"aws access key id", `aws_key = "` + awsKeyID + `"`},
		{"github pat", `token = "` + githubPAT + `"`},
		{"github fine-grained pat", `token = "` + githubFineGrainedPAT + `"`},
		{"gitlab pat", `token = "` + gitlabPAT + `"`},
		{"stripe live key", `stripe = "` + stripeKey + `"`},
		{"google api key", `gkey = "` + googleKey + `"`},
		{"slack token", `access_token: "` + slackToken + `"`},
		{"private key block", `-----BEGIN RSA PRIVATE KEY-----`},
		{"bearer token", `Authorization: Bearer Ab3xK9pQ2rTvWyZ1mN4jH7gF5dS8aE6c`},
		{"generic hex api key", `api_key = "8f14e45fceea167a5a36dedd4bea2543a1b2c3d4"`},
		{"generic password", `password = "hunter2correcthorse"`},
		// Bare tokens in prose, matched only by their own prefixed rule and
		// not by the generic assignment rule — so each prefixed pattern is
		// load-bearing on its own, not shadowed by the generic one.
		{"bare slack token", `Paste ` + slackToken + ` into the form.`},
		{"bare anthropic key", `Paste ` + anthropicKey + ` into the form.`},
		{"bare aws key id", `Paste ` + awsKeyID + ` into the form.`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := secretFindings(t, tc.line)
			require.NotEmpty(t, got, "a real hardcoded secret must still be detected")
			for _, f := range got {
				assert.Equal(t, string(scan.ClassSecret), f.Class,
					"a real secret must stay in the unappealable class")
			}
		})
	}
}

// TestEnvLookupsAndPlaceholdersAreNotSecrets is the accuracy fix. An
// environment lookup names a secret without containing one, and a redacted
// documentation placeholder is not a credential. Both blocked a real
// first-party skill from being imported at all, because the secret class is
// unappealable by design — which is correct policy applied to a wrong
// detection.
func TestEnvLookupsAndPlaceholdersAreNotSecrets(t *testing.T) {
	lines := []string{
		`'x-api-key: ' . getenv('ANTHROPIC_API_KEY'),`,
		`api_key = os.environ['ANTHROPIC_API_KEY']`,
		`api_key = os.environ["ANTHROPIC_API_KEY"]`,
		`apiKey: process.env.ANTHROPIC_API_KEY,`,
		`api_key = ENV['ANTHROPIC_API_KEY']`,
		`ApiKey: os.Getenv("ANTHROPIC_API_KEY"),`,
		`access_token: "xoxp-...",`,
		`api_key = "sk-ant-..."`,
		`password = "xxxxxxxx"`,
		`client_secret = "****"`,
	}
	for _, line := range lines {
		t.Run(line, func(t *testing.T) {
			assert.Empty(t, secretFindings(t, line),
				"an env lookup or a redacted placeholder is not a hardcoded secret")
		})
	}
}
