package skill_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/skael-dev/skael/internal/skill"
	"github.com/skael-dev/skael/internal/testutil"
	"github.com/stretchr/testify/require"
)

func TestSearch_ByName(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	_, err := s.Create(ctx, "code-review", "Code Review", "Reviews code for quality", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	_, err = s.Create(ctx, "deployment", "Deployment", "Deploys applications", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	results, err := s.Search(ctx, "code-review", 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, "code-review", results[0].Name)
}

func TestSearch_ByContent(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	_, err := s.Create(ctx, "security-scan", "Security Scanner", "Detects SQL injection vulnerabilities in code", "Scans for SQL injection vulnerabilities", json.RawMessage(`{}`))
	require.NoError(t, err)

	results, err := s.Search(ctx, "injection", 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, "security-scan", results[0].Name)
}

func TestSearch_FuzzyByName(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	_, err := s.Create(ctx, "code-review", "Code Review", "Reviews code quality", "", json.RawMessage(`{}`))
	require.NoError(t, err)

	// Intentional typo: "code-reveiw" instead of "code-review"
	results, err := s.Search(ctx, "code-reveiw", 10)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	require.Equal(t, "code-review", results[0].Name)
}

func TestSearch_NoResults(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	s := skill.NewStore(pool)
	ctx := context.Background()

	results, err := s.Search(ctx, "nonexistentthing", 10)
	require.NoError(t, err)
	require.Empty(t, results)
}
