//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
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
		require.True(t, tool.Annotations.ReadOnlyHint)
	}
	require.ElementsMatch(t, []string{"search_skills", "get_skill", "list_sets", "resolve_set"}, names)

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
