package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skael-dev/skael/internal/audit"
	"github.com/skael-dev/skael/internal/platform"
)

var serviceAccountNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$`)

// ServiceAccount is a machine identity. Its name is the consumer it acts for.
type ServiceAccount struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	KeyCount   int        `json:"key_count"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

const serviceAccountSelect = `
	SELECT u.id, u.display_name, COUNT(k.id)::int, MAX(k.last_used_at), u.created_at
	FROM auth_users u LEFT JOIN user_api_keys k ON k.user_id = u.id
	WHERE u.kind = 'service'`

func collectServiceAccounts(rows pgx.Rows) ([]ServiceAccount, error) {
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ServiceAccount])
}

func listServiceAccounts(ctx context.Context, pool *pgxpool.Pool) ([]ServiceAccount, error) {
	rows, err := pool.Query(ctx, serviceAccountSelect+` GROUP BY u.id ORDER BY u.display_name`)
	if err != nil {
		return nil, fmt.Errorf("auth.listServiceAccounts: %w", err)
	}
	accounts, err := collectServiceAccounts(rows)
	if err != nil {
		return nil, fmt.Errorf("auth.listServiceAccounts: %w", err)
	}
	return accounts, nil
}

// createServiceAccount inserts the users row and its audit record. The email is
// a reserved .invalid address, so the unique column holds and no mail can reach
// it. The empty hash never verifies, and login refuses the kind as well.
func createServiceAccount(ctx context.Context, pool *pgxpool.Pool, name, actor string) (*ServiceAccount, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth.createServiceAccount: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	a := ServiceAccount{Name: name}
	err = tx.QueryRow(ctx, `
		INSERT INTO auth_users (id, email, email_normalized, display_name, role, kind, created_at)
		VALUES (gen_random_uuid(), $1, lower(trim($1)), $2, 'member', 'service', now())
		RETURNING id, created_at`, name+"@service-account.invalid", name).Scan(&a.ID, &a.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("auth.createServiceAccount: %w", err)
	}
	if err := audit.Record(ctx, tx, actor, "service_account.create", "service_account:"+name, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("auth.createServiceAccount: %w", err)
	}
	return &a, nil
}

// serviceAccountByID returns the account, or nil when the id names no account.
func serviceAccountByID(ctx context.Context, pool *pgxpool.Pool, id string) (*ServiceAccount, error) {
	if uuid.Validate(id) != nil {
		return nil, nil
	}
	rows, err := pool.Query(ctx, serviceAccountSelect+` AND u.id = $1 GROUP BY u.id`, id)
	if err != nil {
		return nil, fmt.Errorf("auth.serviceAccountByID: %w", err)
	}
	a, err := pgx.CollectOneRow(rows, pgx.RowToStructByPos[ServiceAccount])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth.serviceAccountByID: %w", err)
	}
	return &a, nil
}

