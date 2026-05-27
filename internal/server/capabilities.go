package server

import (
	"context"
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
	Edition  string       `json:"edition"`
	Features Features     `json:"features"`
	License  *LicenseInfo `json:"license"`
}

type Capabilities struct {
	edition  string
	features map[string]bool
	license  *LicenseInfo
}

func NewCapabilities() *Capabilities {
	return &Capabilities{
		edition:  "oss",
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

func (c *Capabilities) Response() CapabilitiesResponse {
	return CapabilitiesResponse{
		Edition: c.edition,
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
	}
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
