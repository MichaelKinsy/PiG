package host

import "testing"

// The principals the Pi chord tests use (test/*.test.ts at aa9cf15b): the host's own is { kind: "host" } and what a
// child's parent acts as is { kind: "server" }.

func kindHost() Principal { return map[string]any{"kind": "host"} }

func kindServer() Principal { return map[string]any{"kind": "server"} }

// chordPanic calls fn and returns the ChordError it panics with (nil if it returns): Pi's `assert.throws(() => x.value,
// (error) => isChordError(error, code))` for a state getter that throws.
func chordPanic(fn func()) (thrown *ChordError) {
	defer func() {
		if recovered := recover(); recovered != nil {
			thrown, _ = recovered.(*ChordError)
			if thrown == nil {
				panic(recovered)
			}
		}
	}()
	fn()
	return nil
}

// requireNotReady fails unless reading fn throws not_ready (host.test.ts: `isChordError(error, "not_ready")`).
func requireNotReady(t *testing.T, what string, fn func()) {
	t.Helper()
	if thrown := chordPanic(fn); thrown == nil || thrown.Code != CodeNotReady {
		t.Fatalf("%s: threw %v, want not_ready", what, thrown)
	}
}
