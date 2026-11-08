// Package gitsync keeps git-bound skills in step with their repositories
// through one GitHub App per instance.
package gitsync

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/platform"
)

// Public GitHub. An Enterprise Server names its own pair.
const (
	DefaultWebBaseURL = "https://github.com"
	DefaultAPIBaseURL = "https://api.github.com"
)

// manifestTTL bounds how long a manifest flow may take between Skael and the
// GitHub confirmation page.
const manifestTTL = time.Hour

// Associated data for each sealed column, so a value moved between columns
// does not open.
const (
	aadPrivateKey    = "github_app.private_key"
	aadWebhookSecret = "github_app.webhook_secret"
	aadClientSecret  = "github_app.client_secret"
)

// App is the GitHub App this instance acts as, with its secrets opened.
type App struct {
	AppID         int64
	Slug          string
	Name          string
	OwnerLogin    string
	HTMLURL       string
	WebBaseURL    string
	APIBaseURL    string
	ClientID      string
	PrivateKey    []byte
	WebhookSecret []byte
	ClientSecret  []byte
	CreatedBy     string
	CreatedAt     time.Time
}

// InstallURL is where an org owner installs the App.
func (a *App) InstallURL() string {
	return strings.TrimSuffix(a.HTMLURL, "/") + "/installations/new"
}

// ErrAppExists means the instance already has an App.
var ErrAppExists = errors.New("this instance already has a GitHub App; disconnect it first")

// ErrManifestState means GitHub returned a state this instance did not issue,
// or one that expired.
var ErrManifestState = errors.New("the GitHub App setup expired or did not start here; start it again")

// Store persists the App and the manifest flows in progress.
type Store struct {
	pool *pgxpool.Pool
	keys *platform.Keyring
}

// ErrSealed means a stored App secret does not open with any master key.
var ErrSealed = errors.New("the GitHub App's secrets do not open with the current master key")

// NewStore builds a Store. keys may hold no key; then nothing can be saved.
func NewStore(pool *pgxpool.Pool, keys *platform.Keyring) *Store {
	return &Store{pool: pool, keys: keys}
}

// KeysReady reports whether a master key exists to seal the App's secrets.
func (s *Store) KeysReady() bool { return s.keys.Enabled() }

// App returns the App with its secrets opened, or (nil, nil) when there is none.
func (s *Store) App(ctx context.Context) (*App, error) {
	var a App
	var pk, ws, cs string
	err := s.pool.QueryRow(ctx, `
		SELECT app_id, slug, name, owner_login, html_url, web_base_url, api_base_url, client_id,
			private_key, webhook_secret, client_secret, created_by, created_at
		FROM github_app WHERE id = 1`).Scan(&a.AppID, &a.Slug, &a.Name, &a.OwnerLogin, &a.HTMLURL,
		&a.WebBaseURL, &a.APIBaseURL, &a.ClientID, &pk, &ws, &cs, &a.CreatedBy, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.App: %w", err)
	}
	for _, f := range []struct {
		dst    *[]byte
		sealed string
		aad    string
	}{{&a.PrivateKey, pk, aadPrivateKey}, {&a.WebhookSecret, ws, aadWebhookSecret}, {&a.ClientSecret, cs, aadClientSecret}} {
		plain, err := s.keys.Open(f.sealed, f.aad)
		if err != nil {
			return nil, fmt.Errorf("gitsync.Store.App: open %s: %w: %w", f.aad, ErrSealed, err)
		}
		*f.dst = plain
	}
	return &a, nil
}

