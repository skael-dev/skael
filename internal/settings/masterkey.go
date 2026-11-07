package settings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/platform"
)

// KeyFile is the name of the generated master key in the data directory.
const KeyFile = "master.key"

// Key sources.
const (
	KeySourceEnv  = "env"
	KeySourceFile = "file"
)

// MasterKey is the keyring in effect and where it came from.
type MasterKey struct {
	Keys      *platform.Keyring
	Source    string
	Path      string
	Generated bool
}

// DataDir is SKAEL_DATA_DIR, else a local STORAGE_PATH, else ./data.
func DataDir(snap *Snapshot) string {
	if d := snap.String("data_dir"); d != "" {
		return d
	}
	if p := snap.String("storage_path"); p != "" && !strings.HasPrefix(p, "s3://") {
		return p
	}
	return "./data"
}

// EnsureMasterKey returns SKAEL_MASTER_KEYS when set. Otherwise it reads the
// key file in the data directory, and writes a new key there when none exists.
func EnsureMasterKey(cfg *platform.Config, snap *Snapshot) (*MasterKey, error) {
	if cfg.MasterKeys.Enabled() {
		return &MasterKey{Keys: cfg.MasterKeys, Source: KeySourceEnv}, nil
	}
	path := filepath.Join(DataDir(snap), KeyFile)
	raw, err := os.ReadFile(path)
	generated := false
	if errors.Is(err, os.ErrNotExist) {
		pair, gerr := platform.GenerateKey()
		if gerr != nil {
			return nil, gerr
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("master key: create %s: %w", filepath.Dir(path), err)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("master key: write %s: %w", path, err)
		}
		if _, err := f.WriteString(pair + "\n"); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("master key: write %s: %w", path, err)
		}
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("master key: write %s: %w", path, err)
		}
		raw, generated = []byte(pair), true
	} else if err != nil {
		return nil, fmt.Errorf("master key: read %s: %w", path, err)
	}
	keys, err := platform.ParseKeyring(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, fmt.Errorf("master key file %s: %w", path, err)
	}
	cfg.MasterKeys = keys
	return &MasterKey{Keys: keys, Source: KeySourceFile, Path: path, Generated: generated}, nil
}

const canaryAAD = "master-key-canary"

// Unreadable is one stored secret that no longer opens.
type Unreadable struct {
	Name string `json:"name"`
	// Where says where to issue the secret again.
	Where string `json:"where"`
}

// SealedSource is a feature that stores sealed secrets outside the settings
// table.
type SealedSource interface {
	Unreadable(ctx context.Context) ([]Unreadable, error)
	Discard(ctx context.Context, actor string) error
}

// KeyService checks the master key at start and recovers from a lost one.
type KeyService struct {
	pool    *pgxpool.Pool
	key     *MasterKey
	live    *Live
	sources []SealedSource
}

// NewKeyService returns the service. sources are the other sealed stores.
func NewKeyService(pool *pgxpool.Pool, key *MasterKey, live *Live, sources ...SealedSource) *KeyService {
	return &KeyService{pool: pool, key: key, live: live, sources: sources}
}

// KeyStatus is the master key's state.
type KeyStatus struct {
	Source      string       `json:"source"`
	Path        string       `json:"path,omitempty"`
	KeyID       string       `json:"key_id"`
	Fingerprint string       `json:"fingerprint"`
	Missing     []string     `json:"missing"`
	Unreadable  []Unreadable `json:"unreadable"`
}

// Degraded reports whether a key is lost or a secret no longer opens.
func (s KeyStatus) Degraded() bool { return len(s.Missing) > 0 || len(s.Unreadable) > 0 }

// EnsureCanary seals a canary under the active key if it has none.
func (k *KeyService) EnsureCanary(ctx context.Context) error {
	if k.pool == nil || !k.key.Keys.Enabled() {
		return nil
	}
	sealed, err := k.key.Keys.Seal([]byte("skael"), canaryAAD)
	if err != nil {
		return err
	}
	_, err = k.pool.Exec(ctx, `INSERT INTO master_key_canaries (key_id, sealed) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		k.key.Keys.ActiveID(), sealed)
	return err
}

// Status checks every canary and every sealed secret.
func (k *KeyService) Status(ctx context.Context) (KeyStatus, error) {
	st := KeyStatus{Source: k.key.Source, Path: k.key.Path, KeyID: k.key.Keys.ActiveID(),
		Fingerprint: k.key.Keys.Fingerprint(), Missing: []string{}, Unreadable: []Unreadable{}}
	if k.pool == nil {
		return st, nil
	}
	rows, err := k.pool.Query(ctx, `SELECT key_id, sealed FROM master_key_canaries ORDER BY created_at`)
	if err != nil {
		return st, fmt.Errorf("master key: canaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, sealed string
		if err := rows.Scan(&id, &sealed); err != nil {
			return st, err
		}
		if _, err := k.key.Keys.Open(sealed, canaryAAD); err != nil {
			st.Missing = append(st.Missing, id)
		}
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	for _, r := range k.live.Get().Problems() {
		if r.Unreadable {
			st.Unreadable = append(st.Unreadable, Unreadable{Name: r.Def.Label, Where: "Settings → " + sectionTitle(r.Def.Section)})
		}
	}
	for _, src := range k.sources {
		u, err := src.Unreadable(ctx)
		if err != nil {
			return st, err
		}
		st.Unreadable = append(st.Unreadable, u...)
	}
	return st, nil
}

func sectionTitle(id string) string {
	if s := SectionByID(id); s != nil {
		return s.Title
	}
	return id
}

// Reset discards every secret that no longer opens and every canary of a lost
// key, and seals a canary under the active key.
func (k *KeyService) Reset(ctx context.Context, actor string) error {
	tx, err := k.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	rows, err := tx.Query(ctx, `SELECT key, sealed FROM settings WHERE sealed <> ''`)
	if err != nil {
		return err
	}
	var discard []string
	for rows.Next() {
		var key, sealed string
		if err := rows.Scan(&key, &sealed); err != nil {
			rows.Close()
			return err
		}
		if _, err := k.key.Keys.Open(sealed, aad(key)); err != nil {
			discard = append(discard, key)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, key := range discard {
		if _, err := tx.Exec(ctx, `DELETE FROM settings WHERE key = $1`, key); err != nil {
			return err
		}
	}
	canaries, err := tx.Query(ctx, `SELECT key_id, sealed FROM master_key_canaries`)
	if err != nil {
		return err
	}
	var lost []string
	for canaries.Next() {
		var id, sealed string
		if err := canaries.Scan(&id, &sealed); err != nil {
			canaries.Close()
			return err
		}
		if _, err := k.key.Keys.Open(sealed, canaryAAD); err != nil {
			lost = append(lost, id)
		}
	}
	canaries.Close()
	if _, err := tx.Exec(ctx, `DELETE FROM master_key_canaries WHERE key_id = ANY($1)`, lost); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE settings_version SET version = version + 1 WHERE id = 1`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, 'reset')`, Channel); err != nil {
		return err
	}
	if err := audit.Record(ctx, tx, actor, "master_key.reset", "master_key:"+k.key.Keys.ActiveID(),
		map[string]any{"discarded_settings": discard}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, src := range k.sources {
		if err := src.Discard(ctx, actor); err != nil {
			return err
		}
	}
	if err := k.EnsureCanary(ctx); err != nil {
		return err
	}
	return k.live.Load(ctx)
}
