package settings

import (
	"strconv"
	"strings"
	"time"
)

// EnvFunc is os.LookupEnv, injectable for tests.
type EnvFunc func(string) (string, bool)

// Source is where a resolved value came from.
type Source string

const (
	SourceDefault Source = "default"
	SourceUI      Source = "ui"
	SourceEnv     Source = "env"
)

// Opener opens a sealed value. platform.Keyring implements it.
type Opener interface {
	Open(value, aad string) ([]byte, error)
}

// Stored is one row of the settings table. A secret keeps either a
// reference in Value or a sealed literal in Sealed.
type Stored struct {
	Value     string
	Sealed    string
	UpdatedBy string
	UpdatedAt time.Time
}

// Resolved is one setting's effective value and where it came from.
type Resolved struct {
	Def    *Def
	Value  string
	Source Source
	// Reference is the env: or file: reference a secret was read from.
	Reference string
	// Problem says why a configured value is not in effect, such as an
	// invalid env value or a reference that names nothing.
	Problem string
	// Unreadable is a sealed secret that no master key opens.
	Unreadable bool
	UpdatedBy  string
	UpdatedAt  time.Time
}

// Snapshot is every setting at one version. It never changes once built.
type Snapshot struct {
	Version int64
	values  map[string]*Resolved
}

func aad(key string) string { return "settings:" + key }

// Resolve builds a snapshot. An env var wins over a stored value and locks it.
func Resolve(version int64, env EnvFunc, stored map[string]Stored, keys Opener) *Snapshot {
	s := &Snapshot{Version: version, values: make(map[string]*Resolved, len(Defs))}
	for i := range Defs {
		d := &Defs[i]
		r := &Resolved{Def: d, Value: d.Default, Source: SourceDefault}
		if v, ok := env(d.Env); ok && v != "" {
			if err := d.Validate(v); err != nil {
				r.Problem = d.Env + "=" + v + " is invalid: " + err.Error() + "; the default is in effect"
			} else {
				r.Value, r.Source = v, SourceEnv
			}
			s.values[d.Key] = r
			continue
		}
		if row, ok := stored[d.Key]; ok && !d.EnvOnly {
			r.UpdatedBy, r.UpdatedAt = row.UpdatedBy, row.UpdatedAt
			resolveStored(d, row, env, keys, r)
		}
		s.values[d.Key] = r
	}
	return s
}

func resolveStored(d *Def, row Stored, env EnvFunc, keys Opener, r *Resolved) {
	if d.Kind != KindSecret {
		if err := d.Validate(row.Value); err != nil {
			r.Problem = "the saved value is invalid: " + err.Error()
			return
		}
		r.Value, r.Source = row.Value, SourceUI
		return
	}
	if row.Value != "" {
		r.Reference = row.Value
		v, err := resolveReference(row.Value, env)
		if err != nil {
			r.Problem = "the reference " + row.Value + " does not resolve: " + err.Error()
			return
		}
		r.Value, r.Source = v, SourceUI
		return
	}
	if row.Sealed == "" {
		return
	}
	if keys == nil {
		r.Problem, r.Unreadable = "no master key can open the saved secret", true
		return
	}
	plain, err := keys.Open(row.Sealed, aad(d.Key))
	if err != nil {
		r.Problem, r.Unreadable = "the saved secret does not open with the current master key; enter it again", true
		return
	}
	r.Value, r.Source = string(plain), SourceUI
}

// Get returns the resolved setting, or nil for an unknown key.
func (s *Snapshot) Get(key string) *Resolved { return s.values[key] }

// String returns the effective value of key.
func (s *Snapshot) String(key string) string {
	if r := s.values[key]; r != nil {
		return r.Value
	}
	return ""
}

// Bool returns key as a bool.
func (s *Snapshot) Bool(key string) bool { return s.String(key) == "true" }

// Int returns key as an int, or 0.
func (s *Snapshot) Int(key string) int {
	n, _ := strconv.Atoi(s.String(key))
	return n
}

// Float returns key as a float, or 0.
func (s *Snapshot) Float(key string) float64 {
	f, _ := strconv.ParseFloat(s.String(key), 64)
	return f
}

// Duration returns key as a duration, or 0.
func (s *Snapshot) Duration(key string) time.Duration {
	d, _ := time.ParseDuration(s.String(key))
	return d
}

// List returns key split on commas, trimmed, without empty items.
func (s *Snapshot) List(key string) []string {
	var out []string
	for _, item := range strings.Split(s.String(key), ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// Problems returns every setting whose configured value is not in effect.
func (s *Snapshot) Problems() []*Resolved {
	var out []*Resolved
	for i := range Defs {
		if r := s.values[Defs[i].Key]; r.Problem != "" {
			out = append(out, r)
		}
	}
	return out
}
