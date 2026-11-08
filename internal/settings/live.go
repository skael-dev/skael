package settings

import (
	"context"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// PollInterval is how often a replica checks the version when no NOTIFY came.
const PollInterval = 30 * time.Second

// Live holds the current snapshot. A save on any replica reaches every replica
// through NOTIFY, and the version poll catches a missed notification.
type Live struct {
	store *Store
	env   EnvFunc
	keys  Opener
	cur   atomic.Pointer[Snapshot]

	mu   sync.Mutex
	subs []func(*Snapshot)
}

// NewLive returns a Live over store. Call Load before Get.
func NewLive(store *Store, env EnvFunc, keys Opener) *Live {
	if env == nil {
		env = os.LookupEnv
	}
	l := &Live{store: store, env: env, keys: keys}
	l.cur.Store(Resolve(0, env, nil, keys))
	return l
}

// Static returns a Live with no store, resolved from env alone. It serves spec
// generation and tests.
func Static(env map[string]string) *Live {
	return NewLive(nil, func(k string) (string, bool) { v, ok := env[k]; return v, ok }, nil)
}

// Env returns the environment the snapshot resolves against.
func (l *Live) Env() EnvFunc { return l.env }

// Store returns the store, or nil for a static Live.
func (l *Live) Store() *Store { return l.store }

// Get returns the current snapshot.
func (l *Live) Get() *Snapshot { return l.cur.Load() }

// OnChange registers fn to run after each new snapshot.
func (l *Live) OnChange(fn func(*Snapshot)) {
	l.mu.Lock()
	l.subs = append(l.subs, fn)
	l.mu.Unlock()
}

// Load reads the store and publishes a new snapshot.
func (l *Live) Load(ctx context.Context) error {
	if l.store == nil {
		return nil
	}
	stored, version, err := l.store.Load(ctx)
	if err != nil {
		return err
	}
	snap := Resolve(version, l.env, stored, l.keys)
	l.cur.Store(snap)
	l.mu.Lock()
	subs := append([]func(*Snapshot){}, l.subs...)
	l.mu.Unlock()
	for _, fn := range subs {
		fn(snap)
	}
	return nil
}

// Watch reloads on every NOTIFY and every PollInterval until ctx ends.
func (l *Live) Watch(ctx context.Context) {
	if l.store == nil {
		return
	}
	for ctx.Err() == nil {
		if err := l.listen(ctx); err != nil && ctx.Err() == nil {
			log.Warn().Err(err).Msg("settings: listen failed; retrying")
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (l *Live) listen(ctx context.Context) error {
	conn, err := l.store.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	for ctx.Err() == nil {
		waitCtx, cancel := context.WithTimeout(ctx, PollInterval)
		n, err := conn.Conn().WaitForNotification(waitCtx)
		cancel()
		if err != nil && ctx.Err() != nil {
			return nil
		}
		if err != nil && waitCtx.Err() == nil {
			return err
		}
		if n != nil {
			if v, perr := strconv.ParseInt(n.Payload, 10, 64); perr == nil && v <= l.Get().Version {
				continue
			}
		} else if v, err := l.store.Version(ctx); err != nil || v == l.Get().Version {
			continue
		}
		if err := l.Load(ctx); err != nil {
			log.Warn().Err(err).Msg("settings: reload failed")
		}
	}
	return nil
}
