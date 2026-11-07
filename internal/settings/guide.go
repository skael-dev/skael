package settings

import (
	"embed"
	"strings"
)

//go:embed guides/*.md
var guides embed.FS

// PlaceholderURL stands for the instance's public URL in a guide. The settings
// page replaces it with the real one.
const PlaceholderURL = "https://skael.example.com"

// Guide returns the section's guide in Markdown, or "".
func Guide(section string) string {
	b, err := guides.ReadFile("guides/" + section + ".md")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
