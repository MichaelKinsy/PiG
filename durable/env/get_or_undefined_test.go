package env_test

import (
	"errors"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// packages/durable/src/env/index.ts:19-21 getOrUndefined: the value of an ok result, undefined (nil) for a failure.
func TestGetOrUndefinedReturnsTheValueOrNil(t *testing.T) {
	value := "content"
	if got := durableenv.GetOrUndefined(durableenv.Ok[*string, error](&value)); got == nil || *got != "content" {
		t.Fatalf("GetOrUndefined(Ok) = %v, want the value", got)
	}
	if got := durableenv.GetOrUndefined(durableenv.Err[*string, error](errors.New("failed"))); got != nil {
		t.Fatalf("GetOrUndefined(Err) = %v, want nil", got)
	}
}
