package data_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/data/errors.test.ts.
//
// Designed out (type level): the
// expectTypeOf assertions of "narrowing on code narrows reason" and "invalid builds an invalid error ...". Go's
// ChordErrorInit does not reject a wrong code/reason pairing at compile time; data.ChordErrorFromJson checks the
// pairing at runtime, as upstream's runtime does; "a wrong pairing is a type error" is ported as a runtime test in
// typelevel_test.go (unenforced by the Go compiler).

func TestChordError(t *testing.T) {
	t.Run("is a tagged union: constructing the valid pairings", func(t *testing.T) {
		for _, init := range []data.ChordErrorInit{
			{Code: data.CodeInvalid, Reason: data.ReasonArgs, Message: "m"},
			{Code: data.CodeUnavailable, Message: "m"},
			{Code: data.CodeUnavailable, Reason: data.ReasonLocked, Message: "m"},
		} {
			if err := data.NewChordError(init, nil); err.Code != init.Code || err.Reason != init.Reason {
				t.Errorf("NewChordError(%+v) = %+v", init, err)
			}
		}
	})

	t.Run("narrowing on code narrows reason", func(t *testing.T) {
		var err error = data.NewChordError(data.ChordErrorInit{Code: data.CodeTooLarge, Reason: data.ReasonPart, Message: "m"}, nil)
		chordErr, ok := data.IsChordError(err)
		if !ok || chordErr.Code != data.CodeTooLarge || chordErr.Reason != data.ReasonPart {
			t.Fatalf("IsChordError(%v) = %v, %v", err, chordErr, ok)
		}
		var target *data.ChordError
		if !errors.As(err, &target) || target != chordErr {
			t.Fatal("a ChordError is an error that errors.As finds")
		}
	})

	t.Run("round-trips through JSON without stack or cause", func(t *testing.T) {
		details := delta.JsonObjectOf("path", "/text")
		err := data.NewChordError(data.ChordErrorInit{
			Code: data.CodeInvalid, Reason: data.ReasonArgs, Message: "args/text: bad", Details: details, HasDetails: true,
		}, errors.New("x"))
		text, marshalErr := json.Marshal(err)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if got, want := string(text), `{"code":"invalid","reason":"args","message":"args/text: bad","details":{"path":"/text"}}`; got != want {
			t.Fatalf("JSON.stringify = %s, want %s", got, want)
		}
		parsed := nexttest.ParseJSON(t, string(text))
		back, fromErr := data.ChordErrorFromJson(parsed)
		if fromErr != nil {
			t.Fatal(fromErr)
		}
		nexttest.RequireJSONEqual(t, back.ToJSON(), parsed)
		gone := data.NewChordError(data.ChordErrorInit{Code: data.CodeGone, Message: "m"}, nil)
		nexttest.RequireJSONEqual(t, gone.ToJSON(), delta.JsonObjectOf("code", "gone", "message", "m"))
	})

	t.Run("keeps details by reference", func(t *testing.T) {
		details := delta.JsonObjectOf("large", []any{1.0, 2.0, 3.0})
		raw := delta.JsonObjectOf("code", "gone", "message", "m", "details", details)
		err, fromErr := data.ChordErrorFromJson(raw)
		if fromErr != nil {
			t.Fatal(fromErr)
		}
		if got, ok := err.Details.(*delta.JsonObject); !ok || got != details || !err.HasDetails {
			t.Fatalf("details = %v, want the same object", err.Details)
		}
	})

	refusals := []struct {
		name    string
		raw     any
		problem string
	}{
		{"not an object", "x", "not an object"},
		{"null", nil, "not an object"},
		{"an array", []any{}, "not an object"},
		{"unknown key", map[string]any{"code": "gone", "message": "m", "stack": "s"}, `unknown key "stack"`},
		{"unknown code", map[string]any{"code": "teapot", "message": "m"}, "unknown code"},
		{"inherited name as code", map[string]any{"code": "toString", "message": "m"}, "unknown code"},
		{"code not a string", map[string]any{"code": 1.0, "message": "m"}, "unknown code"},
		{"invalid without reason", map[string]any{"code": "invalid", "message": "m"}, "reason not allowed for invalid"},
		{"reason not allowed", map[string]any{"code": "gone", "reason": "args", "message": "m"}, "reason not allowed for gone"},
		{"reason of another code", map[string]any{"code": "unavailable", "reason": "part", "message": "m"}, "reason not allowed for unavailable"},
		{"message not a string", map[string]any{"code": "gone", "message": 1.0}, "message is not a string"},
	}
	for _, refusal := range refusals {
		t.Run("chordErrorFromJson refuses "+refusal.name, func(t *testing.T) {
			_, err := data.ChordErrorFromJson(refusal.raw)
			if want := "Invalid ChordError JSON: " + refusal.problem; err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("ChordErrorFromJson(%v) error = %v, want %q", refusal.raw, err, want)
			}
		})
	}
}

