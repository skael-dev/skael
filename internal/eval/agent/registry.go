package agent

// Get returns the adapter named name.
//
// There was a registry here: a map, a mutex, a Register called from each
// adapter's init, and a blank import in every binary to make that init run. It
// bought one thing — adding an adapter without touching a lookup — and cost a
// failure mode where a forgotten import compiled clean and silently emptied the
// panel. With one adapter left, the map is a lookup with one key, so this is
// the same function with the failure mode removed.
//
// Adapter stays an interface even so, because the runner's tests substitute a
// fake for it and would otherwise need a container and a CLI to run at all.
func Get(name string) (Adapter, bool) {
	if name == (&ClaudeCode{}).Name() {
		return New(), true
	}
	return nil, false
}

// All returns every adapter this binary can drive.
func All() []Adapter { return []Adapter{New()} }
