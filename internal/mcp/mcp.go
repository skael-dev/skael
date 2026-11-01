// Package mcp serves the registry's tools over the stateless Streamable HTTP
// transport of the Model Context Protocol. No tool can publish.
package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/runs"
	"github.com/skael-dev/skael/internal/sets"
	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/submissions"
)

// Only this version is offered, because the older Streamable HTTP version
// requires JSON-RPC batches and this one removes them.
const protocolVersion = "2025-06-18"

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
	codeForbidden      = -32001
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type server struct {
	version     string
	corsOrigins []string
	trusted     *platform.TrustedProxies
	tools       []tool
}

// RegisterRoutes wires /mcp and /.well-known/mcp.json. The auth middleware
// guards /mcp and leaves the well-known document public.
func RegisterRoutes(router chi.Router, skills *skill.Store, setStore *sets.Store, runStore *runs.Store, subs *submissions.Service, cfg *platform.Config, version string) {
	s := &server{
		version: version,
		trusted: platform.ParseTrustedProxies(cfg.TrustedProxies),
		tools:   append(newTools(skills, setStore), reportingTools(runStore, subs)...),
	}
	for _, origin := range strings.Split(cfg.CORSOrigins, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			s.corsOrigins = append(s.corsOrigins, origin)
		}
	}
	router.Post("/mcp", s.handlePost)
	// A 405 tells a client that no server-to-client SSE stream exists.
	router.Get("/mcp", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	router.Get("/.well-known/mcp.json", s.handleWellKnown)
}

func (s *server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || slices.Contains(s.corsOrigins, origin) || slices.Contains(s.corsOrigins, "*") {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

func (s *server) handlePost(w http.ResponseWriter, r *http.Request) {
	// The transport requires this check, and /mcp also accepts session cookies.
	if !s.originAllowed(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		http.Error(w, "content type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" && v != protocolVersion {
		http.Error(w, "unsupported MCP protocol version", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(r.Body)
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.Error(w, "cannot read request body", http.StatusBadRequest)
		return
	}
	if !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, response{JSONRPC: "2.0", Error: &rpcError{codeParse, "parse error"}})
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil || req.JSONRPC != "2.0" {
		writeJSON(w, http.StatusBadRequest, response{JSONRPC: "2.0", Error: &rpcError{codeInvalidRequest, "invalid request: send one JSON-RPC 2.0 object"}})
		return
	}
	// A notification, or the client's reply to a server request.
	if len(req.ID) == 0 || strings.HasPrefix(req.Method, "notifications/") || (req.Method == "" && (req.Result != nil || req.Error != nil)) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !validID(req.ID) || req.Method == "" {
		writeJSON(w, http.StatusBadRequest, response{JSONRPC: "2.0", Error: &rpcError{codeInvalidRequest, "invalid request: id must be a string or a number, and method is required"}})
		return
	}

	result, err := s.dispatch(r, req)
	resp := response{JSONRPC: "2.0", ID: req.ID, Result: result}
	if err != nil {
		var rpcErr *rpcError
		if !errors.As(err, &rpcErr) {
			log.Error().Err(err).Str("method", req.Method).Msg("mcp request")
			rpcErr = &rpcError{codeInternal, "internal error"}
		}
		resp.Result, resp.Error = nil, rpcErr
	}
	writeJSON(w, http.StatusOK, resp)
}

func validID(id json.RawMessage) bool {
	id = bytes.TrimSpace(id)
	return len(id) > 0 && !bytes.Equal(id, []byte("null")) && id[0] != '{' && id[0] != '['
}

func (s *server) dispatch(r *http.Request, req request) (any, error) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		// A client that asks for another version receives ours and decides
		// whether to continue, as the protocol's negotiation defines.
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "skael", "version": s.version},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		return s.callTool(r, req.Params)
	default:
		return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
	}
}

func decodeParams(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return &rpcError{codeInvalidParams, "invalid params: " + err.Error()}
	}
	return nil
}

func (s *server) handleWellKnown(w http.ResponseWriter, r *http.Request) {
	type toolDoc struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Scope       string `json:"scope"`
	}
	docs := make([]toolDoc, len(s.tools))
	for i, t := range s.tools {
		docs[i] = toolDoc{t.Name, t.Description, t.scope}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":            "skael",
		"version":         s.version,
		"protocolVersion": protocolVersion,
		"transport": map[string]any{
			"type": "streamable-http",
			"url":  platform.RequestScheme(r, s.trusted) + "://" + r.Host + "/mcp",
		},
		"authentication": map[string]any{"type": "header", "header": "X-API-Key"},
		"tools":          docs,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Warn().Err(err).Msg("mcp write response")
	}
}
