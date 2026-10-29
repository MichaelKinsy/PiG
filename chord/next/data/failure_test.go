package data_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/test/next/data/failure.test.ts.

func TestFailureCauses(t *testing.T) {
	t.Run("a value thrown by an open is transient unless marked broken", func(t *testing.T) {
		err := errors.New("network down")
		transient := data.OpenFailure(err, "open notes")
		if transient.Kind != data.FailureTransient {
			t.Fatalf("kind = %q", transient.Kind)
		}
		nexttest.RequireJSONEqual(t, transient.Error.ToJSON(), delta.JsonObjectOf("code", "unavailable", "message", "open notes: network down"))
		if !errors.Is(transient.Error.Cause, err) {
			t.Fatal("the thrown error is not the cause")
		}
	})

	t.Run("broken() marks an open's failure as broken; its message is the cause's", func(t *testing.T) {
		marked := data.Broken(errors.New("bad config"))
		if marked.Error() != "bad config" {
			t.Fatalf("message = %q", marked.Error())
		}
		cause := data.OpenFailure(marked, "open notes")
		if cause.Kind != data.FailureBroken || cause.Error.Message != "open notes: bad config" {
			t.Fatalf("cause = %v %q", cause.Kind, cause.Error.Message)
		}
	})

	t.Run("the kind comes from the stage, never from the error code", func(t *testing.T) {
		chord := data.Invalid(data.ReasonHostAddress, "wrong host")
		if got := data.OpenFailure(chord, "open"); got.Kind != data.FailureTransient || got.Error != chord {
			t.Fatalf("OpenFailure(chord) = %v %p", got.Kind, got.Error)
		}
		if got := data.OpenFailure(data.Broken(chord), "open"); got.Kind != data.FailureBroken || got.Error != chord {
			t.Fatalf("OpenFailure(broken(chord)) = %v %p", got.Kind, got.Error)
		}
	})
}

func TestWaitingCause(t *testing.T) {
	root := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "app.catalog is not served here"}, nil)
	unavailable := func(message string) *data.ChordError {
		return data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: message}, nil)
	}

	t.Run("names the nearest blocker and the root cause, its blocker's error as the cause", func(t *testing.T) {
		cause := data.WaitingFor("app.chat(S1)", root)
		if cause.Kind != data.FailureWaiting {
			t.Fatalf("kind = %q", cause.Kind)
		}
		nexttest.RequireJSONEqual(t, cause.Error.ToJSON(), delta.JsonObjectOf(
			"code", "unavailable", "message", "waiting for app.chat(S1): app.catalog is not served here",
		))
		if !errors.Is(cause.Error.Cause, root) {
			t.Fatal("the blocker's error is not the cause")
		}
		// A chain names its nearest blocker and its root only (fix round 1, Opus F1).
		chain := data.WaitingFor("app.panel", cause.Error)
		if chain.Error.Message != "waiting for app.panel: app.catalog is not served here" {
			t.Fatalf("chain message = %q", chain.Error.Message)
		}
	})

	t.Run("reaches a fixed point when a ring of waiting attempts passes its causes round (Opus F1)", func(t *testing.T) {
		b := data.WaitingFor("X", unavailable("X is not served here")).Error
		rounds := 0
		for ; rounds < 10; rounds++ {
			next := data.WaitingFor("A", data.WaitingFor("B", b).Error).Error
			if next.Message == b.Message {
				break
			}
			b = next
		}
		if rounds > 1 {
			t.Fatalf("rounds = %d, want at most 1", rounds)
		}
		if b.Message != "waiting for A: X is not served here" {
			t.Fatalf("message = %q", b.Message)
		}
	})

	t.Run("keeps the root cause within 500 characters: a long label is clipped first (Sol 8)", func(t *testing.T) {
		original := unavailable("ORIGINAL: missing provider")
		message := data.WaitingFor("sol.item("+strings.Repeat("x", 600)+")", original).Error.Message
		if units := jsstring.Length(message); units > 500 {
			t.Fatalf("length = %d, want at most 500", units)
		}
		if !strings.Contains(message, original.Message) || !strings.HasPrefix(message, "waiting for sol.item(xxx") {
			t.Fatalf("message = %q", message)
		}
	})

	t.Run("keeps a long chain's root cause, never repeated ellipses (Sol 8)", func(t *testing.T) {
		err := unavailable("ORIGINAL: missing provider")
		for i := range 2000 {
			err = data.WaitingFor("level-"+strconv.Itoa(i), err).Error
		}
		if err.Message != "waiting for level-1999: ORIGINAL: missing provider" {
			t.Fatalf("message = %q", err.Message)
		}
	})

	t.Run("clips a long root past its share, and both when both are long", func(t *testing.T) {
		long := unavailable(strings.Repeat("y", 600))
		short := data.WaitingFor("app.a", long).Error.Message
		if jsstring.Length(short) != 500 || !strings.HasPrefix(short, "waiting for app.a: yyy") {
			t.Fatalf("short = %d %q", jsstring.Length(short), short)
		}
		both := data.WaitingFor(strings.Repeat("z", 600), long).Error.Message
		if units := jsstring.Length(both); units != 500 || !strings.HasSuffix(both, "y…") {
			t.Fatalf("both = %d units, ends %q", units, both[len(both)-4:])
		}
	})

	t.Run("is compared by value: kind, code, reason and message", func(t *testing.T) {
		if !data.SameCause(data.WaitingFor("a", root), data.WaitingFor("a", root)) {
			t.Error("equal waiting causes differ")
		}
		if data.SameCause(data.WaitingFor("a", root), data.WaitingFor("b", root)) {
			t.Error("causes naming different blockers are the same")
		}
		if data.SameCause(data.FailureCause{Kind: data.FailureBroken, Error: root}, data.FailureCause{Kind: data.FailureTransient, Error: root}) {
			t.Error("causes of different kinds are the same")
		}
		other := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonUndeclared, Message: root.Message}, nil)
		if data.SameCause(data.FailureCause{Kind: data.FailureWaiting, Error: root}, data.FailureCause{Kind: data.FailureWaiting, Error: other}) {
			t.Error("causes of different reasons are the same")
		}
	})
}
