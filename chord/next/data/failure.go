package data

import (
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/chord/src/next/data/failure.ts: failure causes. Transient (the object's own effect failed: retry
// later), broken (its own code, a cycle or authority: retry by hand) or waiting (not its fault: no provider, no fitting
// version, a dependency that failed; woken by an event). The failing stage decides, never the error code.

// FailureKind is the kind of a FailureCause, from the failing stage, never from the error code.
type FailureKind string

// The kinds of FailureCause: transient (the object's own effect failed: retry later), broken (its own code, a cycle or
// authority: retry by hand) or waiting (not its fault: no provider, no fitting version, a dependency that failed; woken
// by an event).
const (
	FailureTransient FailureKind = "transient"
	FailureBroken    FailureKind = "broken"
	FailureWaiting   FailureKind = "waiting"
)

// FailureCause is why an object, or a request, is failed.
type FailureCause struct {
	Kind  FailureKind
	Error *ChordError
}

// BrokenOpen marks an open's failure as broken (bad configuration): `return nil, Broken(err)`. Its message is its
// cause's, and Unwrap returns the cause.
type BrokenOpen struct{ cause error }

// Broken marks an open's failure as broken (bad configuration).
func Broken(cause error) error { return &BrokenOpen{cause: cause} }

// Error returns the cause's message.
func (err *BrokenOpen) Error() string { return err.cause.Error() }

// Unwrap returns the cause.
func (err *BrokenOpen) Unwrap() error { return err.cause }

// OpenFailure is the cause of an error returned by an open: broken when it came from Broken, transient otherwise.
func OpenFailure(thrown error, label string) FailureCause {
	if broken, ok := thrown.(*BrokenOpen); ok {
		return FailureCause{Kind: FailureBroken, Error: AsChordError(broken.cause, label)}
	}
	return FailureCause{Kind: FailureTransient, Error: AsChordError(thrown, label)}
}

const (
	// messageLimit is the longest message a `waiting` cause of a dependent carries (telemetry's rule for error messages),
	// in UTF-16 code units as JavaScript counts them.
	messageLimit = 500
	// waitingPrefix is how a dependent's message names its blocker: the nearest one first.
	waitingPrefix = "waiting for "
	// waitingSeparator follows a blocker's label.
	waitingSeparator = ": "
	// clippedMarker marks a clipped label or root.
	clippedMarker = "…"
	// rootLimit is the most of the 500 characters the root cause takes when both are long.
	rootLimit = 320
)

// WaitingFor is the `waiting` cause of a dependent parked on blocker, which failed with err: `unavailable`, its message
// always "waiting for <blocker>: <root cause>" (the root found through any "waiting for …: " of err's message), so a
// ring of waiting attempts passing causes round reaches the same message after one round; within 500 characters, the
// label clipped first, the root only past its share. err is its cause.
func WaitingFor(blocker string, err *ChordError) FailureCause {
	root := rootMessage(err.Message)
	label := blocker
	room := messageLimit - len(waitingPrefix) - len(waitingSeparator)
	if utf16Len(label)+utf16Len(root) > room {
		rootRoom := max(rootLimit, room-utf16Len(label))
		if utf16Len(root) > rootRoom {
			root = clipped(root, rootRoom)
		}
		if utf16Len(label)+utf16Len(root) > room {
			label = clipped(label, room-utf16Len(root))
		}
	}
	message := waitingPrefix + label + waitingSeparator + root
	return FailureCause{Kind: FailureWaiting, Error: NewChordError(ChordErrorInit{Code: CodeUnavailable, Message: message}, err)}
}

func utf16Len(text string) int { return jsstring.Length(text) }

// clipped is text within length UTF-16 code units, clipped with "…" when longer. JavaScript slices code units; a cut
// through a surrogate pair leaves a lone surrogate, which jsstring holds as WTF-8.
func clipped(text string, length int) string {
	if utf16Len(text) <= length {
		return text
	}
	return jsstring.Slice(text, 0, max(length-1, 0)) + clippedMarker // the marker is one code unit
}

// rootMessage is the root cause in a "waiting for X: root" message: past every leading "waiting for …: ".
func rootMessage(message string) string {
	rest := message
	for strings.HasPrefix(rest, waitingPrefix) {
		end := strings.Index(rest[len(waitingPrefix):], waitingSeparator)
		if end < 0 {
			break
		}
		rest = rest[len(waitingPrefix)+end+len(waitingSeparator):]
	}
	return rest
}

// SameCause reports whether two failure causes say the same (a wake compares causes by value): kind, code, reason and
// message.
func SameCause(a, b FailureCause) bool {
	return a.Kind == b.Kind && a.Error.Code == b.Error.Code && a.Error.Reason == b.Error.Reason && a.Error.Message == b.Error.Message
}