func TestProtocolTable(t *testing.T) {
	t.Run("is pinned together with PROTOCOL_VERSION", func(t *testing.T) {
		const pinned = `{"invalid":["args","params","member","host_address","reentrant","read_after_bulk","cycle"],` +
			`"contract_mismatch":["updating"],"forbidden":["revoked"],"gone":[],"not_ready":[],` +
			`"released":["ended","closed","settled","version","discarded"],"too_large":["part"],` +
			`"unavailable":["locked","park_timeout","busy","storage_failed","undeclared","version","reload_cycle"],"aborted":[]}`
		table := delta.NewJsonObject(9)
		for _, code := range data.ChordErrorCodes() {
			reasons, known := data.ChordErrorReasons(code)
			if !known {
				t.Fatalf("ChordErrorCodes lists %q but ChordErrorReasons does not know it", code)
			}
			names := make([]any, len(reasons))
			for index, reason := range reasons {
				names[index] = string(reason)
			}
			table.Set(string(code), names)
		}
		if data.ProtocolVersion != 1 || nexttest.JSONText(t, table) != pinned {
			t.Fatal("the error table changed: bump ProtocolVersion and update this pin")
		}
	})
}

// ChordErrorCode is Pi's union of nine string literals (CHORD_ERROR_REASONS' keys): the codes in order, each accepted with exactly its own reasons
// (and without one, but `invalid`), none with another code's reason, and no tenth.
func TestChordErrorCodeAllKindsAreTheNineCodesAndEachTakesOnlyItsOwnReasons(t *testing.T) {
	wire := []string{"invalid", "contract_mismatch", "forbidden", "gone", "not_ready", "released", "too_large", "unavailable", "aborted"}
	codes := data.ChordErrorCodes()
	if len(codes) != len(wire) {
		t.Fatalf("%d codes, want Pi's %d", len(codes), len(wire))
	}
	every := map[data.ChordErrorReason]bool{}
	for _, code := range codes {
		reasons, _ := data.ChordErrorReasons(code)
		for _, reason := range reasons {
			every[reason] = true
		}
	}
	for index, code := range codes {
		if string(code) != wire[index] {
			t.Errorf("code %d = %q, want %q", index, code, wire[index])
		}
		own, known := data.ChordErrorReasons(code)
		if !known {
			t.Errorf("%q is listed but unknown", code)
		}
		allowed := map[data.ChordErrorReason]bool{}
		for _, reason := range own {
			allowed[reason] = true
			if _, err := data.ChordErrorFromJson(map[string]any{"code": string(code), "reason": string(reason), "message": "m"}); err != nil {
				t.Errorf("%s/%s is refused: %v", code, reason, err)
			}
		}
		for reason := range every {
			if allowed[reason] {
				continue
			}
			if _, err := data.ChordErrorFromJson(map[string]any{"code": string(code), "reason": string(reason), "message": "m"}); err == nil {
				t.Errorf("%s/%s is accepted, but it is another code's reason", code, reason)
			}
		}
		_, err := data.ChordErrorFromJson(map[string]any{"code": string(code), "message": "m"})
		if (err == nil) == (code == data.CodeInvalid) {
			t.Errorf("%s without a reason: err = %v; only `invalid` requires one", code, err)
		}
	}
	if _, known := data.ChordErrorReasons(data.ChordErrorCode("unheard_of")); known {
		t.Error("a tenth code is known")
	}
	if _, err := data.ChordErrorFromJson(map[string]any{"code": "unheard_of", "message": "m"}); err == nil {
		t.Error("an unknown code is accepted from the wire")
	}
}

