package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type ImportSource struct {
	Type      string `json:"type"`
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	Ref       string `json:"ref"`
	Path      string `json:"path"`
	CommitSHA string `json:"commit_sha"`
}

type DiscoveredSkill struct {
	Name              string      `json:"name"`
	Description       string      `json:"description"`
	Path              string      `json:"path"`
	Files             []FileEntry `json:"files"`
	ScanStatus        string      `json:"scan_status"`
	ScanFindingsCount int         `json:"scan_findings_count"`
	ExistingVersion   int         `json:"existing_version"`
}

type FileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type ResolveResponse struct {
	Source     ImportSource      `json:"source"`
	Skills     []DiscoveredSkill `json:"skills"`
	PluginName string            `json:"plugin_name,omitempty"`
}

type ImportedSkill struct {
	Name       string `json:"name"`
	Version    int    `json:"version"`
	ScanStatus string `json:"scan_status"`
	Created    bool   `json:"created"`
}

type FailedSkill struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type ImportResponse struct {
	Imported []ImportedSkill `json:"imported"`
	Failed   []FailedSkill   `json:"failed"`
}

type ImportSourceEntry struct {
	SkillName  string `json:"skill_name"`
	SourceType string `json:"source_type"`
	SourceURL  string `json:"source_url"`
	SourcePath string `json:"source_path"`
	SourceRef  string `json:"source_ref"`
	CommitSHA  string `json:"commit_sha"`
	ImportedAt string `json:"imported_at"`
}

func (c *Client) ImportResolve(url string) (*ResolveResponse, error) {
	payload, err := json.Marshal(map[string]string{"url": url})
	if err != nil {
		return nil, fmt.Errorf("marshal resolve request: %w", err)
	}

	resp, err := c.do(http.MethodPost, "/api/import/resolve", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result ResolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode resolve response: %w", err)
	}
	return &result, nil
}

func (c *Client) ImportSkills(source ImportSource, skillNames []string, namespace string) (*ImportResponse, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"source":    source,
		"skills":    skillNames,
		"namespace": namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal import request: %w", err)
	}

	resp, err := c.do(http.MethodPost, "/api/import", bytes.NewReader(payload), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result ImportResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode import response: %w", err)
	}
	return &result, nil
}

func (c *Client) ImportUpload(archive []byte) (*ResolveResponse, error) {
	resp, err := c.do(http.MethodPost, "/api/import/upload", bytes.NewReader(archive), "application/gzip")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result ResolveResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode upload response: %w", err)
	}
	return &result, nil
}

func (c *Client) ImportSources() ([]ImportSourceEntry, error) {
	resp, err := c.do(http.MethodGet, "/api/import/sources", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var sources []ImportSourceEntry
	if err := json.Unmarshal(raw, &sources); err != nil {
		return nil, fmt.Errorf("decode sources: %w", err)
	}
	return sources, nil
}
