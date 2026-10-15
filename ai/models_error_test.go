package ai

import (
	"errors"
	"testing"
)

// packages/ai/src/utils/models-error.ts:5-24 (ModelsError, withCauseDetail).
func TestNewModelsErrorCauseDetail(t *testing.T) {
	cause := errors.New("  network down \n")
	for _, tc := range []struct {
		name    string
		message string
		cause   error
		want    string
	}{
		{"no cause keeps the message", "load failed", nil, "load failed"},
		{"the cause detail is appended trimmed", "load failed", cause, "load failed: network down"},
		{"a blank cause adds nothing", "load failed", errors.New(" \t\n"), "load failed"},
		{"a message that already holds the detail is kept", "load failed: network down", cause, "load failed: network down"},
		{"containment is a substring test", "network down hard", cause, "network down hard"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := NewModelsError(ModelsErrorModelSource, tc.message, tc.cause)
			if got.Error() != tc.want || got.Message != tc.want || got.Code != ModelsErrorModelSource {
				t.Fatalf("got %q code=%q, want %q", got.Error(), got.Code, tc.want)
			}
			if !errors.Is(got, tc.cause) && tc.cause != nil {
				t.Fatalf("cause is not unwrapped: %v", got.Unwrap())
			}
		})
	}
	// :3 the six upstream codes.
	for code, want := range map[ModelsErrorCode]string{ModelsErrorModelSource: "model_source", ModelsErrorModelValidation: "model_validation", ModelsErrorProvider: "provider", ModelsErrorStream: "stream", ModelsErrorAuth: "auth", ModelsErrorOAuth: "oauth"} {
		if string(code) != want {
			t.Errorf("code %q want %q", code, want)
		}
	}
}
