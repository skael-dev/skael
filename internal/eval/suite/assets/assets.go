// Package assets embeds the static suite assets.
package assets

import "embed"

// Distractors holds the synthetic distractor pack used for trigger precision.
//
//go:embed distractors/distractors.yaml
var Distractors embed.FS
