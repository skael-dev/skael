//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skael-dev/skael/internal/auth"
)

type mcpReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (a *machineAPI) mcp(key, method string, params any) mcpReply {
	a.t.Helper()
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(a.t, err)
	req, err := http.NewRequest(http.MethodPost, a.base+"/mcp", bytes.NewReader(raw))
	require.NoError(a.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	require.Equalf(a.t, http.StatusOK, resp.StatusCode, "%s: %s", method, data)
	var reply mcpReply
	require.NoError(a.t, json.Unmarshal(data, &reply), string(data))
	return reply
}

func (a *machineAPI) mcpTool(key, name string, args map[string]any) mcpReply {
	a.t.Helper()
	return a.mcp(key, "tools/call", map[string]any{"name": name, "arguments": args})
}

func requireForbidden(t *testing.T, reply mcpReply, message string) {
	t.Helper()
	require.NotNil(t, reply.Error, string(reply.Result))
	require.Equal(t, -32001, reply.Error.Code)
	require.Contains(t, reply.Error.Message, message)
}

func TestMCPToolsFollowKeyScopes(t *testing.T) {
	a := startMachineServer(t)
	a.publish("alpha", "first")
	var one struct {
		ID string `json:"id"`
	}
	a.sessionJSON(http.MethodPost, "/api/sets", map[string]any{"name": "one", "entries": []map[string]any{{"name": "alpha"}}}, http.StatusCreated, &one)
	a.sessionJSON(http.MethodPost, "/api/sets", map[string]any{"name": "two", "entries": []map[string]any{{"name": "alpha"}}}, http.StatusCreated, nil)

	var personal struct {
		Key string `json:"key"`
	}
	a.sessionJSON(http.MethodPost, "/api/auth/keys", map[string]any{"name": "laptop"}, http.StatusCreated, &personal)
	_, skillsOnly := a.serviceKey("reader", []string{auth.ScopeSkillsRead}, "")
	_, limited := a.serviceKey("limited", []string{auth.ScopeSkillsRead, auth.ScopeSetsRead}, one.ID)

	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools map[string]any `json:"tools"`
		} `json:"capabilities"`
	}
	reply := a.mcp(personal.Key, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	require.NoError(t, json.Unmarshal(reply.Result, &init))
	require.Equal(t, "2025-06-18", init.ProtocolVersion)
	require.NotNil(t, init.Capabilities.Tools)

	var list struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Type string `json:"type"`
			} `json:"inputSchema"`
			Annotations struct {
				ReadOnlyHint bool `json:"readOnlyHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(a.mcp(skillsOnly, "tools/list", nil).Result, &list))
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
		require.Equal(t, "object", tool.InputSchema.Type)
		readOnly := !slices.Contains([]string{"report_outcome", "submit_skill", "get_submission"}, tool.Name)
		require.Equal(t, readOnly, tool.Annotations.ReadOnlyHint, tool.Name)
	}
	require.ElementsMatch(t, []string{"search_skills", "get_skill", "list_sets", "resolve_set", "report_outcome", "submit_skill", "get_submission"}, names)

	require.Nil(t, a.mcpTool(skillsOnly, "search_skills", map[string]any{"query": "alpha"}).Error)
	requireForbidden(t, a.mcpTool(skillsOnly, "resolve_set", map[string]any{"name": "one"}), "forbidden: this key lacks sets:read")

	var resolved struct {
		StructuredContent wireResolution `json:"structuredContent"`
		Content           []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	reply = a.mcpTool(personal.Key, "resolve_set", map[string]any{"name": "one"})
	require.Nil(t, reply.Error)
	require.NoError(t, json.Unmarshal(reply.Result, &resolved))
	require.True(t, resolved.StructuredContent.Complete)
	require.Equal(t, "alpha", resolved.StructuredContent.Entries[0].Name)
	require.Equal(t, "text", resolved.Content[0].Type)
	require.Contains(t, resolved.Content[0].Text, resolved.StructuredContent.ID)

	requireForbidden(t, a.mcpTool(limited, "search_skills", map[string]any{"query": "alpha"}), "forbidden")
	requireForbidden(t, a.mcpTool(limited, "get_skill", map[string]any{"name": "alpha"}), "forbidden")
	requireForbidden(t, a.mcpTool(limited, "resolve_set", map[string]any{"name": "two"}), "forbidden")
	requireForbidden(t, a.mcpTool(limited, "resolve_set", map[string]any{"name": "absent"}), "forbidden")
	require.Nil(t, a.mcpTool(limited, "resolve_set", map[string]any{"name": "one"}).Error)

	var sets struct {
		StructuredContent struct {
			Sets []struct {
				Name string `json:"name"`
			} `json:"sets"`
		} `json:"structuredContent"`
	}
	require.NoError(t, json.Unmarshal(a.mcpTool(limited, "list_sets", nil).Result, &sets))
	require.Len(t, sets.StructuredContent.Sets, 1)
	require.Equal(t, "one", sets.StructuredContent.Sets[0].Name)
}

type mcpResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent wireSubmission `json:"structuredContent"`
}

func (a *machineAPI) mcpResult(key, name string, args map[string]any) mcpResult {
	a.t.Helper()
	reply := a.mcpTool(key, name, args)
	require.Nilf(a.t, reply.Error, "%s", name)
	var out mcpResult
	require.NoError(a.t, json.Unmarshal(reply.Result, &out), string(reply.Result))
	return out
}

func TestMCPReportingToolsFollowKeyScopes(t *testing.T) {
	a := startMachineServer(t)
	a.publish("alpha", "first")
	var one struct {
		ID string `json:"id"`
	}
	a.sessionJSON(http.MethodPost, "/api/sets", map[string]any{"name": "one", "entries": []map[string]any{{"name": "alpha"}}}, http.StatusCreated, &one)

	_, outcomes := a.serviceKey("factoree", []string{auth.ScopeOutcomesWrite}, "")
	_, submitter := a.serviceKey("drafter", []string{auth.ScopeSubmissionsWrite}, "")
	_, other := a.serviceKey("other", []string{auth.ScopeSubmissionsWrite}, "")
	_, limited := a.serviceKey("limited", []string{auth.ScopeSetsRead, auth.ScopeOutcomesWrite, auth.ScopeSubmissionsWrite}, one.ID)
	owner := a.ownerKey()

	outcome := map[string]any{"external_run_id": "run-1", "outcome": "succeeded", "repairs": 2}
	files := []map[string]any{
		{"path": "SKILL.md", "content": "---\nname: alpha\ndescription: from an agent\n---\n\nsecond, from an agent\n"},
		{"path": "notes/more.md", "content": "more\n"},
	}
	submission := map[string]any{"name": "alpha", "rationale": "because", "run_link": "https://ci.example/1", "files": files}

	// Each tool checks its own scope, and a set-limited key reaches none of them.
	requireForbidden(t, a.mcpTool(submitter, "report_outcome", outcome), "lacks outcomes:write")
	requireForbidden(t, a.mcpTool(outcomes, "submit_skill", submission), "lacks submissions:write")
	requireForbidden(t, a.mcpTool(outcomes, "get_submission", map[string]any{"id": "x"}), "lacks submissions:write")
	requireForbidden(t, a.mcpTool(limited, "report_outcome", outcome), "limited to one set")
	requireForbidden(t, a.mcpTool(limited, "submit_skill", submission), "limited to one set")
	requireForbidden(t, a.mcpTool(limited, "get_submission", map[string]any{"id": "x"}), "limited to one set")

	// A personal key has no consumer.
	res := a.mcpResult(owner, "report_outcome", outcome)
	require.True(t, res.IsError)
	require.Contains(t, res.Content[0].Text, "only a service account")

	require.False(t, a.mcpResult(outcomes, "report_outcome", outcome).IsError)
	res = a.mcpResult(outcomes, "report_outcome", map[string]any{"external_run_id": "run-1", "outcome": "bogus"})
	require.True(t, res.IsError)
	require.Contains(t, res.Content[0].Text, "outcome must be")

	// A file path stays inside the skill, and one of the two forms is required.
	for _, bad := range []string{"../escape.md", "/etc/x", "a/../b", "a\\b", ""} {
		reply := a.mcpTool(submitter, "submit_skill", map[string]any{
			"name": "alpha", "rationale": "because",
			"files": []map[string]any{{"path": "SKILL.md", "content": "x"}, {"path": bad, "content": "x"}},
		})
		require.NotNilf(t, reply.Error, "path %q", bad)
		require.Equal(t, -32602, reply.Error.Code)
	}
	require.NotNil(t, a.mcpTool(submitter, "submit_skill", map[string]any{"name": "alpha", "rationale": "because"}).Error)
	require.NotNil(t, a.mcpTool(submitter, "submit_skill", map[string]any{
		"name": "alpha", "rationale": "because", "files": files, "archive_base64": submissionArchive(t, "alpha", "x"),
	}).Error)

	res = a.mcpResult(submitter, "submit_skill", map[string]any{"name": "alpha", "rationale": "because", "archive_base64": "%%%"})
	require.True(t, res.IsError)

	// A secret stores nothing, and the model reads why.
	res = a.mcpResult(submitter, "submit_skill", map[string]any{
		"name": "alpha", "rationale": "because", "archive_base64": submissionArchive(t, "alpha", "Use the key AKIA"+"IOSFODNN7EXAMPLE for this."),
	})
	require.True(t, res.IsError)
	require.Contains(t, res.Content[0].Text, "archive rejected")

	res = a.mcpResult(submitter, "submit_skill", submission)
	require.False(t, res.IsError, res.Content)
	id := res.StructuredContent.ID
	require.NotEmpty(t, id)
	require.Equal(t, "pending", res.StructuredContent.State)

	// The packed files reach the reviewer, and nothing is published.
	var versions struct {
		Versions []json.RawMessage `json:"versions"`
	}
	a.sessionJSON(http.MethodGet, "/api/skills/alpha/versions", nil, http.StatusOK, &versions)
	require.Len(t, versions.Versions, 1)
	var diff struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	a.sessionJSON(http.MethodGet, "/api/submissions/"+id+"/diff", nil, http.StatusOK, &diff)
	require.Len(t, diff.Files, 2)

	a.sessionJSON(http.MethodPost, "/api/submissions/"+id+"/decision", map[string]any{"action": "reject", "reason": "not this"}, http.StatusOK, nil)

	res = a.mcpResult(submitter, "get_submission", map[string]any{"id": id})
	require.False(t, res.IsError)
	require.Equal(t, "rejected", res.StructuredContent.State)
	require.Equal(t, "not this", res.StructuredContent.Reason)

	// Another consumer's key does not learn that it exists.
	res = a.mcpResult(other, "get_submission", map[string]any{"id": id})
	require.True(t, res.IsError)
	require.Contains(t, res.Content[0].Text, "not found")
}
