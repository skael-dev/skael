package server

import (
	"context"
	"errors"
	"testing"

	"github.com/skael-dev/skael/internal/evalsuite"
	"github.com/skael-dev/skael/internal/platform"
	"github.com/skael-dev/skael/internal/testutil"
)

// evalSuiteAdapter must honor skill.SuiteLookup's (nil, nil) contract for
// "no suite registered" — evalsuite.Registry.LatestForSkill itself reports
// that case as an error wrapping evalsuite.ErrNotFound, not as (nil, nil),
// so the adapter is the one place that must translate it.
func TestEvalSuiteAdapter_NoSuiteIsNilNilNotAnError(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	storage, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := evalsuite.NewRegistry(pool, storage)
	a := evalSuiteAdapter{r: reg}

	rec, err := a.LatestForSkill(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("err = %v, want nil for an unregistered skill", err)
	}
	if rec != nil {
		t.Fatalf("rec = %+v, want nil for an unregistered skill", rec)
	}
}

// An infrastructure failure in the underlying lookup (pool exhaustion,
// timeout, cancellation) is not the same fact as "no suite registered", and
// must not be silently reported as absence — that would let a skill that
// genuinely has a suite ship unscored, with no signal to the operator, the
// moment the database blips.
func TestEvalSuiteAdapter_PropagatesInfrastructureErrors(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	storage, err := platform.NewLocalStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg := evalsuite.NewRegistry(pool, storage)
	a := evalSuiteAdapter{r: reg}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // force the query to fail with something other than "no rows"

	rec, err := a.LatestForSkill(ctx, "whatever")
	if err == nil {
		t.Fatal("err = nil, want a propagated infrastructure error")
	}
	if errors.Is(err, evalsuite.ErrNotFound) {
		t.Fatalf("err = %v, must not present as ErrNotFound — that is a different fact", err)
	}
	if rec != nil {
		t.Fatalf("rec = %+v, want nil alongside a non-nil error", rec)
	}
}
