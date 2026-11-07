package providers

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/skael-dev/skael/internal/auth"
)

// Wire types carry the "Providers" prefix: Huma names schemas globally.

type ProvidersBody struct {
	Providers []Provider `json:"providers"`
	Panel     *Panel     `json:"panel"`
	Presets   []Preset   `json:"presets"`
}

type providersOutput struct {
	Body ProvidersBody
}

type ProvidersSaveBody struct {
	Name          string `json:"name" minLength:"1" maxLength:"64"`
	Adapter       string `json:"adapter" enum:"anthropic,gateway,subscription"`
	BaseURL       string `json:"base_url,omitempty"`
	AuthStyle     string `json:"auth_style,omitempty" enum:"x-api-key,bearer"`
	CredentialEnv string `json:"credential_env" minLength:"1"`
}

type providersCreateInput struct {
	Body ProvidersSaveBody
}

type providersUpdateInput struct {
	ID   string `path:"id"`
	Body ProvidersSaveBody
}

type providersDeleteInput struct {
	ID string `path:"id"`
}

type providersPanelInput struct {
	Body Panel
}

type ProvidersPresetBody struct {
	BaseURL string `json:"base_url,omitempty"`
}

type providersPresetInput struct {
	ID   string `path:"id"`
	Body ProvidersPresetBody
}

func requirePrivileged(ctx context.Context) (*auth.User, error) {
	u := auth.UserFromContext(ctx)
	if u == nil {
		return nil, huma.Error401Unauthorized("not authenticated")
	}
	if !u.IsPrivileged() {
		return nil, huma.Error403Forbidden("owner or admin role required")
	}
	return u, nil
}

func (s *Store) body(ctx context.Context) (*providersOutput, error) {
	list, err := s.List(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("providers: internal error", err)
	}
	panel, err := s.Panel(ctx)
	if err != nil {
		return nil, huma.Error500InternalServerError("providers: internal error", err)
	}
	if list == nil {
		list = []Provider{}
	}
	return &providersOutput{Body: ProvidersBody{Providers: list, Panel: panel, Presets: Presets}}, nil
}

func refusal(action string, err error) error {
	var inv *ErrInvalid
	if errors.As(err, &inv) {
		return huma.Error422UnprocessableEntity(inv.Msg)
	}
	return huma.Error500InternalServerError(action+": internal error", err)
}

// RegisterRoutes serves providers, the panel and the presets to owners and admins.
func RegisterRoutes(api huma.API, s *Store) {
	huma.Register(api, huma.Operation{
		OperationID: "list-providers", Method: http.MethodGet, Path: "/api/admin/providers",
		Summary: "List the LLM providers, the judge and panel, and the presets",
	}, func(ctx context.Context, _ *struct{}) (*providersOutput, error) {
		if _, err := requirePrivileged(ctx); err != nil {
			return nil, err
		}
		return s.body(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-provider", Method: http.MethodPost, Path: "/api/admin/providers",
		Summary: "Add an LLM provider",
	}, func(ctx context.Context, in *providersCreateInput) (*providersOutput, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		p := Provider{Name: in.Body.Name, Adapter: in.Body.Adapter, BaseURL: in.Body.BaseURL, AuthStyle: in.Body.AuthStyle, CredentialEnv: in.Body.CredentialEnv}
		if err := s.Save(ctx, &p, u.Email); err != nil {
			return nil, refusal("add provider", err)
		}
		return s.body(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-provider", Method: http.MethodPut, Path: "/api/admin/providers/{id}",
		Summary: "Change an LLM provider",
	}, func(ctx context.Context, in *providersUpdateInput) (*providersOutput, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		p := Provider{ID: in.ID, Name: in.Body.Name, Adapter: in.Body.Adapter, BaseURL: in.Body.BaseURL, AuthStyle: in.Body.AuthStyle, CredentialEnv: in.Body.CredentialEnv}
		if err := s.Save(ctx, &p, u.Email); err != nil {
			return nil, refusal("change provider", err)
		}
		return s.body(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-provider", Method: http.MethodDelete, Path: "/api/admin/providers/{id}",
		Summary: "Remove an LLM provider the panel does not use",
	}, func(ctx context.Context, in *providersDeleteInput) (*providersOutput, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.Delete(ctx, in.ID, u.Email); err != nil {
			return nil, refusal("remove provider", err)
		}
		return s.body(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-eval-panel", Method: http.MethodPut, Path: "/api/admin/providers/panel",
		Summary: "Set the judge and the panel every worker runs",
	}, func(ctx context.Context, in *providersPanelInput) (*providersOutput, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.SetPanel(ctx, in.Body, u.Email); err != nil {
			return nil, refusal("set the panel", err)
		}
		return s.body(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "apply-provider-preset", Method: http.MethodPost, Path: "/api/admin/providers/presets/{id}",
		Summary: "Set up the providers, judge and panel of a common combination",
	}, func(ctx context.Context, in *providersPresetInput) (*providersOutput, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.ApplyPreset(ctx, in.ID, in.Body.BaseURL, u.Email); err != nil {
			return nil, refusal("apply the preset", err)
		}
		return s.body(ctx)
	})
}
