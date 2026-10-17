package host

import (
	"errors"
	"fmt"
	"testing"
)

// Pi errors.ts:47 `forbidden = (message = "forbidden")`: the default applies only when the argument is omitted
// (undefined); an explicit empty message stays empty.
func TestForbiddenDefaultsOnlyAnOmittedMessage(t *testing.T) {
	if got := Forbidden().Message; got != "forbidden: forbidden" {
		t.Fatalf("Forbidden() = %q", got)
	}
	if got := Forbidden("").Message; got != "forbidden: " {
		t.Fatalf(`Forbidden("") = %q, want "forbidden: "`, got)
	}
	if got := Forbidden("not for you"); got.Message != "forbidden: not for you" || got.Code != CodeForbidden || got.Retryable {
		t.Fatalf("Forbidden(not for you) = %+v", got)
	}
}

// Pi errors.ts:25-42: a ChordError's name is "ChordError", its message is "<code>[/<reason>]: <message>", retryable
// is true only for superseded and upgrade_conflict, and cause, details and reason are the options.
func TestChordErrorCarriesPiFields(t *testing.T) {
	cause := errors.New("disk")
	err := NewChordError(CodeInvalid, "bad args", ChordErrorOptions{Cause: cause, Details: []any{"x"}, Reason: "args"})
	if err.Name != "ChordError" || err.Message != "invalid/args: bad args" || err.Code != CodeInvalid || err.Reason != "args" ||
		err.Retryable || err.Cause != cause || fmt.Sprint(err.Details) != "[x]" { //nolint:errorlint // the cause is the same error value
		t.Fatalf("NewChordError = %+v", err)
	}
	if err.Error() != err.Message || !errors.Is(err, cause) {
		t.Fatalf("Error/Unwrap = %q, %v", err.Error(), errors.Is(err, cause))
	}
	for code, retryable := range map[ChordErrorCode]bool{
		CodeSuperseded: true, CodeUpgradeConflict: true, CodeAborted: false, CodeGone: false, CodeUnavailable: false,
	} {
		if got := NewChordError(code, "m", ChordErrorOptions{}); got.Retryable != retryable || got.Message != string(code)+": m" {
			t.Errorf("%s: %+v", code, got)
		}
	}
	if got := Invalid("cycle", "loop", 3); got.Message != "invalid/cycle: loop" || got.Reason != "cycle" || got.Details != 3 {
		t.Fatalf("Invalid = %+v", got)
	}
}

// Pi errors.ts:44-45 `isChordError = (error, code?) => error instanceof ChordError && (code === undefined || ...)`: the
// code is optional, and instanceof does not look through a wrapper such as runAll's AggregateError (errors.ts:72).
func TestIsChordErrorIsInstanceofWithAnOptionalCode(t *testing.T) {
	gone := NewChordError(CodeGone, "g", ChordErrorOptions{})
	if !IsChordError(gone) || !IsChordError(gone, CodeGone) || IsChordError(gone, CodeAborted) {
		t.Fatal("IsChordError on a ChordError")
	}
	if IsChordError(errors.New("plain")) || IsChordError(nil) {
		t.Fatal("IsChordError on a non-ChordError")
	}
	aggregate := runAll([]func() error{func() error { return gone }, func() error { return gone }}, "two failed")
	if IsChordError(aggregate) || IsChordError(aggregate, CodeGone) {
		t.Fatalf("IsChordError(AggregateError) = true; Pi instanceof is false")
	}
	if got := ToWireError(aggregate); got != (WireError{Code: CodeUnavailable, Message: "two failed"}) {
		t.Fatalf("ToWireError(AggregateError) = %+v; Pi wire.ts:116-123 is unavailable with the aggregate message", got)
	}
}

// Pi errors.ts:50-56 asChordError keeps a ChordError (the same object) and makes anything else unavailable,
// "<context>: <message>", with the original as cause.
func TestAsChordErrorKeepsChordErrorsAndWrapsOthers(t *testing.T) {
	gone := NewChordError(CodeGone, "g", ChordErrorOptions{Reason: "r"})
	if asChordError(gone, "ctx") != gone {
		t.Fatal("asChordError did not keep the ChordError")
	}
	plain := errors.New("boom")
	got := asChordError(plain, "loading x")
	if got.Code != CodeUnavailable || got.Message != "unavailable: loading x: boom" || got.Cause != plain || got.Reason != "" { //nolint:errorlint // asChordError keeps the same error value
		t.Fatalf("asChordError(plain) = %+v", got)
	}
	wrapped := fmt.Errorf("outer: %w", gone)
	if got := asChordError(wrapped, "c"); got == gone || got.Code != CodeUnavailable || got.Cause != wrapped { //nolint:errorlint // identity: Pi's instanceof does not unwrap
		t.Fatalf("asChordError(wrapped) = %+v; Pi instanceof does not unwrap", got)
	}
}

// Pi errors.ts:59-73 runAll runs every step in order even after a failure, then throws nothing, the one error, or an
// AggregateError(errors, message) of all of them in order. A step that throws (a Go panic) counts as a failure.
func TestRunAllRunsEveryStepAndAggregates(t *testing.T) {
	var order []int
	step := func(index int, err error) func() error {
		return func() error { order = append(order, index); return err }
	}
	if err := runAll([]func() error{step(1, nil), step(2, nil)}, "m"); err != nil || fmt.Sprint(order) != "[1 2]" {
		t.Fatalf("no failure: %v %v", err, order)
	}
	one := errors.New("one")
	order = nil
	if err := runAll([]func() error{step(1, one), step(2, nil)}, "m"); err != one || fmt.Sprint(order) != "[1 2]" { //nolint:errorlint // one failure is rethrown itself, not wrapped
		t.Fatalf("one failure: %v %v", err, order)
	}
	two := errors.New("two")
	order = nil
	err := runAll([]func() error{step(1, one), func() error { order = append(order, 2); panic(two) }, step(3, nil)}, "several")
	var aggregate *AggregateError
	if !errors.As(err, &aggregate) || aggregate.Message != "several" || err.Error() != "several" || len(aggregate.Errors) != 2 ||
		aggregate.Errors[0] != one || aggregate.Errors[1] != two || fmt.Sprint(order) != "[1 2 3]" { //nolint:errorlint // the aggregate holds the failures themselves
		t.Fatalf("two failures: %#v %v", err, order)
	}
	if !errors.Is(err, one) || !errors.Is(err, two) {
		t.Fatal("AggregateError does not expose its errors")
	}
}
