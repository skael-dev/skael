package auth_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// setupAuthAPI creates a Chi router + Huma API backed by a real ephemeral
// Postgres, registers auth routes, and returns an httptest.Server with a
// cookie-jar-enabled client.
func setupAuthAPI(t *testing.T, _ bool) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := setupAuthAPIAuthenticated(t)
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	return srv, &http.Client{Jar: jar}
}

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

func TestAuthRoutes_CreateKey(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, client := setupAuthAPI(t, false)

	// Sign up.
	resp := doPost(t, client, srv.URL+"/api/auth/sign-up/email", map[string]string{
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
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, client := setupAuthAPI(t, false)

	// Sign up.
	resp := doPost(t, client, srv.URL+"/api/auth/sign-up/email", map[string]string{
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
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, client := setupAuthAPI(t, false)

	// Sign up.
	resp := doPost(t, client, srv.URL+"/api/auth/sign-up/email", map[string]string{
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