// validateKeyLimits checks the scopes and the set limit a caller asked for and
// returns the set id to store.
func validateKeyLimits(ctx context.Context, keys *KeyStore, scopes []string, setID string) (*string, error) {
	for _, s := range scopes {
		if !ValidScope(s) {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("unknown scope %q", s))
		}
	}
	if setID == "" {
		return nil, nil
	}
	if !containsScope(scopes, ScopeSetsRead) {
		return nil, huma.Error422UnprocessableEntity("a set-limited key needs the sets:read scope")
	}
	exists := false
	if uuid.Validate(setID) == nil {
		if err := keys.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM sets WHERE id = $1)`, setID).Scan(&exists); err != nil {
			return nil, fmt.Errorf("auth.validateKeyLimits: %w", err)
		}
	}
	if !exists {
		return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("unknown set id %q", setID))
	}
	return &setID, nil
}

func containsScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

func requirePrivileged(ctx context.Context) (*User, error) {
	u := UserFromContext(ctx)
	if u == nil {
		return nil, huma.Error401Unauthorized("not authenticated")
	}
	if !u.IsPrivileged() {
		return nil, huma.Error403Forbidden("owner or admin role required")
	}
	return u, nil
}

// RegisterServiceAccountRoutes wires service account and audit endpoints.
func RegisterServiceAccountRoutes(api huma.API, pool *pgxpool.Pool, keys *KeyStore) {
	type serviceAccountListBody struct {
		ServiceAccounts []ServiceAccount `json:"service_accounts"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-service-accounts", Method: http.MethodGet, Path: "/api/service-accounts",
		Summary: "List service accounts",
	}, func(ctx context.Context, _ *struct{}) (*struct{ Body serviceAccountListBody }, error) {
		if _, err := requirePrivileged(ctx); err != nil {
			return nil, err
		}
		accounts, err := listServiceAccounts(ctx, pool)
		if err != nil {
			return nil, err
		}
		if accounts == nil {
			accounts = []ServiceAccount{}
		}
		return &struct{ Body serviceAccountListBody }{Body: serviceAccountListBody{ServiceAccounts: accounts}}, nil
	})

	type serviceAccountCreateBody struct {
		Name string `json:"name" minLength:"1" maxLength:"64" doc:"Lowercase letters, digits and hyphens. This name is the consumer."`
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-service-account", Method: http.MethodPost, Path: "/api/service-accounts",
		Summary: "Create a service account", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct{ Body serviceAccountCreateBody }) (*struct{ Body ServiceAccount }, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, err
		}
		if !serviceAccountNameRe.MatchString(in.Body.Name) {
			return nil, huma.Error422UnprocessableEntity("name must use lowercase letters, digits and hyphens, and start and end with a letter or digit")
		}
		a, err := createServiceAccount(ctx, pool, in.Body.Name, u.Email)
		if platform.IsDuplicateKey(err) {
			return nil, huma.Error409Conflict(fmt.Sprintf("service account %q already exists", in.Body.Name))
		}
		if err != nil {
			return nil, err
		}
		return &struct{ Body ServiceAccount }{Body: *a}, nil
	})

	type serviceAccountPath struct {
		ID string `path:"id"`
	}
	loadAccount := func(ctx context.Context, id string) (*User, *ServiceAccount, error) {
		u, err := requirePrivileged(ctx)
		if err != nil {
			return nil, nil, err
		}
		a, err := serviceAccountByID(ctx, pool, id)
		if err != nil {
			return nil, nil, err
		}
		if a == nil {
			return nil, nil, huma.Error404NotFound("service account not found")
		}
		return u, a, nil
	}

	huma.Register(api, huma.Operation{
		OperationID: "delete-service-account", Method: http.MethodDelete, Path: "/api/service-accounts/{id}",
		Summary: "Delete a service account and revoke its keys", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *serviceAccountPath) (*struct{}, error) {
		u, a, err := loadAccount(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("delete service account: %w", err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err := tx.Exec(ctx, `DELETE FROM auth_users WHERE id = $1 AND kind = 'service'`, a.ID); err != nil {
			return nil, fmt.Errorf("delete service account: %w", err)
		}
		if err := audit.Record(ctx, tx, u.Email, "service_account.delete", "service_account:"+a.Name, nil); err != nil {
			return nil, err
		}
		return nil, tx.Commit(ctx)
	})

	type serviceAccountKeysBody struct {
		Keys []APIKeyInfo `json:"keys"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-service-account-keys", Method: http.MethodGet, Path: "/api/service-accounts/{id}/keys",
		Summary: "List a service account's keys",
	}, func(ctx context.Context, in *serviceAccountPath) (*struct{ Body serviceAccountKeysBody }, error) {
		_, a, err := loadAccount(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		list, err := keys.ListByUser(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body serviceAccountKeysBody }{Body: serviceAccountKeysBody{Keys: list}}, nil
	})

	type serviceAccountKeyBody struct {
		Name   string   `json:"name" minLength:"1" maxLength:"64"`
		Scopes []string `json:"scopes" minItems:"1"`
		SetID  string   `json:"set_id,omitempty" doc:"Limit the key to this one set."`
	}
	type serviceAccountKeyCreated struct {
		ID        string    `json:"id"`
		Name      string    `json:"name"`
		Key       string    `json:"key" doc:"Shown once."`
		Prefix    string    `json:"prefix"`
		Scopes    []string  `json:"scopes"`
		SetID     *string   `json:"set_id,omitempty"`
		CreatedAt time.Time `json:"created_at"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "create-service-account-key", Method: http.MethodPost, Path: "/api/service-accounts/{id}/keys",
		Summary: "Create a scoped key for a service account", DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		ID   string `path:"id"`
		Body serviceAccountKeyBody
	}) (*struct{ Body serviceAccountKeyCreated }, error) {
		u, a, err := loadAccount(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if len(in.Body.Scopes) == 0 {
			return nil, huma.Error422UnprocessableEntity("a service key needs at least one scope")
		}
		setID, err := validateKeyLimits(ctx, keys, in.Body.Scopes, in.Body.SetID)
		if err != nil {
			return nil, err
		}
		fullKey, prefix, err := GenerateAPIKey()
		if err != nil {
			return nil, err
		}
		hash, err := HashAPIKey(fullKey)
		if err != nil {
			return nil, err
		}
		row, err := keys.CreateLimited(ctx, a.ID, in.Body.Name, prefix, hash, in.Body.Scopes, setID)
		if err != nil {
			return nil, err
		}
		if err := audit.Record(ctx, pool, u.Email, "service_account.key.create", "service_account:"+a.Name,
			map[string]any{"key": row.ID, "scopes": row.Scopes, "set_id": row.SetID}); err != nil {
			return nil, err
		}
		return &struct{ Body serviceAccountKeyCreated }{Body: serviceAccountKeyCreated{
			ID: row.ID, Name: row.Name, Key: fullKey, Prefix: prefix, Scopes: row.Scopes, SetID: row.SetID, CreatedAt: row.CreatedAt,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "revoke-service-account-key", Method: http.MethodDelete, Path: "/api/service-accounts/{id}/keys/{keyId}",
		Summary: "Revoke a service account's key", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *struct {
		ID    string `path:"id"`
		KeyID string `path:"keyId"`
	}) (*struct{}, error) {
		u, a, err := loadAccount(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		if uuid.Validate(in.KeyID) != nil || keys.Delete(ctx, in.KeyID, a.ID) != nil {
			return nil, huma.Error404NotFound("key not found")
		}
		return nil, audit.Record(ctx, pool, u.Email, "service_account.key.revoke", "service_account:"+a.Name,
			map[string]any{"key": in.KeyID})
	})

	type auditListBody struct {
		Entries []audit.AuditEntry `json:"entries"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "list-audit-log", Method: http.MethodGet, Path: "/api/admin/audit",
		Summary: "List recent audit entries",
	}, func(ctx context.Context, in *struct {
		Limit int `query:"limit" minimum:"1" maximum:"500" default:"100"`
	}) (*struct{ Body auditListBody }, error) {
		if _, err := requirePrivileged(ctx); err != nil {
			return nil, err
		}
		entries, err := audit.List(ctx, pool, in.Limit)
		if err != nil {
			return nil, err
		}
		if entries == nil {
			entries = []audit.AuditEntry{}
		}
		return &struct{ Body auditListBody }{Body: auditListBody{Entries: entries}}, nil
	})
}
