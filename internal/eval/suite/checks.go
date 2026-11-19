package suite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ChecksDir holds the scripts a command check runs, under evals/. The runner
// copies it into the workspace only after the agent has finished.
const ChecksDir = "checks"

// Check types. Each is graded without a judge, after the session, in the
// sandbox with the network off.
const (
	CheckFileExists = "file_exists"
	CheckRegex      = "regex"
	CheckJSONSchema = "json_schema"
	CheckCommand    = "command"
)

// Check is one deterministic expectation of an eval. Path is relative to the
// workspace the agent ran in. Script is relative to evals/checks/.
type Check struct {
	Name    string          `json:"name,omitempty"`
	Type    string          `json:"type"`
	Path    string          `json:"path,omitempty"`
	Pattern string          `json:"pattern,omitempty"`
	Schema  json.RawMessage `json:"schema,omitempty"`
	Script  string          `json:"script,omitempty"`
}

// Label is how a report and a loss name the check. The "check:" prefix is what
// tells a reader the judge did not produce this verdict.
func (c Check) Label() string {
	name := c.Name
	if name == "" {
		switch c.Type {
		case CheckFileExists:
			name = "file_exists " + c.Path
		case CheckRegex:
			name = fmt.Sprintf("regex %s in %s", c.Pattern, c.Path)
		case CheckJSONSchema:
			name = "json_schema " + c.Path
		case CheckCommand:
			name = "command " + c.Script
		default:
			name = c.Type
		}
	}
	return "check: " + name
}

// ReadsFile reports whether the check inspects a file the session produced.
func (c Check) ReadsFile() bool {
	return c.Type == CheckFileExists || c.Type == CheckRegex || c.Type == CheckJSONSchema
}

// Problem returns why c cannot run, or "" when it can. dir holds evals/.
func (c Check) Problem(dir string) string {
	switch c.Type {
	case CheckFileExists, CheckRegex, CheckJSONSchema:
		if p := workspacePathProblem(c.Path); p != "" {
			return p
		}
	case CheckCommand:
		if c.Script == "" {
			return "a command check needs a script under evals/checks/"
		}
		if missing := missingFile(dir, ScriptPath(c.Script)); missing != "" {
			return "missing script " + missing
		}
	case "":
		return "a check needs a type"
	default:
		return fmt.Sprintf("unknown check type %q: want file_exists, regex, json_schema or command", c.Type)
	}
	switch c.Type {
	case CheckRegex:
		if c.Pattern == "" {
			return "a regex check needs a pattern"
		}
		if _, err := regexp.Compile(c.Pattern); err != nil {
			return "the pattern does not compile: " + err.Error()
		}
	case CheckJSONSchema:
		if _, err := compileSchema(c.Schema); err != nil {
			return "the schema does not compile: " + err.Error()
		}
	}
	return ""
}

// ScriptPath is a command check's script, relative to the directory that holds
// evals/.
func ScriptPath(script string) string {
	return path.Join(EvalsDir, ChecksDir, filepath.ToSlash(script))
}

// workspacePathProblem refuses a path that is absolute or leaves the
// workspace. The path is author input, and the runner quotes it into a shell.
func workspacePathProblem(p string) string {
	if p == "" {
		return "a file check needs a path"
	}
	slash := filepath.ToSlash(p)
	if path.IsAbs(slash) || filepath.IsAbs(p) {
		return fmt.Sprintf("path %q must be relative to the workspace", p)
	}
	clean := path.Clean(slash)
	if clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
		return fmt.Sprintf("path %q must name a file inside the workspace", p)
	}
	return ""
}

func compileSchema(raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("a json_schema check needs a schema")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	// An empty loader refuses every remote or file $ref, so a schema cannot
	// make the worker read its own disk or the network.
	c.UseLoader(jsonschema.SchemeURLLoader{})
	if err := c.AddResource("check.json", doc); err != nil {
		return nil, err
	}
	return c.Compile("check.json")
}

// CheckResult is one check's verdict on one session.
type CheckResult struct {
	Label    string `json:"label"`
	Passed   bool   `json:"passed"`
	Evidence string `json:"evidence"`
}

// FileState is what the sandbox saw of one file a check reads: whether it was
// a regular file, and its bytes when it was.
type FileState struct {
	Present bool
	Content []byte
}

// CommandState is one command check's exit code and the tail of its output.
type CommandState struct {
	ExitCode int
	Output   string
}

// Judge turns what the sandbox observed into a verdict. It is pure, so the
// same observation always grades the same way, whatever judge model runs.
func (c Check) Judge(f FileState, cmd CommandState) CheckResult {
	r := CheckResult{Label: c.Label()}
	switch c.Type {
	case CheckFileExists:
		r.Passed = f.Present
		r.Evidence = c.Path + " is missing"
		if f.Present {
			r.Evidence = c.Path + " exists"
		}
	case CheckRegex:
		if !f.Present {
			r.Evidence = c.Path + " is missing"
			return r
		}
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			r.Evidence = "the pattern does not compile: " + err.Error()
			return r
		}
		if loc := re.FindIndex(f.Content); loc != nil {
			r.Passed = true
			r.Evidence = fmt.Sprintf("%s matches: %q", c.Path, clip(string(f.Content[loc[0]:loc[1]]), 200))
		} else {
			r.Evidence = fmt.Sprintf("%s has no match for %s", c.Path, c.Pattern)
		}
	case CheckJSONSchema:
		if !f.Present {
			r.Evidence = c.Path + " is missing"
			return r
		}
		sch, err := compileSchema(c.Schema)
		if err != nil {
			r.Evidence = "the schema does not compile: " + err.Error()
			return r
		}
		inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(f.Content))
		if err != nil {
			r.Evidence = c.Path + " is not JSON: " + err.Error()
			return r
		}
		if err := sch.Validate(inst); err != nil {
			r.Evidence = clip(c.Path+" does not match the schema: "+err.Error(), 1000)
			return r
		}
		r.Passed = true
		r.Evidence = c.Path + " matches the schema"
	case CheckCommand:
		r.Passed = cmd.ExitCode == 0
		r.Evidence = fmt.Sprintf("%s exited %d", c.Script, cmd.ExitCode)
		if out := strings.TrimSpace(cmd.Output); out != "" {
			r.Evidence += ": " + out
		}
	default:
		r.Evidence = "unknown check type " + c.Type
	}
	return r
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// HasChecks reports whether any eval in set carries a check. A worker that
// predates checks must never run such a suite, because it would ignore them.
func (s *EvalSet) HasChecks() bool {
	for _, e := range s.Evals {
		if len(e.Checks) > 0 {
			return true
		}
	}
	return false
}
