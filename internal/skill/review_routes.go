package skill

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
)

// reviewBody is the request body for the human review endpoint. Both fields
// are required, so neither carries ",omitempty" — an omitted action or reason
// must fail validation, not silently default.
type reviewBody struct {
	Action string `json:"action" doc:"approve or reject"`
	Reason string `json:"reason" doc:"Written justification, recorded on the version"`
}

// reviewInput is the request shape for POST
// /api/skills/{name}/versions/{version}/review.
type reviewInput struct {
	Name    string `path:"name"`
	Version int    `path:"version"`
	Body    reviewBody
}

// reviewOutput returns the version as it stands after the review decision.
type reviewOutput struct {
	Body *Version
}

// registerReviewRoutes wires up the human review endpoint: an owner or admin
// approving or rejecting a version a scan or a failed/absent evaluation left
// in needs_review. It is the manual counterpart to the automatic release a
// verified evaluation performs, needed when the eval is wrong, no worker is
// running, or a human has simply read the thing.
func registerReviewRoutes(api huma.API, store *Store) {
	huma.Register(api, huma.Operation{
		OperationID:   "review-skill-version",
		Method:        http.MethodPost,
		Path:          "/api/skills/{name}/versions/{version}/review",
		Summary:       "Approve or reject a version held for review",
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, input *reviewInput) (*reviewOutput, error) {
		// 1. Only an owner or admin may act. This is the same privilege
		// helper publishOverrideAllowed resolves against — one definition of
		// who may override the gate — but the raw check, not the
		// requested-explicitly wrapper: a review request always requests
		// the action by definition.
		user := auth.UserFromContext(ctx)
		if !user.IsPrivileged() {
			return nil, huma.Error403Forbidden("only an owner or admin may review a held version")
		}

		// 2. Validate the action by hand, matching the precedent set for
		// event_source, so an unknown action gets a clear message rather than
		// a generic enum-tag rejection.
		action := input.Body.Action
		if action != "approve" && action != "reject" {
			return nil, huma.Error422UnprocessableEntity(
				fmt.Sprintf("action must be %q or %q, got %q", "approve", "reject", action))
		}

		reason := strings.TrimSpace(input.Body.Reason)
		if reason == "" {
			return nil, huma.Error422UnprocessableEntity(
				"reason is required: an override with no written justification is the one that gets forgotten")
		}

		// 3. Look up the version directly rather than through HeldVersion, so
		// "does not exist" (404) and "exists but is not held" (409) are
		// distinguishable to the caller.
		ver, err := store.GetVersion(ctx, input.Name, input.Version)
		if err != nil {
			return nil, fmt.Errorf("review: lookup version: %w", err)
		}
		if ver == nil {
			return nil, huma.Error404NotFound(
				fmt.Sprintf("%s v%d not found", input.Name, input.Version))
		}
		if ver.GateState != "needs_review" {
			return nil, huma.Error409Conflict(
				fmt.Sprintf("%s v%d is %s, not awaiting review", input.Name, input.Version, ver.GateState))
		}

		// 4. Apply the decision.
		switch action {
		case "approve":
			if err := store.ReleaseVersion(ctx, store.Pool(), input.Name, input.Version, user.Email, reason); err != nil {
				return nil, fmt.Errorf("review: release: %w", err)
			}
		case "reject":
			if err := store.RejectVersion(ctx, input.Name, input.Version, user.Email, reason); err != nil {
				return nil, fmt.Errorf("review: reject: %w", err)
			}
		}

		// 5. Log at warn — the same shape as the publish override's line,
		// because it is the same kind of event: a privileged human
		// overriding what the automated gate decided.
		log.Warn().
			Str("skill", input.Name).
			Int("version", input.Version).
			Str("user", user.Email).
			Str("role", user.Role).
			Str("action", action).
			Str("reason", reason).
			Msg("manual review: privileged user decided a version held for review")

		updated, err := store.GetVersion(ctx, input.Name, input.Version)
		if err != nil {
			return nil, fmt.Errorf("review: reload version: %w", err)
		}
		if updated == nil {
			return nil, errors.New("review: version disappeared after decision")
		}
		return &reviewOutput{Body: updated}, nil
	})
}
