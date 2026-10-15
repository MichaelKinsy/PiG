package host

import (
	"errors"
	"testing"
)

// Pi reload.ts:97-106 reloadError: the result keeps the cause's code, reason and details and says the reload aborted;
// a plain error becomes `unavailable` (asChordError, errors.ts) as reload.test.ts:150-151 asserts
// (`isChordError(error, "unavailable") && /reload aborted.*b fails/`); an error whose message (after its
// "<code>[/<reason>]: " prefix) already starts with "reload aborted" is returned unchanged.
func TestReloadErrorKeepsTheCauseCodeAndSaysTheReloadAborted(t *testing.T) {
	plain := errors.New("b fails")
	got := reloadError(plain)
	if got.Code != CodeUnavailable || got.Message != "unavailable: reload aborted: unavailable: reload: b fails" {
		t.Fatalf("plain: %q %q", got.Code, got.Message)
	}
	if !errors.Is(got, plain) {
		t.Fatalf("plain: cause chain lost %v", got)
	}

	details := map[string]any{"path": "x"}
	invalid := NewChordError(CodeInvalid, "r#1: schema of state n changed without a state-version bump", ChordErrorOptions{Reason: "state", Details: details})
	got = reloadError(invalid)
	if got.Code != CodeInvalid || got.Reason != "state" || got.Details == nil || got.Cause != invalid { //nolint:errorlint // the cause is the error itself, not a wrap
		t.Fatalf("invalid: %+v", got)
	}
	if want := "invalid/state: reload aborted: invalid/state: r#1: schema of state n changed without a state-version bump"; got.Message != want {
		t.Fatalf("invalid message = %q, want %q", got.Message, want)
	}

	aborted := NewChordError(CodeAborted, "reload aborted while preparing", ChordErrorOptions{Reason: "aborted"})
	if got := reloadError(aborted); got != aborted {
		t.Fatalf("already aborted: wrapped again as %q", got.Message)
	}
	if got := reloadError(got); got.Message != "invalid/state: reload aborted: invalid/state: r#1: schema of state n changed without a state-version bump" {
		t.Fatalf("a reload error is not wrapped twice: %q", got.Message)
	}
}
