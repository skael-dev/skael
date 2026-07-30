package spec

import (
	"fmt"
	"regexp"
)

// specName is the Agent Skills spec name rule. Deliberately stricter than the
// registry's own rule, which additionally allows ':' and '.' for namespacing —
// a generated skill must be spec-compliant, so it is linted against the spec.
var specName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Validate reports every structural problem with the IR, rather than stopping
// at the first — the interview's self-critique pass feeds the whole list back
// to the model in one call.
func (s *SkillSpec) Validate() []error {
	var errs []error

	switch {
	case s.Name == "":
		errs = append(errs, fmt.Errorf("name is required"))
	case len(s.DirName()) > MaxName:
		errs = append(errs, fmt.Errorf("name %q exceeds the 64-character spec limit", s.DirName()))
	case !specName.MatchString(s.DirName()):
		errs = append(errs, fmt.Errorf("name %q must be lowercase kebab-case", s.DirName()))
	}

	if s.Purpose == "" {
		errs = append(errs, fmt.Errorf("purpose is required"))
	}
	if s.Description == "" {
		errs = append(errs, fmt.Errorf("description is required"))
	} else if len(s.Description) > MaxDescription {
		errs = append(errs, fmt.Errorf("description is %d bytes, exceeding the 1024-byte spec limit", len(s.Description)))
	}

	if len(s.Steps) == 0 {
		errs = append(errs, fmt.Errorf("spec needs at least one step"))
	}
	seen := make(map[string]bool, len(s.Steps))
	for i, st := range s.Steps {
		switch {
		case st.ID == "":
			errs = append(errs, fmt.Errorf("step %d has no id", i))
		case seen[st.ID]:
			errs = append(errs, fmt.Errorf("duplicate step id %q", st.ID))
		default:
			seen[st.ID] = true
		}
		if st.Action == "" {
			errs = append(errs, fmt.Errorf("step %q has no action", st.ID))
		}
		if st.Postcondition == "" {
			errs = append(errs, fmt.Errorf("step %q has no postcondition — every step must state a verifiable outcome", st.ID))
		}
	}

	var positive int
	for _, t := range s.Triggers {
		if !t.Negative {
			positive++
		}
	}
	if positive == 0 {
		errs = append(errs, fmt.Errorf("spec needs at least one positive trigger phrase"))
	}

	if n := s.Resources.Count(); n > MaxModules {
		errs = append(errs, fmt.Errorf("resource plan plans %d modules, at most 3 are allowed", n))
	}

	for _, c := range s.Constraints {
		if c.Kind != RuleMust && c.Kind != RuleMustNot {
			errs = append(errs, fmt.Errorf("constraint %q has invalid kind %q", c.ID, c.Kind))
		}
		switch c.Severity {
		case SeverityCritical, SeverityMajor, SeverityMinor:
		default:
			errs = append(errs, fmt.Errorf("constraint %q has invalid severity %q", c.ID, c.Severity))
		}
	}

	switch s.TargetTier {
	case TierFloor, TierMid, TierStrong:
	case "":
		errs = append(errs, fmt.Errorf("target_tier is required"))
	default:
		errs = append(errs, fmt.Errorf("invalid target_tier %q", s.TargetTier))
	}

	return errs
}
