package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/skael-dev/skael/internal/auth"
	"github.com/skael-dev/skael/internal/ownership"
	"github.com/skael-dev/skael/internal/settings"
)

// ownerMailer emails the owners of a skill through the instance's mail server.
// An unowned skill's owners are the instance owners and admins, who own
// everything. With no mail server it sends nothing.
func ownerMailer(snap func() *settings.Snapshot, owners *ownership.Resolver, users *auth.UserStore) func(ctx context.Context, skillName, subject, text string) error {
	return func(ctx context.Context, skillName, subject, text string) error {
		s := snap()
		host := s.String("smtp.host")
		if host == "" || users == nil {
			return nil
		}
		st, err := owners.ResolveForPublish(ctx, skillName, nil)
		if err != nil {
			return fmt.Errorf("owner mail: resolve owners: %w", err)
		}
		var to []string
		for _, o := range st.Owners {
			to = append(to, o.Email)
		}
		if len(to) == 0 {
			all, err := users.List(ctx)
			if err != nil {
				return fmt.Errorf("owner mail: list users: %w", err)
			}
			for _, u := range all {
				if u.DisabledAt == nil && (u.Role == auth.RoleOwner || u.Role == auth.RoleAdmin) {
					to = append(to, u.Email)
				}
			}
		}
		var errs []error
		for _, addr := range to {
			if err := auth.SendSMTPText(ctx, host, s.Int("smtp.port"), s.String("smtp.username"),
				s.String("smtp.password"), s.String("smtp.from"), addr, subject, text); err != nil {
				errs = append(errs, fmt.Errorf("owner mail to %s: %w", addr, err))
			}
		}
		return errors.Join(errs...)
	}
}
