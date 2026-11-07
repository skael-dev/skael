package settings

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/skael-dev/skael/internal/auth"
)

// Wire types carry the "Settings" prefix: Huma names schemas globally.

type SettingsField struct {
	Key         string           `json:"key"`
	Env         string           `json:"env"`
	Kind        Kind             `json:"kind"`
	Label       string           `json:"label"`
	Help        string           `json:"help"`
	Default     string           `json:"default"`
	Options     []SettingsOption `json:"options"`
	Suggestions []SettingsOption `json:"suggestions"`
	Min         *float64         `json:"min,omitempty"`
	Max         *float64         `json:"max,omitempty"`
	EnvOnly     bool             `json:"env_only"`
	Scope       Scope            `json:"scope"`
	Source      Source           `json:"source"`
	LockedBy    string           `json:"locked_by,omitempty"`
	Value       string           `json:"value"`
	IsSet       bool             `json:"is_set"`
	Reference   string           `json:"reference,omitempty"`
	Problem     string           `json:"problem,omitempty"`
	UpdatedBy   string           `json:"updated_by,omitempty"`
	UpdatedAt   time.Time        `json:"updated_at,omitzero"`
}

type SettingsSection struct {
	ID     string          `json:"id"`
	Title  string          `json:"title"`
	Binary Binary          `json:"binary"`
	Test   string          `json:"test,omitempty"`
	Guide  string          `json:"guide"`
	Fields []SettingsField `json:"fields"`
}

type SettingsBody struct {
	Version  int64             `json:"version"`
	Sections []SettingsSection `json:"sections"`
}

type settingsOutput struct {
	Body SettingsBody
}

type SettingsUpdateBody struct {
	Changes []SettingsChange `json:"changes" minItems:"1" maxItems:"100"`
}

type settingsUpdateInput struct {
	Body SettingsUpdateBody
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

// Field is the wire form of one resolved setting. A secret never leaves the
// server: only whether it is set and its reference do.
func Field(r *Resolved) SettingsField {
	d := r.Def
	f := SettingsField{
		Key: d.Key, Env: d.Env, Kind: d.Kind, Label: d.Label, Help: d.Help, Default: d.Default,
		Options: nonNil(d.Options), Suggestions: nonNil(d.Suggestions), Min: d.Min, Max: d.Max,
		EnvOnly: d.EnvOnly, Scope: d.Scope, Source: r.Source, Reference: r.Reference, Problem: r.Problem,
		UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt,
	}
	if r.Source == SourceEnv {
		f.LockedBy = d.Env
	}
	if d.Kind == KindSecret {
		f.IsSet = r.Value != ""
	} else {
		f.Value = r.Value
		f.IsSet = r.Source != SourceDefault
	}
	return f
}

func nonNil(o []SettingsOption) []SettingsOption {
	if o == nil {
		return []SettingsOption{}
	}
	return o
}

// Body is the whole settings page for snap.
func Body(snap *Snapshot) SettingsBody {
	out := SettingsBody{Version: snap.Version, Sections: []SettingsSection{}}
	for _, s := range Sections {
		sec := SettingsSection{ID: s.ID, Title: s.Title, Binary: s.Binary, Test: s.Test, Guide: Guide(s.ID), Fields: []SettingsField{}}
		for i := range Defs {
			if Defs[i].Section == s.ID {
				sec.Fields = append(sec.Fields, Field(snap.Get(Defs[i].Key)))
			}
		}
		out.Sections = append(out.Sections, sec)
	}
	return out
}

// RegisterRoutes serves the settings page to owners and admins.
func RegisterRoutes(api huma.API, live *Live) {
	huma.Register(api, huma.Operation{
		OperationID: "get-settings",
		Method:      http.MethodGet,
		Path:        "/api/admin/settings",
		Summary:     "List every setting with its value, source and help",
	}, func(ctx context.Context, _ *struct{}) (*settingsOutput, error) {
		if _, err := requirePrivileged(ctx); err != nil {
			return nil, err
		}
		return &settingsOutput{Body: Body(live.Get())}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-settings",
		Method:      http.MethodPut,
		Path:        "/api/admin/settings",
		Summary:     "SettingsChange settings; every replica applies them without a restart",
	}, func(ctx context.Context, in *settingsUpdateInput) (*settingsOutput, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		if live.Store() == nil {
			return nil, huma.Error503ServiceUnavailable("settings are not stored on this server")
		}
		if err := live.Store().Apply(ctx, u.Email, in.Body.Changes, live.Env()); err != nil {
			var inv *ErrInvalid
			switch {
			case errors.As(err, &inv):
				return nil, huma.Error422UnprocessableEntity(inv.Msg)
			case errors.Is(err, ErrNoKeys):
				return nil, huma.Error409Conflict(err.Error())
			}
			return nil, huma.Error500InternalServerError("save settings: internal error", err)
		}
		if err := live.Load(ctx); err != nil {
			return nil, huma.Error500InternalServerError("reload settings: internal error", err)
		}
		return &settingsOutput{Body: Body(live.Get())}, nil
	})
}