// StartManifest records a manifest flow and returns its state.
func (s *Store) StartManifest(ctx context.Context, webBase, apiBase, actor string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gitsync.Store.StartManifest: %w", err)
	}
	state := hex.EncodeToString(b)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO github_app_manifests (state, web_base_url, api_base_url, created_by)
		VALUES ($1, $2, $3, $4)`, state, webBase, apiBase, actor); err != nil {
		return "", fmt.Errorf("gitsync.Store.StartManifest: %w", err)
	}
	return state, nil
}

// manifestFlow is a consumed manifest state.
type manifestFlow struct {
	WebBaseURL string
	APIBaseURL string
	CreatedBy  string
}

// consumeManifest deletes and returns a live state, or ErrManifestState.
func (s *Store) consumeManifest(ctx context.Context, state string) (*manifestFlow, error) {
	var f manifestFlow
	err := s.pool.QueryRow(ctx, `
		DELETE FROM github_app_manifests
		WHERE state = $1 AND created_at > now() - make_interval(secs => $2)
		RETURNING web_base_url, api_base_url, created_by`,
		state, int(manifestTTL.Seconds())).Scan(&f.WebBaseURL, &f.APIBaseURL, &f.CreatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrManifestState
	}
	if err != nil {
		return nil, fmt.Errorf("gitsync.Store.consumeManifest: %w", err)
	}
	return &f, nil
}

// save seals the App's secrets and stores it, with its audit record.
func (s *Store) save(ctx context.Context, a *App) error {
	sealed := make([]string, 3)
	for i, f := range []struct {
		plain []byte
		aad   string
	}{{a.PrivateKey, aadPrivateKey}, {a.WebhookSecret, aadWebhookSecret}, {a.ClientSecret, aadClientSecret}} {
		v, err := s.keys.Seal(f.plain, f.aad)
		if err != nil {
			return fmt.Errorf("gitsync.Store.save: seal %s: %w", f.aad, err)
		}
		sealed[i] = v
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("gitsync.Store.save: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		INSERT INTO github_app (app_id, slug, name, owner_login, html_url, web_base_url, api_base_url,
			client_id, private_key, webhook_secret, client_secret, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO NOTHING`,
		a.AppID, a.Slug, a.Name, a.OwnerLogin, a.HTMLURL, a.WebBaseURL, a.APIBaseURL, a.ClientID,
		sealed[0], sealed[1], sealed[2], a.CreatedBy)
	if err != nil {
		return fmt.Errorf("gitsync.Store.save: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAppExists
	}
	if err := audit.Record(ctx, tx, a.CreatedBy, "github_app.create", "github_app:"+a.Slug,
		map[string]any{"app_id": a.AppID, "owner": a.OwnerLogin, "web_base_url": a.WebBaseURL}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Delete removes the App. GitHub keeps it; its owner deletes it there.
func (s *Store) Delete(ctx context.Context, actor string) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("gitsync.Store.Delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var slug string
	err = tx.QueryRow(ctx, `DELETE FROM github_app WHERE id = 1 RETURNING slug`).Scan(&slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("gitsync.Store.Delete: %w", err)
	}
	if err := audit.Record(ctx, tx, actor, "github_app.delete", "github_app:"+slug, nil); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// ManifestRequest is what an admin chooses before GitHub creates the App.
type ManifestRequest struct {
	Name         string
	Organization string
	WebBaseURL   string
	APIBaseURL   string
	PublicURL    string
}

// Manifest returns the URL to post the manifest to and the manifest JSON.
// The App is installable by any account, so one App serves every org of a
// company; it is not listed on the Marketplace.
func Manifest(r ManifestRequest, state string) (postURL string, manifest string, err error) {
	public := strings.TrimSuffix(r.PublicURL, "/")
	m := map[string]any{
		"name":            r.Name,
		"url":             public,
		"hook_attributes": map[string]any{"url": public + "/api/github/webhook", "active": true},
		"redirect_url":    public + "/api/admin/github/callback",
		"setup_url":       public + "/settings/github",
		"setup_on_update": true,
		"public":          true,
		"default_permissions": map[string]string{
			"contents":      "write",
			"pull_requests": "write",
			"checks":        "write",
			"metadata":      "read",
		},
		"default_events": []string{"push", "pull_request", "check_run"},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return "", "", fmt.Errorf("gitsync.Manifest: %w", err)
	}
	base := strings.TrimSuffix(r.WebBaseURL, "/")
	path := "/settings/apps/new"
	if org := strings.TrimSpace(r.Organization); org != "" {
		path = "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	return base + path + "?state=" + url.QueryEscape(state), string(raw), nil
}

// conversion is GitHub's answer to a manifest code.
type conversion struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	HTMLURL       string `json:"html_url"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// Complete exchanges the code GitHub returned for the App's credentials and
// stores them. The state must be one this instance issued in the last hour.
func (s *Store) Complete(ctx context.Context, client *http.Client, state, code string) (*App, error) {
	if !s.KeysReady() {
		return nil, platform.ErrNoMasterKey
	}
	flow, err := s.consumeManifest(ctx, state)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSuffix(flow.APIBaseURL, "/") + "/app-manifests/" + url.PathEscape(code) + "/conversions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(nil))
	if err != nil {
		return nil, fmt.Errorf("gitsync: conversion request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitsync: GitHub did not answer the App conversion: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("gitsync: GitHub refused the App conversion with %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var c conversion
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("gitsync: decode the App conversion: %w", err)
	}
	if c.ID == 0 || c.PEM == "" || c.WebhookSecret == "" {
		return nil, errors.New("gitsync: GitHub's App conversion lacks the id, key or webhook secret")
	}
	a := &App{
		AppID: c.ID, Slug: c.Slug, Name: c.Name, OwnerLogin: c.Owner.Login, HTMLURL: c.HTMLURL,
		WebBaseURL: flow.WebBaseURL, APIBaseURL: flow.APIBaseURL, ClientID: c.ClientID,
		PrivateKey: []byte(c.PEM), WebhookSecret: []byte(c.WebhookSecret), ClientSecret: []byte(c.ClientSecret),
		CreatedBy: flow.CreatedBy,
	}
	if err := s.save(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// SealedUnreadable reports whether the App exists and its secrets no longer open.
func (s *Store) SealedUnreadable(ctx context.Context) (bool, error) {
	_, err := s.App(ctx)
	if errors.Is(err, ErrSealed) {
		return true, nil
	}
	return false, err
}
