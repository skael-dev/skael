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

// setupAuthAPIAuthenticated is setupAuthAPI plus the real auth middleware, so
// the /api/admin routes see the authenticated user on the request context the
// way they do in the server.
func setupAuthAPIAuthenticated(t *testing.T) *httptest.Server {
	t.Helper()

	pool := testutil.SetupTestDB(t)
	userStore := auth.NewUserStore(pool)
	keyStore := auth.NewKeyStore(pool)

	sessionManager := scs.New()

	r := chi.NewMux()
	r.Use(sessionManager.LoadAndSave)
	r.Use(auth.Middleware(sessionManager, userStore, keyStore))

	api := humachi.New(r, huma.DefaultConfig("Test API", "1.0.0"))
	auth.RegisterRoutes(api, sessionManager, userStore, keyStore, false)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return srv
}

// signUp creates an account with its own cookie jar and returns the client and
// the created user.
func signUp(t *testing.T, srv *httptest.Server, email, name string) (*http.Client, auth.User) {
	t.Helper()

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	resp := doPost(t, client, srv.URL+"/api/auth/signup", map[string]string{
		"email":    email,
		"name":     name,
		"password": "password123",
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var user auth.User
	decodeJSON(t, resp, &user)
	return client, user
}

// doPut sends a JSON PUT request and returns the response.
func doPut(t *testing.T, client *http.Client, url string, body interface{}) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(http.MethodPut, url, bodyReader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// roleOf reads a user's current role back from the owner-visible user list, so
// assertions check persisted state rather than the mutation's own response.
func roleOf(t *testing.T, client *http.Client, srv *httptest.Server, userID string) string {
	t.Helper()
	resp := doGet(t, client, srv.URL+"/api/admin/users")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Users []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"users"`
	}
	decodeJSON(t, resp, &body)

	for _, u := range body.Users {
		if u.ID == userID {
			return u.Role
		}
	}
	t.Fatalf("user %s not present in the user list", userID)
	return ""
}

func TestAdminSetUserRole(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)

	ownerClient, owner := signUp(t, srv, "owner@example.com", "Owner")
	require.Equal(t, auth.RoleOwner, owner.Role)

	memberClient, member := signUp(t, srv, "member@example.com", "Member")
	require.Equal(t, auth.RoleMember, member.Role, "every account after the first is unprivileged")

	otherClient, other := signUp(t, srv, "other@example.com", "Other")
	require.Equal(t, auth.RoleMember, other.Role)

	roleURL := func(id string) string { return srv.URL + "/api/admin/users/" + id + "/role" }

	t.Run("owner promotes a member to admin", func(t *testing.T) {
		resp := doPut(t, ownerClient, roleURL(member.ID), map[string]string{"role": auth.RoleAdmin})
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var body struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Role  string `json:"role"`
		}
		decodeJSON(t, resp, &body)
		require.Equal(t, member.ID, body.ID)
		require.Equal(t, "member@example.com", body.Email)
		require.Equal(t, auth.RoleAdmin, body.Role)

		require.Equal(t, auth.RoleAdmin, roleOf(t, ownerClient, srv, member.ID))
	})

	t.Run("a promoted admin is still not the owner", func(t *testing.T) {
		// The freshly promoted admin may not hand out roles.
		resp := doPut(t, memberClient, roleURL(other.ID), map[string]string{"role": auth.RoleAdmin})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		resp.Body.Close()

		require.Equal(t, auth.RoleMember, roleOf(t, ownerClient, srv, other.ID))
	})

	t.Run("owner demotes an admin back to member", func(t *testing.T) {
		resp := doPut(t, ownerClient, roleURL(member.ID), map[string]string{"role": auth.RoleMember})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()

		require.Equal(t, auth.RoleMember, roleOf(t, ownerClient, srv, member.ID))
	})

	t.Run("a member may not change roles", func(t *testing.T) {
		resp := doPut(t, otherClient, roleURL(member.ID), map[string]string{"role": auth.RoleAdmin})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		resp.Body.Close()

		require.Equal(t, auth.RoleMember, roleOf(t, ownerClient, srv, member.ID))
	})

	t.Run("nobody may be promoted to owner", func(t *testing.T) {
		resp := doPut(t, ownerClient, roleURL(member.ID), map[string]string{"role": auth.RoleOwner})
		require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
		resp.Body.Close()

		require.Equal(t, auth.RoleMember, roleOf(t, ownerClient, srv, member.ID))
	})

	t.Run("an unknown role is rejected", func(t *testing.T) {
		for _, role := range []string{"", "superuser", "ADMIN"} {
			resp := doPut(t, ownerClient, roleURL(member.ID), map[string]string{"role": role})
			require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "role %q", role)
			resp.Body.Close()
		}

		require.Equal(t, auth.RoleMember, roleOf(t, ownerClient, srv, member.ID))
	})

	t.Run("the owner may not change their own role", func(t *testing.T) {
		resp := doPut(t, ownerClient, roleURL(owner.ID), map[string]string{"role": auth.RoleMember})
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
		resp.Body.Close()

		require.Equal(t, auth.RoleOwner, roleOf(t, ownerClient, srv, owner.ID))
	})

	t.Run("an unknown user is not found", func(t *testing.T) {
		resp := doPut(t, ownerClient, roleURL("6b6d3f0f-77d1-4b0a-9b8a-6a1e0b6a9c11"),
			map[string]string{"role": auth.RoleAdmin})
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("a malformed user id is not found", func(t *testing.T) {
		resp := doPut(t, ownerClient, roleURL("not-a-uuid"), map[string]string{"role": auth.RoleAdmin})
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})

	t.Run("an unauthenticated caller is refused", func(t *testing.T) {
		jar, err := cookiejar.New(nil)
		require.NoError(t, err)
		anon := &http.Client{Jar: jar}

		resp := doPut(t, anon, roleURL(member.ID), map[string]string{"role": auth.RoleAdmin})
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		resp.Body.Close()

		require.Equal(t, auth.RoleMember, roleOf(t, ownerClient, srv, member.ID))
	})
}
