package skillimport

import (
	"fmt"
	"net/url"
	"strings"
)

type Source struct {
	Type      string `json:"type"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Ref       string `json:"ref"`
	Path      string `json:"path"`
	CommitSHA string `json:"commit_sha"`
}

func ResolveURL(raw string) (Source, error) {
	if raw == "" {
		return Source{}, fmt.Errorf("empty URL")
	}

	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return Source{}, fmt.Errorf("parse URL: %w", err)
	}

	if u.Host != "github.com" {
		return Source{}, fmt.Errorf("unsupported host %q (only github.com is supported)", u.Host)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return Source{}, fmt.Errorf("expected github.com/owner/repo, got %q", u.Path)
	}

	s := Source{
		Type:  "github",
		Owner: parts[0],
		Repo:  parts[1],
	}

	// Format: /owner/repo/tree/ref[/path...]
	if len(parts) >= 4 && parts[2] == "tree" {
		s.Ref = parts[3]
		if len(parts) > 4 {
			s.Path = strings.Join(parts[4:], "/")
		}
	}

	return s, nil
}
