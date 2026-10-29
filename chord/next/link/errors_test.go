package link_test

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/link/errors.test.ts. `wireError("plain")` (a thrown non-Error) has no Go form: Go
// errors are error values; the plugin code `internal` case is covered by a plain error.

func TestErrorsOnTheWire(t *testing.T) {
	t.Run("carry a ChordError as its JSON, without stack or cause", func(t *testing.T) {
		err := data.NewChordError(data.ChordErrorInit{Code: data.CodeInvalid, Reason: data.ReasonArgs, Message: "bad"}, errors.New("x"))
		wire := link.WireErrorOf(err)
		nexttest.RequireJSONEqual(t, wire, object("kind", "chord", "error", object("code", "invalid", "reason", "args", "message", "bad")))
		back, backErr := link.ErrorFromWire(wire)
		requireNoError(t, backErr)
		chordErr, ok := data.IsChordError(back)
		if !ok || chordErr.Code != data.CodeInvalid || chordErr.Reason != data.ReasonArgs || chordErr.Message != "bad" || chordErr.Cause != nil {
			t.Fatalf("back = %#v", back)
		}
	})

	t.Run("carry a PluginError's code, message and details only; the consumer rethrows a PluginError", func(t *testing.T) {
		wire := link.WireErrorOf(data.NewPluginError("quota", "too many notes", object("limit", 3.0)))
		nexttest.RequireJSONEqual(t, wire, object("kind", "plugin", "code", "quota", "message", "too many notes", "details", object("limit", 3.0)))
		back, err := link.ErrorFromWire(wire)
		requireNoError(t, err)
		var plugin *data.PluginError
		ok := errors.As(back, &plugin)
		if !ok || plugin.Code != "quota" || plugin.Message != "too many notes" || !plugin.HasDetails {
			t.Fatalf("back = %#v", back)
		}
		nexttest.RequireJSONEqual(t, plugin.Details, object("limit", 3.0))
		nexttest.RequireJSONEqual(t, link.WireErrorOf(data.NewPluginError("c", "m")), object("kind", "plugin", "code", "c", "message", "m"))
		// A null details is details, not an absent one.
		nexttest.RequireJSONEqual(t, link.WireErrorOf(data.NewPluginError("c", "m", nil)), object("kind", "plugin", "code", "c", "message", "m", "details", nil))
	})

	t.Run("carry anything else as the plugin code internal with its message", func(t *testing.T) {
		nexttest.RequireJSONEqual(t, link.WireErrorOf(errors.New("boom")), object("kind", "plugin", "code", "internal", "message", "boom"))
	})

	t.Run("throw on a malformed ChordError envelope (a protocol error)", func(t *testing.T) {
		_, err := link.ErrorFromWire(object("kind", "chord", "error", object("code", "nope", "message", "m")))
		requireErrorContains(t, err, "Invalid ChordError JSON")
		_, err = link.ErrorFromWire(delta.JsonObjectOf("kind", "nope"))
		if err == nil {
			t.Fatal("an unknown wire error kind was accepted")
		}
	})
}
