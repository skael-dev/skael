package providers

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Preset is one common judge and panel combination, set up in one step.
type Preset struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// NeedsBaseURL asks the owner for a gateway's address.
	NeedsBaseURL   bool     `json:"needs_base_url"`
	DefaultBaseURL string   `json:"default_base_url,omitempty"`
	WorkerEnv      []string `json:"worker_env"`
	// Subscription marks a preset whose panel bills a Claude subscription.
	Subscription bool `json:"subscription"`

	build func(baseURL string) ([]Provider, func(ids map[string]string) Panel)
}

const (
	anthropicName    = "Anthropic"
	subscriptionName = "Claude subscription"
	openRouterURL    = "https://openrouter.ai/api"
	judgeModel       = "claude-opus-5"
	gatewayJudge     = "anthropic/claude-opus-5"
)

func anthropicProvider() Provider {
	return Provider{Name: anthropicName, Adapter: "anthropic", CredentialEnv: "ANTHROPIC_API_KEY"}
}

func subscriptionProvider() Provider {
	return Provider{Name: subscriptionName, Adapter: "subscription", CredentialEnv: "CLAUDE_CODE_OAUTH_TOKEN"}
}

func gatewayProvider(name, baseURL, env string) Provider {
	return Provider{Name: name, Adapter: "gateway", BaseURL: baseURL, AuthStyle: "bearer", CredentialEnv: env}
}

func gatewayName(baseURL string) (name, env string) {
	switch {
	case strings.Contains(baseURL, "openrouter.ai"):
		return "OpenRouter", "OPENROUTER_API_KEY"
	default:
		return "Gateway", "GATEWAY_API_KEY"
	}
}

func members(id string, models ...string) []Slot {
	out := make([]Slot, len(models))
	for i, m := range models {
		out[i] = Slot{ProviderID: id, Model: m}
	}
	return out
}

// Presets in the order the setup shows them.
var Presets = []Preset{
	{
		ID: "anthropic", Title: "Anthropic API key",
		Description: "The judge and the panel run on Anthropic's API, billed per token.",
		WorkerEnv:   []string{"ANTHROPIC_API_KEY"},
		build: func(string) ([]Provider, func(map[string]string) Panel) {
			return []Provider{anthropicProvider()}, func(ids map[string]string) Panel {
				return Panel{Judge: Slot{ids[anthropicName], judgeModel}, Members: members(ids[anthropicName], "sonnet", "haiku")}
			}
		},
	},
	{
		ID: "openrouter", Title: "OpenRouter",
		Description:  "The judge and the panel run through OpenRouter with one key.",
		NeedsBaseURL: false, DefaultBaseURL: openRouterURL,
		WorkerEnv: []string{"OPENROUTER_API_KEY"},
		build: func(string) ([]Provider, func(map[string]string) Panel) {
			return []Provider{gatewayProvider("OpenRouter", openRouterURL, "OPENROUTER_API_KEY")}, func(ids map[string]string) Panel {
				return Panel{Judge: Slot{ids["OpenRouter"], gatewayJudge},
					Members: members(ids["OpenRouter"], "anthropic/claude-sonnet-5", "anthropic/claude-haiku-4.5")}
			}
		},
	},
	{
		ID: "litellm", Title: "LiteLLM or another gateway",
		Description:  "The judge and the panel run through your own Anthropic-compatible gateway. Check the model names it serves after setup.",
		NeedsBaseURL: true, DefaultBaseURL: "http://litellm:4000",
		WorkerEnv: []string{"LITELLM_API_KEY"},
		build: func(baseURL string) ([]Provider, func(map[string]string) Panel) {
			return []Provider{gatewayProvider("LiteLLM", baseURL, "LITELLM_API_KEY")}, func(ids map[string]string) Panel {
				return Panel{Judge: Slot{ids["LiteLLM"], judgeModel}, Members: members(ids["LiteLLM"], "claude-sonnet-5", "claude-haiku-4-5")}
			}
		},
	},
	{
		ID: "anthropic-subscription", Title: "Anthropic judge, Claude subscription panel",
		Description:  "The judge runs on Anthropic's API. The panel runs Claude Code on a subscription token from claude setup-token.",
		WorkerEnv:    []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"},
		Subscription: true,
		build: func(string) ([]Provider, func(map[string]string) Panel) {
			return []Provider{anthropicProvider(), subscriptionProvider()}, func(ids map[string]string) Panel {
				return Panel{Judge: Slot{ids[anthropicName], judgeModel}, Members: members(ids[subscriptionName], "sonnet", "haiku")}
			}
		},
	},
	{
		ID: "gateway-subscription", Title: "Gateway judge, Claude subscription panel",
		Description:  "The judge runs through a gateway such as OpenRouter. The panel runs Claude Code on a subscription token.",
		NeedsBaseURL: true, DefaultBaseURL: openRouterURL,
		WorkerEnv:    []string{"OPENROUTER_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"},
		Subscription: true,
		build: func(baseURL string) ([]Provider, func(map[string]string) Panel) {
			name, env := gatewayName(baseURL)
			model := judgeModel
			if name == "OpenRouter" {
				model = gatewayJudge
			}
			return []Provider{gatewayProvider(name, baseURL, env), subscriptionProvider()}, func(ids map[string]string) Panel {
				return Panel{Judge: Slot{ids[name], model}, Members: members(ids[subscriptionName], "sonnet", "haiku")}
			}
		},
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

// ApplyPreset creates the preset's providers, or updates ones with the same
// name, and sets its judge and panel.
func (s *Store) ApplyPreset(ctx context.Context, id, baseURL, actor string) error {
	p := presetByID(id)
	if p == nil {
		return invalid("unknown preset %q", id)
	}
	if baseURL == "" {
		baseURL = p.DefaultBaseURL
	}
	provs, panel := p.build(strings.TrimSuffix(strings.TrimSpace(baseURL), "/"))
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM eval_panel WHERE id = 1`); err != nil {
			return err
		}
		ids := map[string]string{}
		for i := range provs {
			if err := upsertProvider(ctx, tx, &provs[i], actor); err != nil {
				return err
			}
			ids[provs[i].Name] = provs[i].ID
		}
		return setPanel(ctx, tx, panel(ids), actor)
	})
}
