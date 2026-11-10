package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/evalqueue"
)

// CatalogURL is the models.dev catalog: the registry OpenCode itself resolves
// provider/model ids from, so the picker offers exactly what a session accepts.
const CatalogURL = "https://models.dev/api.json"

// catalogMaxAge is how long a fetched catalog is served before a refresh.
const catalogMaxAge = 24 * time.Hour

// catalogProviders are the models.dev providers the picker serves, keyed by
// provider type. An openai-compatible endpoint lists its own models instead.
var catalogProviders = []string{evalqueue.ProviderAnthropic, evalqueue.ProviderOpenAI, evalqueue.ProviderOpenRouter}

// Model is one entry in the picker.
type Model struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Context     int                  `json:"context,omitempty"`
	Cost        *evalqueue.ModelCost `json:"cost,omitempty"`
	ReleaseDate string               `json:"release_date,omitempty"`
}

// Catalog serves the models.dev catalog from Postgres, refreshing it in the
// background once a day.
type Catalog struct {
	pool   *pgxpool.Pool
	url    string
	client *http.Client

	mu         sync.Mutex
	models     map[string][]Model
	etag       string
	fetchedAt  time.Time
	refreshing bool
}

// NewCatalog returns a catalog on pool that fetches from url.
func NewCatalog(pool *pgxpool.Pool, url string) *Catalog {
	return &Catalog{pool: pool, url: url, client: &http.Client{Timeout: 30 * time.Second}}
}

// Models returns the catalog's tool-capable models for a provider type, newest
// first. A stale copy is served at once and refreshed in the background.
func (c *Catalog) Models(ctx context.Context, providerType string) ([]Model, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.models[providerType], nil
}

// Cost is a model's price, or nil when the catalog does not know it.
func (c *Catalog) Cost(ctx context.Context, providerType, model string) *evalqueue.ModelCost {
	list, err := c.Models(ctx, providerType)
	if err != nil {
		return nil
	}
	for _, m := range list {
		if m.ID == model {
			return m.Cost
		}
	}
	return nil
}

func (c *Catalog) ensure(ctx context.Context) error {
	c.mu.Lock()
	loaded, stale := c.models != nil, time.Since(c.fetchedAt) > catalogMaxAge
	c.mu.Unlock()
	if !loaded {
		if err := c.load(ctx); err != nil {
			return err
		}
		c.mu.Lock()
		loaded, stale = c.models != nil, time.Since(c.fetchedAt) > catalogMaxAge
		c.mu.Unlock()
	}
	if !loaded {
		return c.refresh(ctx)
	}
	if stale {
		c.mu.Lock()
		if !c.refreshing {
			c.refreshing = true
			go func() {
				bg, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if err := c.refresh(bg); err != nil {
					log.Warn().Err(err).Msg("providers: model catalog refresh failed; serving the last copy")
				}
			}()
		}
		c.mu.Unlock()
	}
	return nil
}

func (c *Catalog) load(ctx context.Context) error {
	var etag string
	var body []byte
	var at time.Time
	err := c.pool.QueryRow(ctx, `SELECT etag, body, fetched_at FROM model_catalog WHERE id = 1`).Scan(&etag, &body, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("providers: load the model catalog: %w", err)
	}
	var m map[string][]Model
	if err := json.Unmarshal(body, &m); err != nil {
		return fmt.Errorf("providers: decode the model catalog: %w", err)
	}
	c.mu.Lock()
	c.models, c.etag, c.fetchedAt = m, etag, at
	c.mu.Unlock()
	return nil
}

// refresh fetches the catalog. A failure keeps the last copy.
func (c *Catalog) refresh(ctx context.Context) error {
	defer func() {
		c.mu.Lock()
		c.refreshing = false
		c.mu.Unlock()
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.etag != "" && c.models != nil {
		req.Header.Set("If-None-Match", c.etag)
	}
	c.mu.Unlock()
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("providers: fetch the model catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		_, err := c.pool.Exec(ctx, `UPDATE model_catalog SET fetched_at = now() WHERE id = 1`)
		c.mu.Lock()
		c.fetchedAt = time.Now()
		c.mu.Unlock()
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("providers: fetch the model catalog: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return fmt.Errorf("providers: read the model catalog: %w", err)
	}
	trimmed, err := trimCatalog(raw)
	if err != nil {
		return err
	}
	body, err := json.Marshal(trimmed)
	if err != nil {
		return err
	}
	etag := resp.Header.Get("ETag")
	if _, err := c.pool.Exec(ctx, `
		INSERT INTO model_catalog (id, etag, body, fetched_at) VALUES (1, $1, $2, now())
		ON CONFLICT (id) DO UPDATE SET etag = EXCLUDED.etag, body = EXCLUDED.body, fetched_at = now()`, etag, body); err != nil {
		return fmt.Errorf("providers: store the model catalog: %w", err)
	}
	c.mu.Lock()
	c.models, c.etag, c.fetchedAt = trimmed, etag, time.Now()
	c.mu.Unlock()
	return nil
}

// trimCatalog keeps the served providers' non-deprecated text models that can
// call tools, newest first. The full catalog is megabytes; the trimmed one is
// what the picker needs.
func trimCatalog(raw []byte) (map[string][]Model, error) {
	var all map[string]struct {
		Models map[string]struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			ToolCall    bool   `json:"tool_call"`
			Status      string `json:"status"`
			ReleaseDate string `json:"release_date"`
			Modalities  struct {
				Input  []string `json:"input"`
				Output []string `json:"output"`
			} `json:"modalities"`
			Limit struct {
				Context int `json:"context"`
			} `json:"limit"`
			Cost *evalqueue.ModelCost `json:"cost"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("providers: decode the model catalog: %w", err)
	}
	out := map[string][]Model{}
	for _, p := range catalogProviders {
		var list []Model
		for _, m := range all[p].Models {
			if !m.ToolCall || m.Status == "deprecated" || !contains(m.Modalities.Input, "text") || !contains(m.Modalities.Output, "text") {
				continue
			}
			list = append(list, Model{ID: m.ID, Name: m.Name, Context: m.Limit.Context, Cost: m.Cost, ReleaseDate: m.ReleaseDate})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].ReleaseDate != list[j].ReleaseDate {
				return list[i].ReleaseDate > list[j].ReleaseDate
			}
			return list[i].ID < list[j].ID
		})
		out[p] = list
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// endpointModels asks an openai-compatible endpoint for its model ids. Azure
// lists base models rather than deployments, so the picker still accepts any
// typed id.
func endpointModels(ctx context.Context, client *http.Client, baseURL, header, key string) ([]Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/models", nil)
	if err != nil {
		return nil, err
	}
	if header != "" {
		req.Header.Set(header, key)
	} else if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the endpoint did not answer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the endpoint's model list returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("the endpoint's model list is not OpenAI-shaped: %w", err)
	}
	out := make([]Model, 0, len(body.Data))
	for _, d := range body.Data {
		out = append(out, Model{ID: d.ID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
