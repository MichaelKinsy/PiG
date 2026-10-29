package link

import (
	"errors"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/link/errors.ts. Errors on the wire: a ChordError's JSON or a plugin's code, message
// and details; never a stack or a cause.

// WireError is an error as it crosses a link: `{kind: "chord", error}` (a ChordError's JSON) or
// `{kind: "plugin", code, message, details?}`, as the JSON object the codecs write. CheckMessage checks its shape.
type WireError = *delta.JsonObject

// WireErrorOf is what crosses for an error: a ChordError, a PluginError, or anything else as the plugin code
// `internal`.
func WireErrorOf(err error) WireError {
	if chordErr, ok := data.IsChordError(err); ok {
		return delta.JsonObjectOf("kind", "chord", "error", chordErr.ToJSON())
	}
	if plugin, ok := err.(*data.PluginError); ok { //nolint:errorlint // upstream's instanceof
		wire := delta.JsonObjectOf("kind", "plugin", "code", plugin.Code, "message", plugin.Message)
		if plugin.HasDetails {
			wire.Set("details", plugin.Details)
		}
		return wire
	}
	return delta.JsonObjectOf("kind", "plugin", "code", "internal", "message", err.Error())
}

// ErrorFromWire is the error a consumer returns: a *data.ChordError or a *data.PluginError. A malformed ChordError
// envelope is an error (a protocol error), returned as err.
func ErrorFromWire(wire WireError) (received error, err error) {
	kind, _ := wire.Value("kind").(string)
	if kind == "chord" {
		return data.ChordErrorFromJson(wire.Value("error"))
	}
	if kind != "plugin" {
		return nil, errors.New("wire error: an unknown kind")
	}
	code, _ := wire.Value("code").(string)
	message, _ := wire.Value("message").(string)
	// Fresh JSON from the link: details are JSON.
	if details, has := wire.Get("details"); has {
		return data.NewPluginError(code, message, details), nil
	}
	return data.NewPluginError(code, message), nil
}
