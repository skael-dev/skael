package settings

import (
	"fmt"
	"strings"
)

// ConfigDocs renders configuration.md.
func ConfigDocs() string {
	var b strings.Builder
	b.WriteString("---\ntitle: Configuration\ndescription: Every setting the server and the worker read, and where to change it.\n---\n\n")
	b.WriteString("<!-- Generated from internal/settings by `go run ./cmd/server --config-docs`. Do not edit. -->\n\n")
	b.WriteString("An owner or admin changes most server settings on the Settings page, and a change applies on every replica without a restart. ")
	b.WriteString("An environment variable wins over the page and locks its field. Settings marked *env only* are read before the database, so they are set only in the environment.\n\n")
	b.WriteString("A secret field on the page takes a value, which the server seals under the master key, or a reference: `env:NAME` reads another variable, and `file:/path` reads a mounted file.\n")
	for _, bin := range []Binary{BinaryServer, BinaryWorker} {
		if bin == BinaryServer {
			b.WriteString("\n## Server\n")
		} else {
			b.WriteString("\n## Worker\n\n`skael-worker` reads only its own environment.\n")
		}
		for _, s := range Sections {
			if s.Binary != bin {
				continue
			}
			fmt.Fprintf(&b, "\n### %s\n\n", s.Title)
			if g := Guide(s.ID); g != "" {
				b.WriteString(strings.ReplaceAll(g, "\n### ", "\n#### "))
				b.WriteString("\n\n")
			}
			b.WriteString("| Variable | Where | Default | Description |\n|---|---|---|---|\n")
			for i := range Defs {
				d := &Defs[i]
				if d.Section != s.ID {
					continue
				}
				where := "Settings page"
				if d.EnvOnly {
					where = "env only"
				}
				if d.Required != "" {
					where += ", required " + d.Required
				}
				def := "—"
				if d.Default != "" {
					def = "`" + d.Default + "`"
				}
				desc := d.Help
				if d.Doc != "" {
					desc = d.Doc
				}
				if len(d.Options) > 0 {
					desc += " One of: " + joinOptions(d.Options) + "."
				}
				fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", d.Env, where, def, escapeCell(desc))
			}
		}
	}
	return b.String()
}

func joinOptions(opts []SettingsOption) string {
	vals := make([]string, len(opts))
	for i, o := range opts {
		vals[i] = "`" + o.Value + "`"
	}
	return strings.Join(vals, ", ")
}

func escapeCell(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// EnvExample renders .env.example.
func EnvExample() string {
	var b strings.Builder
	b.WriteString("# Generated from internal/settings by `go run ./cmd/server --env-example`. Do not edit.\n")
	b.WriteString("# Most server settings can also be changed on the Settings page. A variable set\n")
	b.WriteString("# here wins over the page and locks its field.\n")
	for _, s := range Sections {
		fmt.Fprintf(&b, "\n# --- %s (%s) ---\n", s.Title, s.Binary)
		for i := range Defs {
			d := &Defs[i]
			if d.Section != s.ID {
				continue
			}
			for _, line := range wrap(d.Help, 76) {
				b.WriteString("# " + line + "\n")
			}
			if d.Key == "database_url" {
				b.WriteString("DATABASE_URL=postgres://skael:skael@localhost:5432/skael?sslmode=disable\n")
				continue
			}
			val := d.Default
			if strings.Contains(val, " ") || strings.Contains(val, "{") {
				val = ""
			}
			fmt.Fprintf(&b, "# %s=%s\n", d.Env, val)
		}
	}
	return b.String()
}

func wrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		if line != "" && len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		if line == "" {
			line = w
		} else {
			line += " " + w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
