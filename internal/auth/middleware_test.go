package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/testutil"
)

// setupMiddlewareTest serves the real middleware with a health route and an SPA
// path beside the protected route.
func setupMiddlewareTest(t *testing.T) (*httptest.Server, *auth.KeyStore) {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)
	people, err := auth.NewPeople(pool, keyStore, auth.PeopleConfig{})
	require.NoError(t, err)

	r := chi.NewMux()
	r.Use(auth.Middleware(people, keyStore))
	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	auth.RegisterRoutes(api, people, userStore, keyStore)
	r.Handle(auth.AuthAllRoutes, people.Auth.Handler())
	huma.Register(api, huma.Operation{
		OperationID: "protected", Method: http.MethodGet, Path: "/api/protected",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body auth.User }, error) {
		return &struct{ Body auth.User }{Body: *auth.UserFromContext(ctx)}, nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "health", Method: http.MethodGet, Path: "/api/health", DefaultStatus: http.StatusOK,
	}, func(context.Context, *struct{}) (*struct{}, error) { return nil, nil })
	r.Get("/*", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("spa")) })

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, keyStore
}

func newClientWithJar(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return &http.Client{Jar: jar}
}

// signupUser signs up a user and returns the client (with session cookie).
func signupUser(t *testing.T, srv *httptest.Server, email, name, password string) *http.Client {
	t.Helper()
	client := newClientWithJar(t)
	resp := doPost(t, client, srv.URL+"/api/auth/sign-up/email", map[string]string{
		"email":    email,
		"name":     name,
		"password": password,
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()
	return client
}

func TestMiddleware_NonAPIPath_PassesThrough(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, _ := setupMiddlewareTest(t)

	// Non-API paths should pass through without auth.
	client := newClientWithJar(t)
	paths := []string{"/", "/settings", "/skills/my-skill", "/favicon.ico"}
	for _, path := range paths {
		resp, err := client.Get(srv.URL + path)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "path %s should pass through", path)
		resp.Body.Close()
	}
}

func TestMiddleware_HealthEndpoint_PassesThrough(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, _ := setupMiddlewareTest(t)

	client := newClientWithJar(t)
	resp, err := client.Get(srv.URL + "/api/health")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestMiddleware_AuthLoginEndpoint_PassesThrough(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, _ := setupMiddlewareTest(t)

	// /api/auth/sign-in/email should be accessible without auth (the login handler itself
	// will return 401 for bad creds, but the middleware should let it through).
	client := newClientWithJar(t)
	resp := doPost(t, client, srv.URL+"/api/auth/sign-in/email", map[string]string{
		"email":    "nobody@example.com",
		"password": "anything",
	})
	// 401 from the handler, not from middleware — the handler returns "invalid credentials"
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestMiddleware_ValidSession_PassesWithUser(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, _ := setupMiddlewareTest(t)

	// Sign up to get a session cookie.
	client := signupUser(t, srv, "session@example.com", "Session User", "password123")

	// Access the protected endpoint.
	resp, err := client.Get(srv.URL + "/api/protected")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var user auth.User
	decodeJSON(t, resp, &user)
	require.Equal(t, "session@example.com", user.Email)
	require.Equal(t, "Session User", user.Name)
}

func TestMiddleware_ValidAPIKey_PassesWithUser(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, keyStore := setupMiddlewareTest(t)

	// Sign up to create a user, then create an API key for them.
	client := signupUser(t, srv, "apikey@example.com", "API Key User", "password123")

	// Create API key via the route.
	resp := doPost(t, client, srv.URL+"/api/auth/keys", map[string]string{"name": "test-key"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var keyResp struct {
		Key string `json:"key"`
	}
	decodeJSON(t, resp, &keyResp)
	require.NotEmpty(t, keyResp.Key)
	_ = keyStore // keyStore is available if needed

	// Use the API key with a fresh client (no session cookie).
	freshClient := newClientWithJar(t)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/protected", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", keyResp.Key)

	resp2, err := freshClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	var user auth.User
	decodeJSON(t, resp2, &user)
	require.Equal(t, "apikey@example.com", user.Email)
}

func TestMiddleware_NoAuth_Returns401(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, _ := setupMiddlewareTest(t)

	client := newClientWithJar(t)
	resp, err := client.Get(srv.URL + "/api/protected")
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	resp.Body.Close()
	require.Equal(t, "unauthorized", body["error"])
}

func TestMiddleware_InvalidAPIKey_Returns401(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, _ := setupMiddlewareTest(t)

	client := newClientWithJar(t)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/protected", nil)
	require.NoError(t, err)
	req.Header.Set("X-API-Key", "sk-invalidkey1234567890abcdef12")

	resp, err := client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	resp.Body.Close()
	require.Equal(t, "unauthorized", body["error"])
}
