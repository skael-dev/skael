package gitsync_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/gitsync"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

const pem = "-----BEGIN RSA PRIVATE KEY-----\nsecret-key-body\n-----END RSA PRIVATE KEY-----\n"

func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app-manifests/the-code/conversions" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":42,"slug":"skael-acme","name":"Skael Acme",
			"html_url":"https://github.com/apps/skael-acme","client_id":"Iv1.abc",
			"client_secret":"client-secret-value","webhook_secret":"webhook-secret-value",
			"pem":` + jsonString(pem) + `,"owner":{"login":"acme"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func jsonString(s string) string {
	return `"` + strings.ReplaceAll(s, "\n", `\n`) + `"`
}

// The manifest flow stores the App with every secret sealed, opens them for
// use, and accepts each state once.
func TestComplete_StoresTheAppSealedAndConsumesTheState(t *testing.T) {
	ctx := context.Background()
	pool := testutil.SetupTestDB(t)
	keys, err := platform.ParseKeyring("k1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		t.Fatal(err)
	}
	store := gitsync.NewStore(pool, keys)
	gh := fakeGitHub(t)

	state, err := store.StartManifest(ctx, "https://github.com", gh.URL, "admin@example.com")
	if err != nil {
		t.Fatalf("StartManifest: %v", err)
	}
	app, err := store.Complete(ctx, gh.Client(), state, "the-code")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if app.AppID != 42 || app.OwnerLogin != "acme" || app.InstallURL() != "https://github.com/apps/skael-acme/installations/new" {
		t.Errorf("app = %+v", app)
	}

	var row string
	if err := pool.QueryRow(ctx, `SELECT private_key || webhook_secret || client_secret FROM github_app`).Scan(&row); err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"secret-key-body", "webhook-secret-value", "client-secret-value"} {
		if strings.Contains(row, plain) {
			t.Errorf("the stored row holds %q in plaintext", plain)
		}
	}

	loaded, err := store.App(ctx)
	if err != nil || loaded == nil {
		t.Fatalf("App: %v", err)
	}
	if string(loaded.PrivateKey) != pem || string(loaded.WebhookSecret) != "webhook-secret-value" {
		t.Error("the stored secrets do not open to what GitHub returned")
	}

	if _, err := store.Complete(ctx, gh.Client(), state, "the-code"); !errors.Is(err, gitsync.ErrManifestState) {
		t.Errorf("a reused state = %v, want ErrManifestState", err)
	}
}

func TestManifest_TargetsTheOrganizationAndThisInstance(t *testing.T) {
	postURL, manifest, err := gitsync.Manifest(gitsync.ManifestRequest{
		Name: "Skael", Organization: "acme", WebBaseURL: "https://github.com", PublicURL: "https://skael.acme.dev/",
	}, "st")
	if err != nil {
		t.Fatal(err)
	}
	if postURL != "https://github.com/organizations/acme/settings/apps/new?state=st" {
		t.Errorf("post URL = %s", postURL)
	}
	for _, want := range []string{
		`"url":"https://skael.acme.dev/api/github/webhook"`,
		`"redirect_url":"https://skael.acme.dev/api/admin/github/callback"`,
		`"checks":"write"`, `"pull_requests":"write"`, `"contents":"write"`,
	} {
		if !strings.Contains(manifest, want) {
			t.Errorf("manifest lacks %s: %s", want, manifest)
		}
	}
}
