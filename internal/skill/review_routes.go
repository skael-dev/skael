package skill

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/gate"
)

// reviewBody is the request body for the human review endpoint. Action and
// Reason are required, so neither carries ",omitempty" — an omitted action or
// reason must fail validation, not silently default.
type reviewBody struct {
	Action string `json:"action" doc:"approve or reject"`
	Reason string `json:"reason" doc:"Written justification, recorded on the version"`

	// HoldReason names which reason this decision clears. It carries
	// ",omitempty" because it is optional: with exactly one reason
	// outstanding it is inferred, which is what keeps every already-deployed
	// `skael review --approve` working. Validated by hand rather than with an
	// enum tag so an omitted value can default.
	HoldReason string `json:"hold_reason,omitempty"`
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

// registerReviewRoutes wires up the human review endpoint: an owner, admin,
// or (for the ownership reason only) a namespace owner approving or
// rejecting a version a scan, ownership check, or a failed/absent evaluation
// left in needs_review. It is the manual counterpart to the automatic release
// a verified evaluation performs, needed when the eval is wrong, no worker is
// running, or a human has simply read the thing.
func registerReviewRoutes(api huma.API, store *Store, owners OwnerResolver) {
	huma.Register(api, huma.Operation{
		OperationID:   "review-skill-version",
		Method:        http.MethodPost,
		Path:          "/api/skills/{name}/versions/{version}/review",
		Summary:       "Approve or reject a version held for review",
		DefaultStatus: http.StatusOK,
	}, func(ctx context.Context, input *reviewInput) (*reviewOutput, error) {
		user := auth.UserFromContext(ctx)

		// 1. Validate the action by hand, matching the precedent set for
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

		// 2. Look up the version directly rather than through HeldVersion, so
		// "does not exist" (404) and "exists but is not held" (409) are
		// distinguishable to the caller. This also guards every call below
		// that assumes the skill/version exists: ApproveReason/RejectReason
		// have no existence check of their own (OutstandingReasons vacuously
		// returns zero rows for a name/version that isn't there), so this
		// lookup must run before any of them do.
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

		// 3. Which reason is this decision about?
		outstanding, err := store.OutstandingReasons(ctx, input.Name, input.Version)
		if err != nil {
			return nil, fmt.Errorf("review: outstanding reasons: %w", err)
		}
		if len(outstanding) == 0 {
			return nil, huma.Error409Conflict(
				fmt.Sprintf("%s v%d has nothing outstanding to review", input.Name, input.Version))
		}

		reasonKind := input.Body.HoldReason
		if reasonKind == "" {
			if len(outstanding) > 1 {
				return nil, huma.Error422UnprocessableEntity(fmt.Sprintf(
					"hold_reason is required: %s v%d is held for %s",
					input.Name, input.Version, strings.Join(outstanding, " and ")))
			}
			reasonKind = outstanding[0]
		}
		if !slices.Contains(outstanding, reasonKind) {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf(
				"%s v%d is not held for %q; outstanding: %s",
				input.Name, input.Version, reasonKind, strings.Join(outstanding, ", ")))
		}

		// 4. Authorize per reason. An instance admin may clear either. A
		// skill's owner may clear only the ownership reason: a scan finding
		// is an instance-level decision, and letting a self-managed
		// namespace owner wave one through makes the security gate as weak
		// as the least careful namespace.
		switch reasonKind {
		case gate.ReasonScan:
			if !user.IsPrivileged() {
				return nil, huma.Error403Forbidden(
					"only an owner or admin may clear a security finding")
			}
		case gate.ReasonOwnership:
			allowed := user.IsPrivileged()
			if !allowed && owners != nil {
				st, err := owners.ResolveForPublish(ctx, input.Name, user)
				if err != nil {
					return nil, fmt.Errorf("review: resolve ownership: %w", err)
				}
				allowed = st.IsOwner
			}
			if !allowed {
				return nil, huma.Error403Forbidden(
					"only an owner of this skill, or an instance admin, may approve this change")
			}
		default:
			return nil, huma.Error422UnprocessableEntity(
				fmt.Sprintf("unrecognised hold reason %q", reasonKind))
		}

		// 5. Apply the decision.
		switch action {
		case "approve":
			var actorID *string
			if user.ID != "" {
				id := user.ID
				actorID = &id
			}
			if _, err := store.ApproveReason(ctx, store.Pool(), input.Name, input.Version,
				reasonKind, actorID, user.Email, reason); err != nil {
				return nil, fmt.Errorf("review: approve: %w", err)
			}
		case "reject":
			if err := store.RejectReason(ctx, input.Name, input.Version,
				reasonKind, user.Email, reason); err != nil {
				return nil, fmt.Errorf("review: reject: %w", err)
			}
		}

		// 6. Log at warn — the same shape as the publish override's line,
		// because it is the same kind of event: a human overriding what the
		// automated gate decided.
		log.Warn().
			Str("skill", input.Name).
			Int("version", input.Version).
			Str("user", user.Email).
			Str("role", user.Role).
			Str("action", action).
			Str("reason", reason).
			Str("hold_reason", reasonKind).
			Msg("manual review: user decided a version held for review")

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
