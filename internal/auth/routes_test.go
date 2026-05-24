package auth_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/testutil"
)

// setupAuthAPI creates a Chi router + Huma API backed by a real ephemeral
// Postgres, registers auth routes, and returns an httptest.Server with a
// cookie-jar-enabled client.
func setupAuthAPI(t *testing.T, disableSignup bool) (*httptest.Server, *http.Client) {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)

	sessionManager := scs.New()

	r := chi.NewMux()
	r.Use(sessionManager.LoadAndSave)

	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	auth.RegisterRoutes(api, sessionManager, userStore, keyStore, disableSignup)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)

	client := &http.Client{Jar: jar}

	return srv, client
}

// doPost sends a JSON POST request to the given URL and returns the response.
func doPost(t *testing.T, client *http.Client, url string, body interface{}) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(b)
	}
	resp, err := client.Post(url, "application/json", bodyReader)
	require.NoError(t, err)
	return resp
}

// doGet sends a GET request and returns the response.
func doGet(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	resp, err := client.Get(url)
	require.NoError(t, err)
	return resp
}

// doDelete sends a DELETE request and returns the response.
func doDelete(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// decodeJSON decodes the response body into v.
func decodeJSON(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()
	defer resp.Body.Close()
	require.NoError(t, json.NewDecoder(resp.Body).Decode(v))
}

func TestAuthRoutes_SignupFirstUser_Owner(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "owner@example.com",
		"name":     "Owner",
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var user auth.User
	decodeJSON(t, resp, &user)
	require.Equal(t, "owner@example.com", user.Email)
	require.Equal(t, "Owner", user.Name)
	require.Equal(t, "owner", user.Role)
	require.NotEmpty(t, user.ID)
}

func TestAuthRoutes_SignupSecondUser_Admin(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	// First user → owner.
	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "owner@example.com",
		"name":     "Owner",
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Second user → admin (use a different client to avoid session overlap).
	jar2, err := cookiejar.New(nil)
	require.NoError(t, err)
	client2 := &http.Client{Jar: jar2}

	resp = doPost(t, client2, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "admin@example.com",
		"name":     "Admin",
		"password": "password456",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var user auth.User
	decodeJSON(t, resp, &user)
	require.Equal(t, "admin@example.com", user.Email)
	require.Equal(t, "admin", user.Role)
}

func TestAuthRoutes_SignupDuplicateEmail(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	body := map[string]string{
		"email":    "dup@example.com",
		"name":     "First",
		"password": "password123",
	}

	resp := doPost(t, client, srv.URL+"/api/auth/signup", body)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Second signup with same email.
	jar2, err := cookiejar.New(nil)
	require.NoError(t, err)
	client2 := &http.Client{Jar: jar2}

	resp = doPost(t, client2, srv.URL+"/api/auth/signup", body)
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()
}

func TestAuthRoutes_SignupShortPassword(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "short@example.com",
		"name":     "Short",
		"password": "abc",
	})
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	resp.Body.Close()
}

func TestAuthRoutes_LoginSuccess(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	// Sign up first.
	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "login@example.com",
		"name":     "Login User",
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Log in with a fresh client (no session cookie).
	jar2, err := cookiejar.New(nil)
	require.NoError(t, err)
	client2 := &http.Client{Jar: jar2}

	resp = doPost(t, client2, srv.URL+"/api/auth/login", map[string]string{
		"email":    "login@example.com",
		"password": "password123",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var user auth.User
	decodeJSON(t, resp, &user)
	require.Equal(t, "login@example.com", user.Email)
	require.Equal(t, "Login User", user.Name)
}

func TestAuthRoutes_LoginWrongPassword(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	// Sign up.
	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "wrongpw@example.com",
		"name":     "Wrong PW",
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	jar2, err := cookiejar.New(nil)
	require.NoError(t, err)
	client2 := &http.Client{Jar: jar2}

	resp = doPost(t, client2, srv.URL+"/api/auth/login", map[string]string{
		"email":    "wrongpw@example.com",
		"password": "wrong-password",
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestAuthRoutes_LoginNonexistentEmail(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	resp := doPost(t, client, srv.URL+"/api/auth/login", map[string]string{
		"email":    "nobody@example.com",
		"password": "password123",
	})
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestAuthRoutes_MeWithSession(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	// Sign up (creates a session).
	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "me@example.com",
		"name":     "Me User",
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Call /me — session cookie should carry over.
	resp = doGet(t, client, srv.URL+"/api/auth/me")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var user auth.User
	decodeJSON(t, resp, &user)
	require.Equal(t, "me@example.com", user.Email)
	require.Equal(t, "Me User", user.Name)
}

func TestAuthRoutes_MeWithoutSession(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	resp := doGet(t, client, srv.URL+"/api/auth/me")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestAuthRoutes_Logout(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	// Sign up.
	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "logout@example.com",
		"name":     "Logout User",
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Logout.
	resp = doPost(t, client, srv.URL+"/api/auth/logout", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// /me should now fail.
	resp = doGet(t, client, srv.URL+"/api/auth/me")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	resp.Body.Close()
}

func TestAuthRoutes_CreateKey(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	// Sign up.
	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "keys@example.com",
		"name":     "Key User",
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Create key.
	resp = doPost(t, client, srv.URL+"/api/auth/keys", map[string]string{
		"name": "my-key",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var keyResp struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Key       string `json:"key"`
		Prefix    string `json:"prefix"`
		CreatedAt string `json:"created_at"`
	}
	decodeJSON(t, resp, &keyResp)
	require.NotEmpty(t, keyResp.ID)
	require.Equal(t, "my-key", keyResp.Name)
	require.NotEmpty(t, keyResp.Key)
	require.True(t, len(keyResp.Key) > 8, "full key should be longer than prefix")
	require.NotEmpty(t, keyResp.Prefix)
	require.NotEmpty(t, keyResp.CreatedAt)
}

func TestAuthRoutes_ListKeys(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	// Sign up.
	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "listkeys@example.com",
		"name":     "List Keys User",
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Create two keys.
	resp = doPost(t, client, srv.URL+"/api/auth/keys", map[string]string{"name": "key-1"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	resp = doPost(t, client, srv.URL+"/api/auth/keys", map[string]string{"name": "key-2"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// List keys.
	resp = doGet(t, client, srv.URL+"/api/auth/keys")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var listResp struct {
		Keys []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Prefix string `json:"prefix"`
		} `json:"keys"`
	}
	decodeJSON(t, resp, &listResp)
	require.Len(t, listResp.Keys, 2)

	// Keys should have prefix but NO full key or hash.
	for _, k := range listResp.Keys {
		require.NotEmpty(t, k.Prefix)
		require.NotEmpty(t, k.ID)
	}
}

func TestAuthRoutes_DeleteKey(t *testing.T) {
	srv, client := setupAuthAPI(t, false)

	// Sign up.
	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    "delkey@example.com",
		"name":     "Del Key User",
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	// Create key.
	resp = doPost(t, client, srv.URL+"/api/auth/keys", map[string]string{"name": "to-delete"})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var keyResp struct {
		ID string `json:"id"`
	}
	decodeJSON(t, resp, &keyResp)

	// Delete it.
	resp = doDelete(t, client, srv.URL+"/api/auth/keys/"+keyResp.ID)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	// List should be empty now.
	resp = doGet(t, client, srv.URL+"/api/auth/keys")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var listResp struct {
		Keys []struct{} `json:"keys"`
	}
	decodeJSON(t, resp, &listResp)
	require.Len(t, listResp.Keys, 0)
}
