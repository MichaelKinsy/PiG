package sdk

import (
	"reflect"
	"testing"
)

// TestProviderHookReply pins what a before_provider_request or before_provider_headers handler's reply carries: Pi's runner sends the payload the handlers left
// (a returned value replaces it, an in-place edit changes it) and ignores a headers handler's result (runner.ts emitBeforeProviderRequest, emitBeforeProviderHeaders).
func TestProviderHookReply(t *testing.T) {
	sent := func() any { return map[string]any{"model": "m", "n": float64(1)} }
	for _, tc := range []struct {
		name   string
		event  string
		object any
		result any
		want   any
	}{
		{"payload left as received is no reply", "before_provider_request", map[string]any{"model": "m", "n": float64(1)}, nil, nil},
		{"payload edited in place is the reply", "before_provider_request", map[string]any{"model": "m", "n": float64(1), "edit": "x"}, nil, map[string]any{"model": "m", "n": float64(1), "edit": "x"}},
		{"payload member changed in place is the reply", "before_provider_request", map[string]any{"model": "other", "n": float64(1)}, nil, map[string]any{"model": "other", "n": float64(1)}},
		{"a returned value replaces the payload and the edit of the old one is dropped", "before_provider_request", map[string]any{"edit": "x"}, map[string]any{"new": true}, map[string]any{"new": true}},
		{"a payload that is no object or list is no reply", "before_provider_request", "text", nil, nil},
		{"headers are always the object the handler left", "before_provider_headers", map[string]any{"x-a": "b"}, map[string]any{"ignored": true}, map[string]any{"x-a": "b"}},
		{"an event with no headers replies an empty object", "before_provider_headers", nil, nil, map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := providerHookReply(tc.event, tc.object, tc.result, sent); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("providerHookReply = %v, want %v", got, tc.want)
			}
		})
	}
}
