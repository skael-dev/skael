package providers

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/skael-dev/skael/internal/evalqueue"
)

// Preset is one common provider, set up with its key in one step. A preset
// with default models fills an empty panel, so an eval runs right after one
// key.
type Preset struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Type        string `json:"type"`
	// NeedsBaseURL asks the owner for the endpoint.
	NeedsBaseURL   bool   `json:"needs_base_url"`
	DefaultBaseURL string `json:"default_base_url,omitempty"`
	BaseURLHint    string `json:"base_url_hint,omitempty"`
	// ModelHint says what a model id means for this provider.
	ModelHint    string   `json:"model_hint,omitempty"`
	JudgeModel   string   `json:"judge_model,omitempty"`
	MemberModels []string `json:"member_models"`
	name         string
}

// Presets in the order the setup shows them. The default models are the
// newest of each vendor's tiers in the models.dev catalog on 2026-11-10.
var Presets = []Preset{
	{
		ID: "openrouter", Title: "OpenRouter", name: "OpenRouter", Type: evalqueue.ProviderOpenRouter,
		Description:  "The judge and the panel run through OpenRouter with one key, on any model it serves.",
		JudgeModel:   "anthropic/claude-opus-5.5",
		MemberModels: []string{"anthropic/claude-sonnet-5.5", "anthropic/claude-haiku-5.5"},
	},
	{
		ID: "anthropic", Title: "Anthropic", name: "Anthropic", Type: evalqueue.ProviderAnthropic,
		Description:  "The judge and the panel run on Anthropic's API.",
		JudgeModel:   "claude-opus-5-5",
		MemberModels: []string{"claude-sonnet-5-5", "claude-haiku-5-5"},
	},
	{
		ID: "openai", Title: "OpenAI", name: "OpenAI", Type: evalqueue.ProviderOpenAI,
		Description:  "The judge and the panel run on OpenAI's API.",
		JudgeModel:   "gpt-6.1-sol",
		MemberModels: []string{"gpt-6.1-sol", "gpt-6-luna"},
	},
	{
		ID: "azure", Title: "Azure AI Foundry", name: "Azure AI Foundry", Type: evalqueue.ProviderOpenAICompatible,
		Description:  "An Azure OpenAI or Foundry resource through its v1 endpoint. Choose the judge and panel after setup.",
		NeedsBaseURL: true, BaseURLHint: "https://<resource>.openai.azure.com/openai/v1",
		ModelHint:    "Each model id is a deployment name in your resource.",
		MemberModels: []string{},
	},
	{
		ID: "openai-compatible", Title: "Another OpenAI-compatible endpoint", name: "OpenAI-compatible", Type: evalqueue.ProviderOpenAICompatible,
		Description:  "LiteLLM, vLLM or any endpoint that serves /chat/completions. Choose the judge and panel after setup.",
		NeedsBaseURL: true, BaseURLHint: "http://litellm:4000/v1",
		MemberModels: []string{},
	},
}

func presetByID(id string) *Preset {
	for i := range Presets {
		if Presets[i].ID == id {
			return &Presets[i]
		}
	}
	return nil
}

// ApplyPreset creates the preset's provider with its key, or updates the one
// with the same name. When no panel is set and the preset has default models,
// it sets them.
func (s *Store) ApplyPreset(ctx context.Context, id, baseURL, authHeader, key, actor string) (*Provider, error) {
	p := presetByID(id)
	if p == nil {
		return nil, invalid("unknown preset %q", id)
	}
	if strings.TrimSpace(key) == "" {
		return nil, invalid("enter the provider's API key, or an env: or file: reference to it")
	}
	if baseURL == "" {
		baseURL = p.DefaultBaseURL
	}
	if p.NeedsBaseURL && strings.TrimSpace(baseURL) == "" {
		return nil, invalid("this provider needs its base URL, such as %s", p.BaseURLHint)
	}
	prov := Provider{Name: p.name, Type: p.Type, BaseURL: baseURL, AuthHeader: authHeader}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT id FROM providers WHERE name = $1`, prov.Name).Scan(&prov.ID); err != nil && err != pgx.ErrNoRows {
			return err
		}
		prov.key = key
		if err := s.upsertProvider(ctx, tx, &prov, actor); err != nil {
			return err
		}
		var hasPanel bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM eval_panel WHERE id = 1)`).Scan(&hasPanel); err != nil {
			return err
		}
		if hasPanel || p.JudgeModel == "" || len(p.MemberModels) == 0 {
			return nil
		}
		members := make([]Slot, len(p.MemberModels))
		for i, m := range p.MemberModels {
			members[i] = Slot{ProviderID: prov.ID, Model: m}
		}
		return setPanel(ctx, tx, Panel{Judge: Slot{ProviderID: prov.ID, Model: p.JudgeModel}, Members: members}, actor)
	})
	if err != nil {
		return nil, err
	}
	return &prov, nil
}
