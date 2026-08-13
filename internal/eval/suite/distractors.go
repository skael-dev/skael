package suite

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/skael-dev/skael/internal/eval/suite/assets"
)

// Distractor is one synthetic skill for trigger-precision measurement.
type Distractor struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Tier is "near", "mid", or "far".
	Tier string `yaml:"tier"`
}

// Distractors returns the shipped synthetic distractor pack.
func Distractors() ([]Distractor, error) {
	b, err := assets.Distractors.ReadFile("distractors/distractors.yaml")
	if err != nil {
		return nil, fmt.Errorf("suite: reading distractor pack: %w", err)
	}
	var d []Distractor
	if err := yaml.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("suite: parsing distractor pack: %w", err)
	}
	return d, nil
}
