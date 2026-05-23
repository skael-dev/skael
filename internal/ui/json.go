package ui

import (
	"encoding/json"
	"fmt"
	"os"
)

// PrintJSON encodes v to stdout with 2-space indentation.
func PrintJSON(v interface{}) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// PrintJSONError prints a structured JSON error object to stdout.
func PrintJSONError(msg, code, suggestion string) {
	obj := struct {
		Error      string `json:"error"`
		Code       string `json:"code,omitempty"`
		Suggestion string `json:"suggestion,omitempty"`
	}{
		Error:      msg,
		Code:       code,
		Suggestion: suggestion,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(obj); err != nil {
		// Fallback if encoding fails
		fmt.Fprintf(os.Stdout, `{"error":%q}`+"\n", msg)
	}
}
