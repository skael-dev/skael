package agent

// Get returns the adapter named name.
func Get(name string) (Adapter, bool) {
	if name == (&ClaudeCode{}).Name() {
		return New(), true
	}
	return nil, false
}

// All returns every adapter this binary can drive.
func All() []Adapter { return []Adapter{New()} }