func TestErrorHelpers(t *testing.T) {
	t.Run("isChordError checks the class and optionally the code", func(t *testing.T) {
		err := data.Invalid(data.ReasonMember, "no method")
		if _, ok := data.IsChordError(err); !ok {
			t.Error("IsChordError(invalid) = false")
		}
		if _, ok := data.IsChordErrorCode(err, data.CodeInvalid); !ok {
			t.Error("IsChordErrorCode(invalid, invalid) = false")
		}
		if _, ok := data.IsChordErrorCode(err, data.CodeGone); ok {
			t.Error("IsChordErrorCode(invalid, gone) = true")
		}
		if _, ok := data.IsChordError(errors.New("x")); ok {
			t.Error("IsChordError(plain error) = true")
		}
		// upstream's `instanceof`: an object that merely looks like a ChordError is not one, and neither is a
		// value wrapping one
		if _, ok := data.IsChordError(&data.PluginError{Code: "invalid", Message: "m"}); ok {
			t.Error("IsChordError(PluginError) = true")
		}
		if _, ok := data.IsChordError(fmt.Errorf("wrapped: %w", err)); ok {
			t.Error("IsChordError(wrapper of a ChordError) = true")
		}
		if _, ok := data.IsChordError(data.Broken(err)); ok {
			t.Error("IsChordError(broken(ChordError)) = true")
		}
	})

	t.Run("invalid builds an invalid error with its reason and details", func(t *testing.T) {
		err := data.Invalid(data.ReasonParams, "params/a: missing", delta.JsonObjectOf("path", "/a"))
		nexttest.RequireJSONEqual(t, err.ToJSON(), delta.JsonObjectOf(
			"code", "invalid", "reason", "params", "message", "params/a: missing", "details", delta.JsonObjectOf("path", "/a"),
		))
		if err.Code != data.CodeInvalid {
			t.Fatalf("code = %q", err.Code)
		}
	})

	// Pi packages/chord/src/data/errors.ts:117 forbidden(message, details?): a `forbidden` error without a reason.
	t.Run("forbidden builds a forbidden error without a reason, with its details", func(t *testing.T) {
		err := data.Forbidden("not authorized", delta.JsonObjectOf("who", "x"))
		nexttest.RequireJSONEqual(t, err.ToJSON(), delta.JsonObjectOf(
			"code", "forbidden", "message", "not authorized", "details", delta.JsonObjectOf("who", "x"),
		))
		if _, ok := data.IsChordErrorCode(err, data.CodeForbidden); !ok || err.Reason != "" {
			t.Fatalf("forbidden = %+v", err)
		}
		bare := data.Forbidden("no details")
		if bare.HasDetails || bare.Message != "no details" {
			t.Fatalf("forbidden without details = %+v", bare)
		}
	})

	t.Run("asChordError keeps a ChordError and wraps anything else as unavailable", func(t *testing.T) {
		chord := data.Invalid(data.ReasonCycle, "c")
		if got := data.AsChordError(chord, "open"); got != chord {
			t.Fatal("AsChordError wrapped a ChordError")
		}
		foreign := errors.New("boom")
		wrapped := data.AsChordError(foreign, "open notes")
		nexttest.RequireJSONEqual(t, wrapped.ToJSON(), delta.JsonObjectOf("code", "unavailable", "message", "open notes: boom"))
		if !errors.Is(wrapped.Cause, foreign) {
			t.Fatal("the foreign error is not the cause")
		}
		if got := data.AsChordError(errors.New("text"), "open").Message; got != "open: text" {
			t.Fatalf("message = %q", got)
		}
	})
}

func TestPluginError(t *testing.T) {
	t.Run("carries a plugin's code, message and JSON details", func(t *testing.T) {
		details := delta.JsonObjectOf("limit", 3.0)
		err := data.NewPluginError("quota", "too many", details)
		var asError error = err
		if asError.Error() != "too many" || err.Code != "quota" || err.Message != "too many" || !err.HasDetails {
			t.Fatalf("PluginError = %+v", err)
		}
		nexttest.RequireJSONEqual(t, err.Details, details)
		if bare := data.NewPluginError("c", "m"); bare.HasDetails || bare.Details != nil {
			t.Fatalf("details = %v, want absent", bare.Details)
		}
	})
}
