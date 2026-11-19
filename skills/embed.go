// Package skills embeds the skills that ship inside the skael binary.
package skills

import (
	"embed"
	"io/fs"
)

// Skael is the SKILL.md that tells an agent how to use the skael CLI.
//
//go:embed skael/SKILL.md
var Skael []byte

//go:embed skael-evals
var evals embed.FS

// Evals is the skael-evals skill folder, which tells an agent how to write a
// suite that passes skael eval check.
var Evals = mustSub(evals, "skael-evals")

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
