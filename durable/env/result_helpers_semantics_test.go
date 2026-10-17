package env

import (
	"errors"
	"testing"
)

// packages/durable/src/env/index.ts:14-17 getOrThrow: a failed result throws its error and a successful one returns exactly its value, whatever else the result carries.
func TestGetOrThrowIgnoresTheFieldTheOutcomeDoesNotUse(t *testing.T) {
	failure := errors.New("failed")
	if value, err := GetOrThrow(Result[int, error]{Ok: false, Value: 7, Error: failure}); !errors.Is(err, failure) || value != 0 {
		t.Fatalf("a failed result returned (%d, %v), want (0, the failure)", value, err)
	}
	if value, err := GetOrThrow(Result[int, error]{Ok: true, Value: 7, Error: failure}); err != nil || value != 7 {
		t.Fatalf("a successful result returned (%d, %v), want (7, nil)", value, err)
	}
}

// index.ts:23-31 toError: a value JSON.stringify cannot encode becomes String(value), and the encoded text of any other value carries no trailing newline.
func TestToErrorFallsBackToTheDefaultFormattingAndTrimsTheEncoderNewline(t *testing.T) {
	if got := ToError(complex(1, 2)).Error(); got != "(1+2i)" {
		t.Errorf("ToError(complex) = %q, want the default formatting (1+2i)", got)
	}
	if got := ToError([]int{1, 2}).Error(); got != "[1,2]" {
		t.Errorf("ToError([]int) = %q, want [1,2] without a newline", got)
	}
}
