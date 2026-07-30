package whetstone_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	whetstone "github.com/skael-dev/skael/cli/whetstone"
)

func TestRunReport_WritesTheHTMLBesideTheSkill(t *testing.T) {
	seedTwoEvals(t)
	path, err := whetstone.RunReport("demo", "latest", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".html" {
		t.Errorf("path = %q, want an HTML file", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "demo") {
		t.Error("the rendered report does not name the skill")
	}
}

func TestRunReport_OpenUsesTheInjectedOpener(t *testing.T) {
	seedTwoEvals(t)
	var opened string
	// Injected rather than shelling out: a test that launches a browser is a
	// test nobody runs twice.
	if _, err := whetstone.RunReport("demo", "latest", true, func(p string) error {
		opened = p
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if opened == "" {
		t.Error("--open did not call the opener")
	}
}
