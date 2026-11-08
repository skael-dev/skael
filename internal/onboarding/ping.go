package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PingURL receives the anonymous install ping.
const PingURL = "https://telemetry.skael.dev/v1/ping"

// Ping is everything the install ping sends. Nothing in it names a person, a
// company, a skill or a host.
type Ping struct {
	InstallID       string `json:"install_id"`
	Version         string `json:"version"`
	PostgresVersion string `json:"postgres_version"`
	Workers         int    `json:"workers"`
	EvalsConfigured bool   `json:"evals_configured"`
}

// BuildPing reads the ping's fields. Sample rows never count.
func BuildPing(ctx context.Context, pool *pgxpool.Pool, version string) (Ping, error) {
	p := Ping{Version: version}
	err := pool.QueryRow(ctx, `SELECT
		(SELECT install_id::text FROM onboarding WHERE id = 1),
		current_setting('server_version'),
		(SELECT COUNT(*) FROM workers WHERE last_seen > now() - interval '1 day'),
		EXISTS (SELECT 1 FROM eval_panel)`).Scan(&p.InstallID, &p.PostgresVersion, &p.Workers, &p.EvalsConfigured)
	if err != nil {
		return p, fmt.Errorf("onboarding: ping: %w", err)
	}
	return p, nil
}

// SendPing posts p to url. A failure is not an error for the caller: the ping
// never affects the server.
func SendPing(ctx context.Context, client *http.Client, url string, p Ping) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("onboarding: ping returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// PingInterval is how often the leader sends the ping.
const PingInterval = 24 * time.Hour
