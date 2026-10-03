package env

import (
	"errors"
	"testing"
)

// toError keeps an Error, wraps a string, and otherwise uses JSON.stringify, which does not escape HTML characters
// (env/index.ts toError).
func TestToErrorMatchesJSONStringify(t *testing.T) {
	cause := errors.New("cause")
	if got := ToError(cause); !errors.Is(got, cause) || got.Error() != "cause" {
		t.Fatalf("error = %v", got)
	}
	for _, tc := range []struct {
		value any
		want  string
	}{
		{"plain <text>", "plain <text>"},
		{map[string]any{"a": "<b>&"}, `{"a":"<b>&"}`},
		{nil, "null"},
		{3.5, "3.5"},
	} {
		if got := ToError(tc.value).Error(); got != tc.want {
			t.Fatalf("ToError(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}
