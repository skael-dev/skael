package onboarding

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/auth"
)

// Step is one checklist or setup item.
type Step struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
}

// OnboardingStatus is the checklist toward the first activation and the setup
// card, which blocks nothing.
type OnboardingStatus struct {
	Steps        []Step `json:"steps"`
	Setup        []Step `json:"setup"`
	SampleLoaded bool   `json:"sample_loaded"`
	// PublicURL is the saved public URL, empty when none is set.
	PublicURL string `json:"public_url"`
}

// SetupFacts are the settings the setup card reads.
type SetupFacts struct {
	PublicURL, SMTPHost string
	SignIn              bool
}

// Status reads the checklist. Sample rows never complete a step.
func Status(ctx context.Context, pool *pgxpool.Pool, facts SetupFacts) (*OnboardingStatus, error) {
	var published, synced, activated, provider, worker, sample bool
	var people int
	err := pool.QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM skills WHERE NOT sample AND latest_version > 0),
		EXISTS (SELECT 1 FROM onboarding WHERE synced_at IS NOT NULL),
		EXISTS (SELECT 1 FROM skill_events WHERE NOT sample AND event_source <> 'control_plane_report'),
		EXISTS (SELECT 1 FROM eval_panel),
		EXISTS (SELECT 1 FROM workers WHERE last_seen > now() - interval '1 day'),
		EXISTS (SELECT 1 FROM skills WHERE sample),
		(`+humansSQL+`)`).Scan(&published, &synced, &activated, &provider, &worker, &sample, &people)
	if err != nil {
		return nil, fmt.Errorf("onboarding: status: %w", err)
	}
	return &OnboardingStatus{
		Steps: []Step{
			{"publish", "Publish a skill", published},
			{"sync", "Sync it into an agent", synced},
			{"activate", "See the first activation", activated},
			{"invite", "Invite a teammate", people >= 2},
		},
		Setup: []Step{
			{"public_url", "Confirm the public URL", facts.PublicURL != ""},
			{"provider", "Choose an LLM provider", provider},
			{"worker", "Add a worker", worker},
			{"signin", "Add a sign-in provider", facts.SignIn},
			{"email", "Set up email", facts.SMTPHost != ""},
		},
		SampleLoaded: sample,
		PublicURL:    facts.PublicURL,
	}, nil
}

// MarkSynced records the first sync from a CLI.
func MarkSynced(ctx context.Context, pool *pgxpool.Pool) {
	_, _ = pool.Exec(ctx, `UPDATE onboarding SET synced_at = now() WHERE id = 1 AND synced_at IS NULL`)
}

type statusOutput struct {
	Body *OnboardingStatus
}

// RegisterRoutes serves the checklist to everyone signed in, and the sample
// actions to owners and admins.
func RegisterRoutes(api huma.API, pool *pgxpool.Pool, sample *Sample, facts func() SetupFacts) {
	huma.Register(api, huma.Operation{
		OperationID: "get-onboarding", Method: http.MethodGet, Path: "/api/onboarding",
		Summary: "The getting-started checklist and the setup card",
	}, func(ctx context.Context, _ *struct{}) (*statusOutput, error) {
		if auth.UserFromContext(ctx) == nil {
			return nil, huma.Error401Unauthorized("not authenticated")
		}
		st, err := Status(ctx, pool, facts())
		if err != nil {
			return nil, huma.Error500InternalServerError("onboarding: internal error", err)
		}
		return &statusOutput{Body: st}, nil
	})

	sampleAction := func(op, method, summary string, run func(context.Context, string) error) {
		huma.Register(api, huma.Operation{OperationID: op, Method: method, Path: "/api/onboarding/sample", Summary: summary},
			func(ctx context.Context, _ *struct{}) (*statusOutput, error) {
				u := auth.UserFromContext(ctx)
				if u == nil || !u.IsPrivileged() {
					return nil, huma.Error403Forbidden("owner or admin role required")
				}
				runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				defer cancel()
				if err := run(runCtx, u.Email); err != nil {
					return nil, huma.Error500InternalServerError("sample data: "+err.Error(), err)
				}
				st, err := Status(ctx, pool, facts())
				if err != nil {
					return nil, huma.Error500InternalServerError("onboarding: internal error", err)
				}
				return &statusOutput{Body: st}, nil
			})
	}
	sampleAction("load-sample-data", http.MethodPost, "Load the sample skills, scores, contest and activations", sample.Load)
	sampleAction("remove-sample-data", http.MethodDelete, "Remove every sample row", sample.Remove)
}

type SetupBody struct {
	ClaimRequired bool `json:"claim_required"`
}

type setupOutput struct {
	Body SetupBody
}

type SetupClaimBody struct {
	Code string `json:"code" minLength:"1" maxLength:"32"`
}

type setupClaimInput struct {
	Body SetupClaimBody
}

type setupClaimOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

// RegisterSetupRoutes serves a fresh instance before anyone signs in. enforced
// is false where no claim code is required, such as in tests.
func RegisterSetupRoutes(api huma.API, claim *Claim, enforced bool, cookieSecure bool) {
	huma.Register(api, huma.Operation{
		OperationID: "get-setup", Method: http.MethodGet, Path: "/api/setup",
		Summary: "Whether the first owner must enter the claim code",
	}, func(ctx context.Context, _ *struct{}) (*setupOutput, error) {
		if !enforced {
			return &setupOutput{Body: SetupBody{}}, nil
		}
		open, err := claim.Open(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("setup: internal error", err)
		}
		return &setupOutput{Body: SetupBody{ClaimRequired: open}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "claim-instance", Method: http.MethodPost, Path: "/api/setup/claim",
		Summary: "Check the claim code from the server log and allow the first sign-up", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *setupClaimInput) (*setupClaimOutput, error) {
		ok, err := claim.Verify(ctx, in.Body.Code)
		if err != nil {
			return nil, huma.Error500InternalServerError("setup: internal error", err)
		}
		if !ok {
			return nil, huma.Error403Forbidden("that is not the claim code; copy it from the server log")
		}
		return &setupClaimOutput{SetCookie: http.Cookie{
			Name: ClaimCookie, Value: in.Body.Code, Path: "/api", MaxAge: 3600,
			HttpOnly: true, Secure: cookieSecure, SameSite: http.SameSiteLaxMode,
		}}, nil
	})
}
