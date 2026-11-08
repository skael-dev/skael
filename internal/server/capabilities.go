package server

import (
	"context"
	"github.com/skael-dev/skael/internal/auth"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

type Features struct {
	Teams             bool `json:"teams"`
	RBAC              bool `json:"rbac"`
	SSO               bool `json:"sso"`
	Audit             bool `json:"audit"`
	Governance        bool `json:"governance"`
	CustomScan        bool `json:"custom_scan"`
	AdvancedAnalytics bool `json:"advanced_analytics"`
}

type LicenseInfo struct {
	Org     string `json:"org"`
	Seats   int    `json:"seats"`
	Expires string `json:"expires"`
}

type CapabilitiesResponse struct {
	Edition  string             `json:"edition"`
	Version  string             `json:"version"`
	Features Features           `json:"features"`
	License  *LicenseInfo       `json:"license"`
	SignIn   auth.SignInMethods `json:"sign_in"`
}

type Capabilities struct {
	edition  string
	version  string
	features map[string]bool
	license  *LicenseInfo
	signIn   func() auth.SignInMethods
}

func NewCapabilities() *Capabilities {
	return &Capabilities{
		edition: "oss",
		// Matches cmd/server's unstamped ldflags default, so a `go run` build
		// reports the same string here that it logs at startup.
		version:  "dev",
		features: make(map[string]bool),
	}
}

func (c *Capabilities) EnableFeature(name string) {
	c.features[name] = true
}

func (c *Capabilities) SetEdition(edition string) {
	c.edition = edition
}

func (c *Capabilities) SetLicense(l *LicenseInfo) {
	c.license = l
}

// SetVersion records the build version reported by /api/capabilities. An empty
// value is ignored rather than stored: an unstamped build should read "dev",
// not blank.
func (c *Capabilities) SetVersion(v string) {
	if v == "" {
		return
	}
	c.version = v
}

// SetSignIn records where the sign-in methods the SPA may show come from. It
// is read on each request, so a settings change shows without a restart.
func (c *Capabilities) SetSignIn(m func() auth.SignInMethods) { c.signIn = m }

func (c *Capabilities) Response() CapabilitiesResponse {
	return CapabilitiesResponse{
		Edition: c.edition,
		Version: c.version,
		Features: Features{
			Teams:             c.features["teams"],
			RBAC:              c.features["rbac"],
			SSO:               c.features["sso"],
			Audit:             c.features["audit"],
			Governance:        c.features["governance"],
			CustomScan:        c.features["custom_scan"],
			AdvancedAnalytics: c.features["advanced_analytics"],
		},
		License: c.license,
		SignIn:  c.signInMethods(),
	}
}

func (c *Capabilities) signInMethods() auth.SignInMethods {
	if c.signIn == nil {
		return auth.SignInMethods{Providers: []string{}}
	}
	return c.signIn()
}

func (c *Capabilities) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-capabilities",
		Method:      http.MethodGet,
		Path:        "/api/capabilities",
		Summary:     "Get server capabilities and edition info",
	}, func(_ context.Context, _ *struct{}) (*struct {
		Body CapabilitiesResponse
	}, error) {
		resp := c.Response()
		return &struct {
			Body CapabilitiesResponse
		}{Body: resp}, nil
	})
}
