package rules

import "testing"

// TestS5lPromiseLikeIsPromise: an upstream call that returns PromiseLike<void> (telemetry's [Symbol.asyncDispose]) is judged as one that
// returns Promise<void>: Go takes a context and returns an error. A Go function that returns a value besides the error does
// not carry it, and an upstream that returns a plain value is not made async by the rewrite.
func TestS5lPromiseLikeIsPromise(t *testing.T) {
	scope := checkCall(t, `

func Dispose(ctx context.Context) error                { return nil }
func Pair(ctx context.Context) (string, error)         { return "", nil }
`)
	for _, tc := range []struct {
		name, returns, fn string
		want              Tri
	}{
		{"PromiseLike void, ctx and error", "PromiseLike<void>", "Dispose", Yes},
		{"Promise void, ctx and error", "Promise<void>", "Dispose", Yes},
		{"PromiseLike void, Go returns a value", "PromiseLike<void>", "Pair", No},
	} {
		got := Signature(testEnv(scope), call(tc.returns), sigOf(t, scope, tc.fn))
		if got.OK != tc.want {
			t.Errorf("%s: %v (%s), want %v", tc.name, got.OK, got.Why, tc.want)
		}
	}
}
