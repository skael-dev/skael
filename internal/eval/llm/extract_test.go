package llm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/skael-dev/skael/internal/eval/llm"
)

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare object", `{"a":1}`, `{"a":1}`},
		{"fenced json", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"fenced bare", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"prose preamble", "Sure! Here is the spec:\n```json\n{\"a\":1}\n```\nLet me know.", `{"a":1}`},
		{"prose no fence", `Here you go: {"a":1} — done.`, `{"a":1}`},
		{"nested braces", `text {"a":{"b":[1,2]},"c":"}"} tail`, `{"a":{"b":[1,2]},"c":"}"}`},
		{"array root", `[{"a":1}]`, `[{"a":1}]`},
		{"brace inside string", `{"a":"it is { unbalanced"}`, `{"a":"it is { unbalanced"}`},
		{"escaped quote", `{"a":"say \"hi\""}`, `{"a":"say \"hi\""}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := llm.ExtractJSON(tt.in)
			if err != nil {
				t.Fatalf("ExtractJSON(%q): %v", tt.in, err)
			}
			if !json.Valid(got) {
				t.Fatalf("ExtractJSON returned invalid JSON: %s", got)
			}
			var a, b any
			_ = json.Unmarshal(got, &a)
			_ = json.Unmarshal([]byte(tt.want), &b)
			if string(got) != tt.want {
				t.Errorf("ExtractJSON(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestExtractJSON_FailsLoudlyWithRawText(t *testing.T) {
	_, err := llm.ExtractJSON("I'm afraid I can't do that.")
	if err == nil {
		t.Fatal("ExtractJSON accepted prose with no JSON")
	}
	// The raw text must appear in the error. A bare "no JSON found" makes a
	// refusal, a rate-limit notice, and a truncated response indistinguishable.
	if !strings.Contains(err.Error(), "can't do that") {
		t.Errorf("error does not quote the raw text: %v", err)
	}
}

func TestExtractJSON_UnbalancedIsAnError(t *testing.T) {
	if _, err := llm.ExtractJSON(`{"a":1`); err == nil {
		t.Error("ExtractJSON accepted a truncated object")
	}
}
