package auth_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
)

func TestSignupAllowed(t *testing.T) {
	domains := []string{"company.com"}
	for _, c := range []struct {
		name, provider, email string
		disabled              bool
		domains               []string
		ok                    bool
	}{
		{"open password", "", "a@x.com", false, nil, true},
		{"disabled password", "", "a@x.com", true, nil, false},
		{"disabled github", "github", "a@x.com", true, nil, false},
		{"disabled google", "google", "a@x.com", true, nil, false},
		{"disabled oidc", "oidc", "a@x.com", true, nil, true},
		{"allowed domain", "", "A@Company.com", false, domains, true},
		{"other domain", "", "a@x.com", false, domains, false},
		{"oidc other domain", "oidc", "a@x.com", true, domains, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := auth.SignupAllowed(c.provider, c.email, c.disabled, c.domains)
			require.Equal(t, c.ok, err == nil, "%v", err)
		})
	}
}

func TestSignupRoles(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)
	_, owner := signUp(t, srv, "first@example.com", "First")
	_, member := signUp(t, srv, "second@example.com", "Second")
	require.Equal(t, auth.RoleOwner, owner.Role)
	require.Equal(t, auth.RoleMember, member.Role)
}

func TestDisableSignupRefusesPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, _ := setupPeople(t, auth.PeopleConfig{DisableSignup: true})
	resp := doPost(t, newClientWithJar(t), srv.URL+"/api/auth/sign-up/email", map[string]string{
		"email": "a@example.com", "name": "A", "password": "password123",
	})
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// Every way into the admin plugin bypasses the one-owner rule, so none may work,
// not even for the owner.
func TestAdminPluginRoutesAreRefused(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)
	client, owner := signUp(t, srv, "owner@example.com", "Owner")
	_, member := signUp(t, srv, "m@example.com", "M")

	for _, path := range []string{"/users", "/users/" + owner.ID + "/role", "/users/" + member.ID + "/password"} {
		resp := doPost(t, client, srv.URL+"/api/auth/admin"+path, map[string]string{"role": "owner"})
		resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode, path)
	}
}

func TestOwnerCannotDeleteOwnAccount(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)
	client, _ := signUp(t, srv, "owner@example.com", "Owner")
	resp := doPost(t, client, srv.URL+"/api/auth/user/delete", map[string]string{"password": "password123"})
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestTemporaryPasswordRequiresChange(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)
	ownerClient, _ := signUp(t, srv, "owner@example.com", "Owner")
	adminClient, target := signUp(t, srv, "target@example.com", "Target")

	denied := doPost(t, adminClient, srv.URL+"/api/admin/users/"+target.ID+"/password", nil)
	denied.Body.Close()
	require.Equal(t, http.StatusForbidden, denied.StatusCode)

	resp := doPost(t, ownerClient, srv.URL+"/api/admin/users/"+target.ID+"/password", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var issued struct {
		TemporaryPassword string `json:"temporary_password"`
	}
	decodeJSON(t, resp, &issued)
	require.NotEmpty(t, issued.TemporaryPassword)

	client := newClientWithJar(t)
	signIn := doPost(t, client, srv.URL+"/api/auth/sign-in/email", map[string]string{
		"email": "target@example.com", "password": issued.TemporaryPassword,
	})
	signIn.Body.Close()
	require.Equal(t, http.StatusOK, signIn.StatusCode)

	gated := doGet(t, client, srv.URL+"/api/protected")
	gated.Body.Close()
	require.Equal(t, http.StatusForbidden, gated.StatusCode)

	state := doGet(t, client, srv.URL+"/api/auth/account")
	require.Equal(t, http.StatusOK, state.StatusCode)
	var account struct {
		MustChangePassword bool `json:"must_change_password"`
	}
	decodeJSON(t, state, &account)
	require.True(t, account.MustChangePassword)

	change := doPost(t, client, srv.URL+"/api/auth/password/change", map[string]string{
		"currentPassword": issued.TemporaryPassword, "newPassword": "another-password-1",
	})
	change.Body.Close()
	require.Equal(t, http.StatusOK, change.StatusCode)

	open := doGet(t, client, srv.URL+"/api/protected")
	open.Body.Close()
	require.Equal(t, http.StatusOK, open.StatusCode)
}

// A disable is an access change: sessions and keys stop at once, and the audit
// log holds it. A sign-in is not, so it adds no row.
func TestDisableEndsAccessAndIsAudited(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv, pool := setupPeople(t, auth.PeopleConfig{})
	ownerClient, _ := signUp(t, srv, "owner@example.com", "Owner")
	client, target := signUp(t, srv, "target@example.com", "Target")

	created := doPost(t, client, srv.URL+"/api/auth/keys", map[string]string{"name": "k"})
	var key struct {
		Key string `json:"key"`
	}
	decodeJSON(t, created, &key)

	var before int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log`).Scan(&before))
	signIn := doPost(t, newClientWithJar(t), srv.URL+"/api/auth/sign-in/email", map[string]string{
		"email": "target@example.com", "password": "password123",
	})
	signIn.Body.Close()
	require.Equal(t, http.StatusOK, signIn.StatusCode)
	var after int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log`).Scan(&after))
	require.Equal(t, before, after)

	disable := doPost(t, ownerClient, srv.URL+"/api/admin/users/"+target.ID+"/disable", nil)
	disable.Body.Close()
	require.Equal(t, http.StatusNoContent, disable.StatusCode)

	session := doGet(t, client, srv.URL+"/api/protected")
	session.Body.Close()
	require.Equal(t, http.StatusUnauthorized, session.StatusCode)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/protected", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+key.Key)
	withKey, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	withKey.Body.Close()
	require.Equal(t, http.StatusUnauthorized, withKey.StatusCode)

	require.Eventually(t, func() bool {
		var n int
		err := pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE action = 'auth.user_disabled' AND resource = $1`, "user:"+target.ID).Scan(&n)
		return err == nil && n == 1
	}, 2*time.Second, 20*time.Millisecond)
}

func TestKeyWorksInBothHeaders(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	srv := setupAuthAPIAuthenticated(t)
	client, _ := signUp(t, srv, "owner@example.com", "Owner")
	created := doPost(t, client, srv.URL+"/api/auth/keys", map[string]string{"name": "k"})
	var key struct {
		Key string `json:"key"`
	}
	decodeJSON(t, created, &key)

	for name, value := range map[string]string{"Authorization": "Bearer " + key.Key, "X-API-Key": key.Key} {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/protected", nil)
		require.NoError(t, err)
		req.Header.Set(name, value)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, name)
	}
}
