package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/auth-all/events"
	"github.com/alternayte/auth-all/plugins/admin"
	"github.com/alternayte/auth-all/store"
)

// sessionUser returns the signed-in person. A key never manages keys.
func sessionUser(ctx context.Context) (*User, error) {
	user := UserFromContext(ctx)
	if user == nil || KeyFromContext(ctx) != nil {
		return nil, huma.Error401Unauthorized("not authenticated")
	}
	return user, nil
}

// owner returns the signed-in owner and a context that names them as the actor.
func owner(ctx context.Context) (context.Context, *User, error) {
	user := UserFromContext(ctx)
	if user == nil {
		return ctx, nil, huma.Error401Unauthorized("not authenticated")
	}
	if !user.IsOwner() {
		return ctx, nil, huma.Error403Forbidden("owner role required")
	}
	actor, _ := events.ActorFrom(ctx)
	actor.ID = user.ID
	return events.WithActor(ctx, actor), user, nil
}

// RegisterRoutes wires up the routes Skael keeps: account state, API keys,
// and owner administration. auth-all serves the rest of a person's account.
func RegisterRoutes(api huma.API, people *People, userStore *UserStore, keyStore *KeyStore) {
	type accountBody struct {
		MustChangePassword bool     `json:"must_change_password"`
		LinkedProviders    []string `json:"linked_providers"`
		TOTPEnabled        bool     `json:"totp_enabled"`
	}
	type accountOutput struct{ Body accountBody }
	huma.Register(api, huma.Operation{
		OperationID: "auth-account",
		Method:      http.MethodGet,
		Path:        "/api/auth/account",
		Summary:     "Get the signed-in person's account state",
	}, func(ctx context.Context, input *struct{}) (*accountOutput, error) {
		user, err := sessionUser(ctx)
		if err != nil {
			return nil, err
		}
		row, err := userStore.GetByID(ctx, user.ID)
		if err != nil || row == nil {
			return nil, huma.Error401Unauthorized("not authenticated")
		}
		accounts, err := people.Auth.Accounts(ctx, user.ID)
		if err != nil {
			return nil, fmt.Errorf("account: linked providers: %w", err)
		}
		linked := make([]string, 0, len(accounts))
		for _, a := range accounts {
			linked = append(linked, a.Provider)
		}
		totp, err := people.Auth.Store().TOTP().Get(ctx, user.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("account: totp: %w", err)
		}
		return &accountOutput{Body: accountBody{
			MustChangePassword: row.MustChangePassword,
			LinkedProviders:    linked,
			TOTPEnabled:        err == nil && totp.ConfirmedAt != nil,
		}}, nil
	})

	// -----------------------------------------------------------------
	// POST /api/auth/keys — create API key
	// -----------------------------------------------------------------
	type createKeyBody struct {
		Name   string   `json:"name" minLength:"1" maxLength:"64"`
		Scopes []string `json:"scopes,omitempty"`
		SetID  string   `json:"set_id,omitempty"`
	}
	type createKeyInput struct {
		Body createKeyBody
	}
	type createKeyResponse struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		Key       string    `json:"key"`
		Prefix    string    `json:"prefix"`
		CreatedAt time.Time `json:"created_at"`
		Scopes    []string  `json:"scopes"`
	}
	type createKeyOutput struct {
		Body createKeyResponse
	}
	huma.Register(api, huma.Operation{
		OperationID:   "create-api-key",
		Method:        http.MethodPost,
		Path:          "/api/auth/keys",
		Summary:       "Create a new API key",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *createKeyInput) (*createKeyOutput, error) {
		user, err := sessionUser(ctx)
		if err != nil {
			return nil, err
		}
		userID := user.ID

		fullKey, prefix, err := GenerateAPIKey()
		if err != nil {
			return nil, fmt.Errorf("create key: generate: %w", err)
		}

		keyHash, err := HashAPIKey(fullKey)
		if err != nil {
			return nil, fmt.Errorf("create key: hash: %w", err)
		}

		setID, err := validateKeyLimits(ctx, keyStore, input.Body.Scopes, input.Body.SetID)
		if err != nil {
			return nil, err
		}
		row, err := keyStore.CreateLimited(ctx, userID, input.Body.Name, prefix, keyHash, input.Body.Scopes, setID)
		if err != nil {
			return nil, fmt.Errorf("create key: store: %w", err)
		}

		return &createKeyOutput{Body: createKeyResponse{
			ID:        row.ID,
			Name:      row.Name,
			Key:       fullKey,
			Prefix:    prefix,
			CreatedAt: row.CreatedAt,
			Scopes:    row.Scopes,
		}}, nil
	})

	// -----------------------------------------------------------------
	// GET /api/auth/keys — list API keys
	// -----------------------------------------------------------------
	type listKeysBody struct {
		Keys []APIKeyInfo `json:"keys"`
	}
	type listKeysOutput struct {
		Body listKeysBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-api-keys",
		Method:      http.MethodGet,
		Path:        "/api/auth/keys",
		Summary:     "List API keys for the current user",
	}, func(ctx context.Context, input *struct{}) (*listKeysOutput, error) {
		user, err := sessionUser(ctx)
		if err != nil {
			return nil, err
		}
		userID := user.ID

		keys, err := keyStore.ListByUser(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("list keys: %w", err)
		}

		return &listKeysOutput{Body: listKeysBody{Keys: keys}}, nil
	})

	// -----------------------------------------------------------------
	// DELETE /api/auth/keys/{id} — delete API key
	// -----------------------------------------------------------------
	type deleteKeyInput struct {
		ID string `path:"id"`
	}
	huma.Register(api, huma.Operation{
		OperationID:   "delete-api-key",
		Method:        http.MethodDelete,
		Path:          "/api/auth/keys/{id}",
		Summary:       "Delete an API key",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *deleteKeyInput) (*struct{}, error) {
		user, err := sessionUser(ctx)
		if err != nil {
			return nil, err
		}
		userID := user.ID

		if err := keyStore.Delete(ctx, input.ID, userID); err != nil {
			return nil, huma.Error404NotFound("key not found")
		}

		return nil, nil
	})

	// -----------------------------------------------------------------
	// GET /api/admin/users — owner lists all users
	// -----------------------------------------------------------------
	type adminUserInfo struct {
		ID        string    `json:"id"`
		Email     string    `json:"email"`
		Name      string    `json:"name"`
		Role      string    `json:"role"`
		CreatedAt time.Time `json:"created_at"`
		Disabled  bool      `json:"disabled"`
	}
	type adminUsersBody struct {
		Users []adminUserInfo `json:"users"`
	}
	type adminUsersOutput struct {
		Body adminUsersBody
	}
	huma.Register(api, huma.Operation{
		OperationID: "admin-list-users",
		Method:      http.MethodGet,
		Path:        "/api/admin/users",
		Summary:     "List all users (owner only)",
	}, func(ctx context.Context, input *struct{}) (*adminUsersOutput, error) {
		if _, _, err := owner(ctx); err != nil {
			return nil, err
		}

		rows, err := userStore.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("admin list users: %w", err)
		}

		users := make([]adminUserInfo, len(rows))
		for i, r := range rows {
			users[i] = adminUserInfo{
				ID:        r.ID,
				Email:     r.Email,
				Name:      r.Name,
				Role:      r.Role,
				CreatedAt: r.CreatedAt,
				Disabled:  r.DisabledAt != nil,
			}
		}

		return &adminUsersOutput{Body: adminUsersBody{Users: users}}, nil
	})

	// -----------------------------------------------------------------
	// PUT /api/admin/users/{id}/role — owner changes another user's role
	// -----------------------------------------------------------------
	type setRoleBody struct {
		Role string `json:"role" doc:"New role: admin or member"`
	}
	type setRoleInput struct {
		ID   string `path:"id"`
		Body setRoleBody
	}
	type setRoleResponse struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	type setRoleOutput struct {
		Body setRoleResponse
	}
	huma.Register(api, huma.Operation{
		OperationID: "admin-set-user-role",
		Method:      http.MethodPut,
		Path:        "/api/admin/users/{id}/role",
		Summary:     "Set a user's role (owner only)",
	}, func(ctx context.Context, input *setRoleInput) (*setRoleOutput, error) {
		ctx, user, err := owner(ctx)
		if err != nil {
			return nil, err
		}

		// Nobody can be promoted to owner: an instance has exactly one owner,
		// so it can never end up with none and lock everyone out.
		if input.Body.Role != RoleAdmin && input.Body.Role != RoleMember {
			return nil, huma.Error422UnprocessableEntity(
				fmt.Sprintf("role must be %q or %q", RoleAdmin, RoleMember))
		}

		// The owner cannot demote themselves, for the same reason.
		if input.ID == user.ID {
			return nil, huma.Error403Forbidden("the owner cannot change their own role")
		}

		// A malformed id can never match a row; treat it as not found rather
		// than letting the driver reject the query.
		if err := uuid.Validate(input.ID); err != nil {
			return nil, huma.Error404NotFound("user not found")
		}

		target, err := userStore.GetByID(ctx, input.ID)
		if err != nil {
			return nil, fmt.Errorf("set role: lookup: %w", err)
		}
		if target == nil || target.Kind == KindService {
			return nil, huma.Error404NotFound("user not found")
		}
		// Defence in depth: the id check above already covers the caller, but
		// an owner is never demotable by this route whichever id is used.
		if target.Role == RoleOwner {
			return nil, huma.Error403Forbidden("the owner's role cannot be changed")
		}

		if _, err := people.Admin.SetRole(ctx, target.ID, input.Body.Role); err != nil {
			return nil, fmt.Errorf("set role: update: %w", err)
		}

		return &setRoleOutput{Body: setRoleResponse{
			ID:    target.ID,
			Email: target.Email,
			Name:  target.Name,
			Role:  input.Body.Role,
		}}, nil
	})

	// Owner-only actions on a person. Service accounts have no password and are
	// not people, and the owner cannot lock the instance's only owner out.
	managedUser := func(ctx context.Context, id string) (context.Context, *UserRow, error) {
		ctx, _, err := owner(ctx)
		if err != nil {
			return ctx, nil, err
		}
		if uuid.Validate(id) != nil {
			return ctx, nil, huma.Error404NotFound("user not found")
		}
		target, err := userStore.GetByID(ctx, id)
		if err != nil {
			return ctx, nil, fmt.Errorf("admin: lookup: %w", err)
		}
		if target == nil || target.Kind == KindService {
			return ctx, nil, huma.Error404NotFound("user not found")
		}
		return ctx, target, nil
	}

	type adminUserPath struct {
		ID string `path:"id"`
	}
	type adminPasswordOutput struct {
		Body struct {
			TemporaryPassword string `json:"temporary_password"`
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "admin-issue-temporary-password",
		Method:      http.MethodPost,
		Path:        "/api/admin/users/{id}/password",
		Summary:     "Issue a temporary password (owner only)",
	}, func(ctx context.Context, input *adminUserPath) (*adminPasswordOutput, error) {
		ctx, target, err := managedUser(ctx, input.ID)
		if err != nil {
			return nil, err
		}
		password, err := people.Admin.ResetPassword(ctx, target.ID, admin.ResetOptions{Temporary: true})
		if err != nil {
			return nil, fmt.Errorf("admin: reset password: %w", err)
		}
		out := &adminPasswordOutput{}
		out.Body.TemporaryPassword = password
		return out, nil
	})

	setDisabled := func(disable bool) func(context.Context, *adminUserPath) (*struct{}, error) {
		return func(ctx context.Context, input *adminUserPath) (*struct{}, error) {
			ctx, target, err := managedUser(ctx, input.ID)
			if err != nil {
				return nil, err
			}
			if target.Role == RoleOwner {
				return nil, huma.Error403Forbidden("the owner cannot be disabled")
			}
			if disable {
				_, err = people.Admin.Disable(ctx, target.ID)
			} else {
				_, err = people.Admin.Enable(ctx, target.ID)
			}
			if err != nil {
				return nil, fmt.Errorf("admin: set disabled: %w", err)
			}
			return nil, nil
		}
	}
	huma.Register(api, huma.Operation{
		OperationID: "admin-disable-user", Method: http.MethodPost, Path: "/api/admin/users/{id}/disable",
		Summary: "Disable a user and end their sessions (owner only)", DefaultStatus: http.StatusNoContent,
	}, setDisabled(true))
	huma.Register(api, huma.Operation{
		OperationID: "admin-enable-user", Method: http.MethodPost, Path: "/api/admin/users/{id}/enable",
		Summary: "Enable a disabled user (owner only)", DefaultStatus: http.StatusNoContent,
	}, setDisabled(false))

	// -----------------------------------------------------------------
	// GET /api/users/search — typeahead for assigning skill owners.
	//
	// Open to any authenticated user, and returning identity only: a
	// namespace owner adding a teammate is a plain member, so restricting
	// this to admins would make delegated ownership unusable. The trade is
	// deliberate and bounded — no role, no timestamps, a minimum query
	// length and a hard cap, so it is a lookup and not an export.
	// -----------------------------------------------------------------
	type publicUser struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	type userSearchInput struct {
		Q string `query:"q"`
	}
	type userSearchBody struct {
		Users []publicUser `json:"users"`
	}
	type userSearchOutput struct{ Body userSearchBody }

	huma.Register(api, huma.Operation{
		OperationID: "search-users",
		Method:      http.MethodGet,
		Path:        "/api/users/search",
		Summary:     "Find users by name or email, for assigning skill owners",
	}, func(ctx context.Context, input *userSearchInput) (*userSearchOutput, error) {
		if UserFromContext(ctx) == nil {
			return nil, huma.Error401Unauthorized("not authenticated")
		}
		rows, err := userStore.Search(ctx, input.Q, 20)
		if err != nil {
			return nil, fmt.Errorf("search users: %w", err)
		}
		users := make([]publicUser, len(rows))
		for i, r := range rows {
			users[i] = publicUser{ID: r.ID, Name: r.Name, Email: r.Email}
		}
		return &userSearchOutput{Body: userSearchBody{Users: users}}, nil
	})
}
