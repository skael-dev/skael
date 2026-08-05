package auth_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// Any authenticated member may search — a namespace owner adding a teammate
// is a member, and locking this to admins kills delegation entirely.
func TestSearchIsOpenToMembers(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)

	_, owner := signUp(t, srv, "owner@example.com", "Owner")
	_ = owner
	memberClient, _ := signUp(t, srv, "member@example.com", "Member Person")

	resp := doGet(t, memberClient, srv.URL+"/api/users/search?q=Member")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// It returns identity and nothing else. No role, no timestamps, no key data.
func TestSearchReturnsNoPrivilegedFields(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)

	memberClient, _ := signUp(t, srv, "owner2@example.com", "Owner")
	signUp(t, srv, "dana@example.com", "Dana Example")

	resp := doGet(t, memberClient, srv.URL+"/api/users/search?q=Dana")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Users []map[string]any `json:"users"`
	}
	decodeJSON(t, resp, &body)
	require.NotEmpty(t, body.Users)

	for _, u := range body.Users {
		keys := make(map[string]bool)
		for k := range u {
			keys[k] = true
		}
		require.Len(t, keys, 3)
		require.True(t, keys["id"])
		require.True(t, keys["name"])
		require.True(t, keys["email"])
	}
}

// Enumeration guard: below the minimum length it returns nothing rather than
// everyone.
func TestSearchRequiresTwoCharacters(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)

	memberClient, _ := signUp(t, srv, "owner3@example.com", "Owner")
	signUp(t, srv, "abby@example.com", "Abby")

	resp := doGet(t, memberClient, srv.URL+"/api/users/search?q=a")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Users []map[string]any `json:"users"`
	}
	decodeJSON(t, resp, &body)
	require.Empty(t, body.Users)
}

// And it is capped regardless of how many match.
func TestSearchIsCapped(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)

	memberClient, _ := signUp(t, srv, "owner4@example.com", "Owner")

	for i := 0; i < 40; i++ {
		signUp(t, srv, fmt.Sprintf("bulk%02d@example.com", i), fmt.Sprintf("BulkUser%02d", i))
	}

	resp := doGet(t, memberClient, srv.URL+"/api/users/search?q=BulkUser")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Users []map[string]any `json:"users"`
	}
	decodeJSON(t, resp, &body)
	require.LessOrEqual(t, len(body.Users), 20)
}

// Matching is on name and email, case-insensitively.
func TestSearchMatchesNameAndEmail(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)

	memberClient, _ := signUp(t, srv, "owner5@example.com", "Owner")
	signUp(t, srv, "carol@acme.com", "Carol Danvers")

	resp := doGet(t, memberClient, srv.URL+"/api/users/search?q="+url.QueryEscape("CAR"))
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Users []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Email string `json:"email"`
		} `json:"users"`
	}
	decodeJSON(t, resp, &body)

	found := false
	for _, u := range body.Users {
		if u.Email == "carol@acme.com" {
			found = true
		}
	}
	require.True(t, found, "expected carol@acme.com in results, got %+v", body.Users)
}
