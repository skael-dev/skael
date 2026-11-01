package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Response pairs a decoded API body with its exact bytes, so --json can print
// the API shape without a client struct dropping a field.
type Response[T any] struct {
	Value T
	Raw   json.RawMessage
}

func fetch[T any](c *Client, method, path string, body any) (*Response[T], error) {
	return fetchHeaders[T](c, method, path, body, nil)
}

func fetchHeaders[T any](c *Client, method, path string, body any, headers map[string]string) (*Response[T], error) {
	var reader io.Reader
	contentType := ""
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(payload)
		contentType = "application/json"
	}
	resp, err := c.doHeaders(method, path, reader, contentType, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", method, path, err)
	}
	out := &Response[T]{Raw: raw}
	if err := json.Unmarshal(raw, &out.Value); err != nil {
		return nil, fmt.Errorf("decode %s %s: %w", method, path, err)
	}
	return out, nil
}

func (c *Client) deleteAt(path string) error {
	resp, err := c.do(http.MethodDelete, path, nil, "")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// SetEntry is one skill in a set. Version 0 means latest.
type SetEntry struct {
	Name    string `json:"name"`
	Version int    `json:"version,omitempty"`
}

// Set is a named list of skills.
type Set struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Entries     []SetEntry `json:"entries"`
	CreatedBy   string     `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// SetCaller is the last use of a set by one identity.
type SetCaller struct {
	Name         string    `json:"name"`
	ResolutionID string    `json:"resolution_id"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// SetDetail is a set with who last used it.
type SetDetail struct {
	Set
	Callers []SetCaller `json:"callers"`
}

// ResolvedEntry is one exact version in a resolution.
type ResolvedEntry struct {
	Name          string `json:"name"`
	Version       int    `json:"version"`
	SHA256        string `json:"sha256"`
	ScanVerdict   string `json:"scan_verdict"`
	ApprovalState string `json:"approval_state"`
}

// ExcludedEntry is a set entry that a resolve could not serve.
type ExcludedEntry struct {
	Name      string `json:"name"`
	Requested string `json:"requested"`
	Reason    string `json:"reason"`
}

// Resolution is the immutable result of resolving a set.
type Resolution struct {
	ID       string          `json:"id"`
	Set      string          `json:"set"`
	Complete bool            `json:"complete"`
	Entries  []ResolvedEntry `json:"entries"`
	Excluded []ExcludedEntry `json:"excluded,omitempty"`
}

// BundleManifest is the manifest.json a bundle carries.
type BundleManifest struct {
	Set          string `json:"set"`
	ResolutionID string `json:"resolution_id"`
	Complete     bool   `json:"complete"`
	Skills       []struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
		SHA256  string `json:"sha256"`
		Archive string `json:"archive"`
	} `json:"skills"`
}

// ListSets calls GET /api/sets.
func (c *Client) ListSets() (*Response[struct {
	Sets []Set `json:"sets"`
}], error) {
	return fetch[struct {
		Sets []Set `json:"sets"`
	}](c, http.MethodGet, "/api/sets", nil)
}

// GetSet calls GET /api/sets/{name}.
func (c *Client) GetSet(name string) (*Response[SetDetail], error) {
	return fetch[SetDetail](c, http.MethodGet, "/api/sets/"+url.PathEscape(name), nil)
}

// CreateSet calls POST /api/sets.
func (c *Client) CreateSet(name, description string, entries []SetEntry) (*Response[Set], error) {
	return fetch[Set](c, http.MethodPost, "/api/sets", struct {
		Name        string     `json:"name"`
		Description string     `json:"description,omitempty"`
		Entries     []SetEntry `json:"entries"`
	}{name, description, entries})
}

// UpdateSet calls PUT /api/sets/{name}, which replaces the description and the
// entries together.
func (c *Client) UpdateSet(name, description string, entries []SetEntry) (*Response[Set], error) {
	return fetch[Set](c, http.MethodPut, "/api/sets/"+url.PathEscape(name), struct {
		Description string     `json:"description,omitempty"`
		Entries     []SetEntry `json:"entries"`
	}{description, entries})
}

// DeleteSet calls DELETE /api/sets/{name}.
func (c *Client) DeleteSet(name string) error {
	return c.deleteAt("/api/sets/" + url.PathEscape(name))
}

// ResolveSet calls GET /api/sets/{name}/resolve and always asks for the
// excluded entries, so a caller can say why a set is incomplete.
func (c *Client) ResolveSet(name string) (*Response[Resolution], error) {
	return fetch[Resolution](c, http.MethodGet, "/api/sets/"+url.PathEscape(name)+"/resolve?include_excluded=true", nil)
}

// bundleTimeout replaces the client's 30-second limit, which covers the whole
// response body and is too short for a large set on a slow link.
const bundleTimeout = 10 * time.Minute

// DownloadBundle calls GET /api/sets/{name}/bundle?resolved={id}. The caller
// closes the returned stream.
func (c *Client) DownloadBundle(name, resolutionID string) (io.ReadCloser, error) {
	long := *c
	long.httpClient = &http.Client{Timeout: bundleTimeout}
	path := "/api/sets/" + url.PathEscape(name) + "/bundle?resolved=" + url.QueryEscape(resolutionID)
	resp, err := long.do(http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// ServiceAccount is a machine identity.
type ServiceAccount struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyCount   int        `json:"key_count"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ServiceKey is a service account's key, without its secret.
type ServiceKey struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	SetID      *string    `json:"set_id,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// CreatedServiceKey is a new key. Key is its secret, which the server returns
// only here.
type CreatedServiceKey struct {
	ServiceKey
	Key string `json:"key"`
}

// ListServiceAccounts calls GET /api/service-accounts.
func (c *Client) ListServiceAccounts() (*Response[struct {
	ServiceAccounts []ServiceAccount `json:"service_accounts"`
}], error) {
	return fetch[struct {
		ServiceAccounts []ServiceAccount `json:"service_accounts"`
	}](c, http.MethodGet, "/api/service-accounts", nil)
}

// CreateServiceAccount calls POST /api/service-accounts.
func (c *Client) CreateServiceAccount(name string) (*Response[ServiceAccount], error) {
	return fetch[ServiceAccount](c, http.MethodPost, "/api/service-accounts", map[string]string{"name": name})
}

// DeleteServiceAccount calls DELETE /api/service-accounts/{id}.
func (c *Client) DeleteServiceAccount(id string) error {
	return c.deleteAt("/api/service-accounts/" + url.PathEscape(id))
}

// ListServiceKeys calls GET /api/service-accounts/{id}/keys.
func (c *Client) ListServiceKeys(accountID string) (*Response[struct {
	Keys []ServiceKey `json:"keys"`
}], error) {
	return fetch[struct {
		Keys []ServiceKey `json:"keys"`
	}](c, http.MethodGet, "/api/service-accounts/"+url.PathEscape(accountID)+"/keys", nil)
}

// CreateServiceKey calls POST /api/service-accounts/{id}/keys.
func (c *Client) CreateServiceKey(accountID, name string, scopes []string, setID string) (*Response[CreatedServiceKey], error) {
	return fetch[CreatedServiceKey](c, http.MethodPost, "/api/service-accounts/"+url.PathEscape(accountID)+"/keys", struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
		SetID  string   `json:"set_id,omitempty"`
	}{name, scopes, setID})
}

// RevokeServiceKey calls DELETE /api/service-accounts/{id}/keys/{keyId}.
func (c *Client) RevokeServiceKey(accountID, keyID string) error {
	return c.deleteAt("/api/service-accounts/" + url.PathEscape(accountID) + "/keys/" + url.PathEscape(keyID))
}
