package skillimport

import (
	"testing"
)

func TestResolveGitHubURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Source
		wantErr bool
	}{
		{
			name:  "full URL with tree path",
			input: "https://github.com/anthropics/skills/tree/main/skills/skill-creator",
			want: Source{
				Type:  "github",
				Owner: "anthropics",
				Repo:  "skills",
				Ref:   "main",
				Path:  "skills/skill-creator",
			},
		},
		{
			name:  "repo root only",
			input: "https://github.com/anthropics/skills",
			want: Source{
				Type:  "github",
				Owner: "anthropics",
				Repo:  "skills",
				Ref:   "",
				Path:  "",
			},
		},
		{
			name:  "no scheme",
			input: "github.com/anthropics/skills",
			want: Source{
				Type:  "github",
				Owner: "anthropics",
				Repo:  "skills",
				Ref:   "",
				Path:  "",
			},
		},
		{
			name:  "tag ref",
			input: "https://github.com/anthropics/skills/tree/v1.2.0",
			want: Source{
				Type:  "github",
				Owner: "anthropics",
				Repo:  "skills",
				Ref:   "v1.2.0",
				Path:  "",
			},
		},
		{
			name:  "trailing slash stripped",
			input: "https://github.com/anthropics/skills/",
			want: Source{
				Type:  "github",
				Owner: "anthropics",
				Repo:  "skills",
				Ref:   "",
				Path:  "",
			},
		},
		{
			name:    "not github",
			input:   "https://gitlab.com/foo/bar",
			wantErr: true,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Type != tt.want.Type || got.Owner != tt.want.Owner || got.Repo != tt.want.Repo || got.Ref != tt.want.Ref || got.Path != tt.want.Path {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
