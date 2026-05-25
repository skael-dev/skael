package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/mail"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/danielgtaylor/huma/v2"

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
			row, err = userStore.CreateWithRole(ctx, input.Body.Email, input.Body.Name, hash, "owner")
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
	type loginOutput struct {
		Body User
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

		return &loginOutput{Body: User{
			ID:    row.ID,
			Email: row.Email,
			Name:  row.Name,
			Role:  row.Role,
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
}
