package spec

import (
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load parses a SkillSpec from YAML. Unknown fields are an error: a spec with a
// misspelled key would otherwise silently lose whatever that key described.
func Load(r io.Reader) (*SkillSpec, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var s SkillSpec
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("spec.Load: %w", err)
	}
	return &s, nil
}

// Save writes the spec as block-style YAML with two-space indentation. A human
// reviews this output at the approval gate, so readability is a requirement.
func (s *SkillSpec) Save(w io.Writer) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("spec.Save: %w", err)
	}
	return enc.Close()
}

// DirName returns the on-disk directory name for the skill. Registry names may
// be namespaced with colons (superpowers:brainstorming) which the Agent Skills
// spec does not permit in a directory name, so the namespace is stripped.
func (s *SkillSpec) DirName() string {
	if i := strings.LastIndex(s.Name, ":"); i >= 0 {
		return s.Name[i+1:]
	}
	return s.Name
}
