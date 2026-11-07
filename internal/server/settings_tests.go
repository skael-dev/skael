package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/scan"
	"github.com/skael-dev/skael/internal/settings"
)

func settingsTesters(client *http.Client) map[string]settings.Tester {
	return map[string]settings.Tester{
		"smtp": testSMTP,
		"oidc": func(ctx context.Context, s *settings.Snapshot, _ string) settings.TestResult {
			return testSignIn(ctx, client, s)
		},
		"github_token": func(ctx context.Context, s *settings.Snapshot, _ string) settings.TestResult {
			return testGitHubToken(ctx, client, s)
		},
		"scanner": testScanner,
	}
}

func fail(format string, a ...any) settings.TestResult {
	return settings.TestResult{Message: fmt.Sprintf(format, a...)}
}

func pass(format string, a ...any) settings.TestResult {
	return settings.TestResult{OK: true, Message: fmt.Sprintf(format, a...)}
}

func testSMTP(ctx context.Context, s *settings.Snapshot, to string) settings.TestResult {
	host := s.String("smtp.host")
	if host == "" {
		return fail("Set the SMTP host first.")
	}
	if s.String("smtp.from") == "" {
		return fail("Set the from address first.")
	}
	err := auth.SendTestSMTP(ctx, host, s.Int("smtp.port"), s.String("smtp.username"), s.String("smtp.password"), s.String("smtp.from"), to)
	if err != nil {
		return fail("The email was not sent: %v", err)
	}
	return pass("A test email went to %s.", to)
}

func testSignIn(ctx context.Context, client *http.Client, s *settings.Snapshot) settings.TestResult {
	public := strings.TrimSuffix(s.String("public_url"), "/")
	if public == "" {
		return fail("Set the public URL on Settings → General first. Every callback URL is built from it.")
	}
	var lines []string
	if s.String("google.client_id") != "" {
		lines = append(lines, "Google redirect URI: "+public+"/api/auth/oauth/google/callback")
	}
	if s.String("github.client_id") != "" {
		lines = append(lines, "GitHub callback URL: "+public+"/api/auth/oauth/github/callback")
	}
	issuer := strings.TrimSuffix(s.String("oidc.issuer"), "/")
	if issuer != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
		if err != nil {
			return fail("The issuer URL is invalid: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return fail("The issuer did not answer: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			return fail("The issuer's discovery document returned HTTP %d.", resp.StatusCode)
		}
		var doc struct {
			Issuer string `json:"issuer"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
			return fail("The issuer's discovery document is not JSON.")
		}
		if strings.TrimSuffix(doc.Issuer, "/") != issuer {
			return fail("The discovery document names the issuer %q, not %q. Use the exact issuer URL.", doc.Issuer, issuer)
		}
		if s.String("oidc.client_id") == "" || s.String("oidc.client_secret") == "" {
			return fail("The issuer answers, but the OIDC client ID or secret is missing.")
		}
		lines = append(lines, "OIDC issuer answers. Redirect URI: "+public+"/api/auth/oauth/oidc/callback")
	}
	if len(lines) == 0 {
		return fail("No sign-in provider is set.")
	}
	return pass("%s", strings.Join(lines, "\n"))
}

func testGitHubToken(ctx context.Context, client *http.Client, s *settings.Snapshot) settings.TestResult {
	token := s.String("github_token")
	if token == "" {
		return fail("Set the GitHub token first.")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/rate_limit", nil)
	if err != nil {
		return fail("%v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return fail("GitHub did not answer: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusUnauthorized {
		return fail("GitHub refused the token. It is wrong, expired or revoked.")
	}
	if resp.StatusCode != http.StatusOK {
		return fail("GitHub returned HTTP %d.", resp.StatusCode)
	}
	var body struct {
		Resources struct {
			Core struct {
				Limit     int `json:"limit"`
				Remaining int `json:"remaining"`
			} `json:"core"`
		} `json:"resources"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fail("GitHub's answer is not JSON.")
	}
	return pass("The token works: %d of %d requests remain this hour.", body.Resources.Core.Remaining, body.Resources.Core.Limit)
}

const sampleSkill = `---
name: settings-test
description: A sample skill the scanner test runs on.
---

Read the README and summarise it.
`

func testScanner(ctx context.Context, s *settings.Snapshot, _ string) settings.TestResult {
	sc := scan.NewExternalScanner(s.String("external_scan.cmd"), s.Duration("external_scan.timeout"))
	if sc == nil {
		return fail("Set the scanner command first.")
	}
	dir, err := os.MkdirTemp("", "skael-scan-test-")
	if err != nil {
		return fail("%v", err)
	}
	defer os.RemoveAll(dir) //nolint:errcheck
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(sampleSkill), 0o600); err != nil {
		return fail("%v", err)
	}
	start := time.Now()
	findings, err := sc.Run(ctx, dir)
	if err != nil {
		return fail("The scanner failed: %v", err)
	}
	return pass("%s ran in %s and reported %d findings on the sample skill.", sc.Name, time.Since(start).Round(time.Millisecond), len(findings))
}
