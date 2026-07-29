package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/mail"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/skael-dev/skael/internal/platform"
)

// RegisterRoutes wires up all authentication-related HTTP endpoints onto the
// provided Huma API: signup, login, logout, me, and API key management.
func RegisterRoutes(api huma.API, sessionManager *scs.SessionManager, userStore *UserStore, keyStore *KeyStore, disableSignup bool) {
	// -----------------------------------------------------------------
	// POST /api/auth/signup
	// -----------------------------------------------------------------
	type signupBody struct {
		Email    string `json:"email" maxLength:"255"`
		Name     string `json:"name" minLength:"1" maxLength:"100"`
		Password string `json:"password" minLength:"8"`
	}
	type signupInput struct {
		Body signupBody
	}
	type signupOutput struct {
		Body User
	}
	huma.Register(api, huma.Operation{
		OperationID:   "auth-signup",
		Method:        http.MethodPost,
		Path:          "/api/auth/signup",
		Summary:       "Create a new user account",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, input *signupInput) (*signupOutput, error) {
		if disableSignup {
			return nil, huma.Error403Forbidden("signup is disabled")
		}

		// Validate email format.
		if _, err := mail.ParseAddress(input.Body.Email); err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid email format")
		}

		// Determine role: first user becomes owner.
		count, err := userStore.Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("signup: count users: %w", err)
		}

		hash, err := HashPassword(input.Body.Password)
		if err != nil {
			return nil, fmt.Errorf("signup: hash password: %w", err)
		}

		var row *UserRow
		if count == 0 {
			row, err = userStore.CreateWithRole(ctx, input.Body.Email, input.Body.Name, hash, RoleOwner)
		} else {
			row, err = userStore.Create(ctx, input.Body.Email, input.Body.Name, hash)
		}
		if err != nil {
			if platform.IsDuplicateKey(err) {
				return nil, huma.Error409Conflict("email already registered")
			}
			return nil, fmt.Errorf("signup: create user: %w", err)
		}

		sessionManager.Put(ctx, "user_id", row.ID)

		return &signupOutput{Body: User{
			ID:    row.ID,
			Email: row.Email,
			Name:  row.Name,
			Role:  row.Role,
		}}, nil
	})

	// -----------------------------------------------------------------
	// POST /api/auth/login
	// -----------------------------------------------------------------
	type loginBody struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	type loginInput struct {
		Body loginBody
	}
	type loginResponse struct {
		ID                    string `json:"id"`
		Email                 string `json:"email"`
		Name                  string `json:"name"`
		Role                  string `json:"role"`
		PasswordResetRequired bool   `json:"password_reset_required"`
	}
	type loginOutput struct {
		Body loginResponse
	}
	huma.Register(api, huma.Operation{
		OperationID: "auth-login",
		Method:      http.MethodPost,
		Path:        "/api/auth/login",
		Summary:     "Log in with email and password",
	}, func(ctx context.Context, input *loginInput) (*loginOutput, error) {
		row, err := userStore.GetByEmail(ctx, input.Body.Email)
		if err != nil {
			return nil, fmt.Errorf("login: lookup user: %w", err)
		}
		if row == nil || !CheckPassword(row.PasswordHash, input.Body.Password) {
			return nil, huma.Error401Unauthorized("invalid credentials")
		}

		sessionManager.Put(ctx, "user_id", row.ID)

		return &loginOutput{Body: loginResponse{
			ID:                    row.ID,
			Email:                 row.Email,
			Name:                  row.Name,
			Role:                  row.Role,
			PasswordResetRequired: row.PasswordResetRequired,
		}}, nil
	})

	// -----------------------------------------------------------------
	// POST /api/auth/logout
	// -----------------------------------------------------------------
	huma.Register(api, huma.Operation{
		OperationID:   "auth-logout",
		Method:        http.MethodPost,
		Path:          "/api/auth/logout",
		Summary:       "Log out and destroy the session",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *struct{}) (*struct{}, error) {
		if err := sessionManager.Destroy(ctx); err != nil {
			return nil, fmt.Errorf("logout: destroy session: %w", err)
		}
		return nil, nil
	})

	// -----------------------------------------------------------------
	// GET /api/auth/me
	// -----------------------------------------------------------------
	type meOutput struct {
		Body User
	}
	huma.Register(api, huma.Operation{
		OperationID: "auth-me",
		Method:      http.MethodGet,
		Path:        "/api/auth/me",
		Summary:     "Get the currently authenticated user",
	}, func(ctx context.Context, input *struct{}) (*meOutput, error) {
		userID := sessionManager.GetString(ctx, "user_id")
		if userID == "" {
			return nil, huma.Error401Unauthorized("not authenticated")
		}

		row, err := userStore.GetByID(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("me: lookup user: %w", err)
		}
		if row == nil {
			return nil, huma.Error401Unauthorized("not authenticated")
		}

		return &meOutput{Body: User{
			ID:    row.ID,
			Email: row.Email,
			Name:  row.Name,
			Role:  row.Role,
		}}, nil
	})

	// -----------------------------------------------------------------
	// POST /api/auth/keys — create API key
	// -----------------------------------------------------------------
	type createKeyBody struct {
		Name string `json:"name" minLength:"1" maxLength:"64"`
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
		userID := sessionManager.GetString(ctx, "user_id")
		if userID == "" {
			return nil, huma.Error401Unauthorized("not authenticated")
		}

		fullKey, prefix, err := GenerateAPIKey()
		if err != nil {
			return nil, fmt.Errorf("create key: generate: %w", err)
		}

		keyHash, err := HashAPIKey(fullKey)
		if err != nil {
			return nil, fmt.Errorf("create key: hash: %w", err)
		}

		row, err := keyStore.Create(ctx, userID, input.Body.Name, prefix, keyHash)
		if err != nil {
			return nil, fmt.Errorf("create key: store: %w", err)
		}

		return &createKeyOutput{Body: createKeyResponse{
			ID:        row.ID,
			Name:      row.Name,
			Key:       fullKey,
			Prefix:    prefix,
			CreatedAt: row.CreatedAt,
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
		userID := sessionManager.GetString(ctx, "user_id")
		if userID == "" {
			return nil, huma.Error401Unauthorized("not authenticated")
		}

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
		userID := sessionManager.GetString(ctx, "user_id")
		if userID == "" {
			return nil, huma.Error401Unauthorized("not authenticated")
		}

		if err := keyStore.Delete(ctx, input.ID, userID); err != nil {
			return nil, huma.Error404NotFound("key not found")
		}

		return nil, nil
	})

	// -----------------------------------------------------------------
	// POST /api/auth/change-password — authenticated user changes own password
	// -----------------------------------------------------------------
	type changePasswordBody struct {
		CurrentPassword string `json:"current_password" minLength:"1"`
		NewPassword     string `json:"new_password" minLength:"8"`
	}
	type changePasswordInput struct {
		Body changePasswordBody
	}
	huma.Register(api, huma.Operation{
		OperationID:   "change-password",
		Method:        http.MethodPost,
		Path:          "/api/auth/change-password",
		Summary:       "Change the current user's password",
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, input *changePasswordInput) (*struct{}, error) {
		userID := sessionManager.GetString(ctx, "user_id")
		if userID == "" {
			return nil, huma.Error401Unauthorized("not authenticated")
		}

		row, err := userStore.GetByID(ctx, userID)
		if err != nil || row == nil {
			return nil, huma.Error401Unauthorized("not authenticated")
		}

		if !CheckPassword(row.PasswordHash, input.Body.CurrentPassword) {
			return nil, huma.Error403Forbidden("current password is incorrect")
		}

		hash, err := HashPassword(input.Body.NewPassword)
		if err != nil {
			return nil, fmt.Errorf("change password: hash: %w", err)
		}

		if err := userStore.UpdatePassword(ctx, userID, hash); err != nil {
			return nil, fmt.Errorf("change password: update: %w", err)
		}

		return nil, nil
	})

	// -----------------------------------------------------------------
	// POST /api/admin/reset-password — owner resets another user's password
	// -----------------------------------------------------------------
	type adminResetBody struct {
		Email string `json:"email" maxLength:"255"`
	}
	type adminResetInput struct {
		Body adminResetBody
	}
	type adminResetResponse struct {
		TemporaryPassword string `json:"temporary_password"`
	}
	type adminResetOutput struct {
		Body adminResetResponse
	}
	huma.Register(api, huma.Operation{
		OperationID: "admin-reset-password",
		Method:      http.MethodPost,
		Path:        "/api/admin/reset-password",
		Summary:     "Reset a user's password (owner only)",
	}, func(ctx context.Context, input *adminResetInput) (*adminResetOutput, error) {
		user := UserFromContext(ctx)
		if user == nil {
			return nil, huma.Error401Unauthorized("not authenticated")
		}
		if !user.IsOwner() {
			return nil, huma.Error403Forbidden("owner role required")
		}

		target, err := userStore.GetByEmail(ctx, input.Body.Email)
		if err != nil {
			return nil, fmt.Errorf("admin reset: lookup: %w", err)
		}
		if target == nil {
			return nil, huma.Error404NotFound("user not found")
		}

		tempPass, err := GenerateTemporaryPassword()
		if err != nil {
			return nil, fmt.Errorf("admin reset: generate: %w", err)
		}

		hash, err := HashPassword(tempPass)
		if err != nil {
			return nil, fmt.Errorf("admin reset: hash: %w", err)
		}

		if err := userStore.UpdatePassword(ctx, target.ID, hash); err != nil {
			return nil, fmt.Errorf("admin reset: update password: %w", err)
		}
		if err := userStore.SetResetRequired(ctx, target.ID, true); err != nil {
			return nil, fmt.Errorf("admin reset: set flag: %w", err)
		}

		return &adminResetOutput{Body: adminResetResponse{
			TemporaryPassword: tempPass,
		}}, nil
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
		user := UserFromContext(ctx)
		if user == nil {
			return nil, huma.Error401Unauthorized("not authenticated")
		}
		if !user.IsOwner() {
			return nil, huma.Error403Forbidden("owner role required")
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
		user := UserFromContext(ctx)
		if user == nil {
			return nil, huma.Error401Unauthorized("not authenticated")
		}
		if !user.IsOwner() {
			return nil, huma.Error403Forbidden("owner role required")
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
		if target == nil {
			return nil, huma.Error404NotFound("user not found")
		}
		// Defence in depth: the id check above already covers the caller, but
		// an owner is never demotable by this route whichever id is used.
		if target.Role == RoleOwner {
			return nil, huma.Error403Forbidden("the owner's role cannot be changed")
		}

		updated, err := userStore.UpdateRole(ctx, target.ID, input.Body.Role)
		if err != nil {
			return nil, fmt.Errorf("set role: update: %w", err)
		}
		if !updated {
			return nil, huma.Error404NotFound("user not found")
		}

		return &setRoleOutput{Body: setRoleResponse{
			ID:    target.ID,
			Email: target.Email,
			Name:  target.Name,
			Role:  input.Body.Role,
		}}, nil
	})
}
