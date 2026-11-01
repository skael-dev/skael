// Package skills embeds the skills that ship inside the skael binary.
package skills

import _ "embed"

// Skael is the SKILL.md that tells an agent how to use the skael CLI.
//
//go:embed skael/SKILL.md
var Skael []byte
