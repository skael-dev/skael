package agent

import (
	"errors"
	"sort"
	"sync"
)

// ErrParseNotImplemented is returned by adapters that have no verified stream
// fixture yet. Adapters fail closed: returning an empty Result would read
// downstream as a session in which the agent did nothing, scoring a working
// skill as a failure.
var ErrParseNotImplemented = errors.New("agent: Parse not implemented — no recorded stream fixture for this CLI")

var (
	mu       sync.RWMutex
	adapters = map[string]Adapter{}
)

// Register adds an adapter. Called from adapter package init functions.
func Register(a Adapter) {
	mu.Lock()
	defer mu.Unlock()
	adapters[a.Name()] = a
}

// Get returns the adapter registered under name.
func Get(name string) (Adapter, bool) {
	mu.RLock()
	defer mu.RUnlock()
	a, ok := adapters[name]
	return a, ok
}

// All returns every registered adapter, ordered by name for stable output.
func All() []Adapter {
	mu.RLock()
	defer mu.RUnlock()

	out := make([]Adapter, 0, len(adapters))
	for _, a := range adapters {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
