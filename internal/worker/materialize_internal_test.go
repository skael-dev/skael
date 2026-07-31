package worker

import (
	"encoding/json"
	"testing"
)

// A suite record whose spec column holds the jsonb literal null must be
// treated the same as no spec recorded at all — see evalsuite.Registry.Put's
// normalization and this function's doc comment. Before the fix,
// json.Unmarshal([]byte("null"), &sp) leaves sp as a non-nil, empty
// spec.SkillSpec, so this returned a spec that would fail Validate().
func TestUnmarshalSuiteSpec_TreatsJSONNullAsAbsent(t *testing.T) {
	sp, err := unmarshalSuiteSpec(json.RawMessage("null"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sp != nil {
		t.Fatalf("unmarshalSuiteSpec(null) = %+v, want nil", sp)
	}
}

func TestUnmarshalSuiteSpec_TreatsEmptyAsAbsent(t *testing.T) {
	sp, err := unmarshalSuiteSpec(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sp != nil {
		t.Fatalf("unmarshalSuiteSpec(nil) = %+v, want nil", sp)
	}
}
