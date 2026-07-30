package suite

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/skael-dev/skael/internal/eval/suite/assets"
)

// Distractor is one synthetic skill in the shipped distractor pack, used to
// measure trigger precision: a skill under test must stay silent on every
// distractor, however plausible its Tier makes it look.
type Distractor struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	// Tier is one of "near" (adjacent-domain near-miss), "mid", or "far"
	// (obviously unrelated). Measuring trigger precision against only
	// obviously-irrelevant skills tests nothing, so the near tier is what
	// makes the pack discriminating.
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
