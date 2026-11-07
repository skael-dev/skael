package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/onboarding"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

func TestClaim_TheFirstSignUpNeedsTheCodeFromTheLog(t *testing.T) {
	if testing.Short() {
		t.Skip("requires database")
	}
	pool := testutil.SetupTestDB(t)
	srv, err := NewBuilder(pool, &platform.Config{StoragePath: t.TempDir(), ListenAddr: ":0"}).WithClaim().Build()
	require.NoError(t, err)
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	code, err := onboarding.NewClaim(pool).Ensure(context.Background())
	require.NoError(t, err)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	signUp := func() int {
		resp, err := c.Post(ts.URL+"/api/auth/sign-up/email", "application/json",
			strings.NewReader(`{"email":"owner@x.com","name":"Owner","password":"correct-horse-battery"}`))
		require.NoError(t, err)
		resp.Body.Close() //nolint:errcheck
		return resp.StatusCode
	}

	require.Equal(t, http.StatusForbidden, signUp())

	resp, err := c.Post(ts.URL+"/api/setup/claim", "application/json", strings.NewReader(`{"code":"AAAA-BBBB-CCCC"}`))
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp, err = c.Post(ts.URL+"/api/setup/claim", "application/json", strings.NewReader(`{"code":"`+code+`"}`))
	require.NoError(t, err)
	resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	require.Equal(t, http.StatusCreated, signUp())
}
