// Package data ports packages/chord/src/next/data: Chord's errors and plugin errors, principals, per-call checks of
// params and arguments, and how a plugin's open reports a broken configuration.
package data

import (
	"errors"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next/data/errors.ts.

// ProtocolVersion is the version of the error table below on links; adding or moving a code or reason bumps it.
const ProtocolVersion = 1

// ChordErrorCode is one of the codes of ChordErrorReasons.
type ChordErrorCode string

// The codes of ChordErrorReasons, as their wire spellings.
const (
	CodeInvalid          ChordErrorCode = "invalid"
	CodeContractMismatch ChordErrorCode = "contract_mismatch"
	CodeForbidden        ChordErrorCode = "forbidden"
	CodeGone             ChordErrorCode = "gone"
	CodeNotReady         ChordErrorCode = "not_ready"
	CodeReleased         ChordErrorCode = "released"
	CodeTooLarge         ChordErrorCode = "too_large"
	CodeUnavailable      ChordErrorCode = "unavailable"
	CodeAborted          ChordErrorCode = "aborted"
)

// ChordErrorReason is a reason a code may carry; the empty reason is upstream's undefined.
type ChordErrorReason string

// The reasons of ChordErrorReasons, as their wire spellings.
const (
	ReasonArgs          ChordErrorReason = "args"
	ReasonParams        ChordErrorReason = "params"
	ReasonMember        ChordErrorReason = "member"
	ReasonHostAddress   ChordErrorReason = "host_address"
	ReasonReentrant     ChordErrorReason = "reentrant"
	ReasonReadAfterBulk ChordErrorReason = "read_after_bulk"
	ReasonCycle         ChordErrorReason = "cycle"
	ReasonUpdating      ChordErrorReason = "updating"
	ReasonRevoked       ChordErrorReason = "revoked"
	ReasonEnded         ChordErrorReason = "ended"
	ReasonClosed        ChordErrorReason = "closed"
	ReasonSettled       ChordErrorReason = "settled"
	ReasonVersion       ChordErrorReason = "version"
	ReasonDiscarded     ChordErrorReason = "discarded"
	ReasonPart          ChordErrorReason = "part"
	ReasonLocked        ChordErrorReason = "locked"
	ReasonParkTimeout   ChordErrorReason = "park_timeout"
	ReasonBusy          ChordErrorReason = "busy"
	ReasonStorageFailed ChordErrorReason = "storage_failed"
	ReasonUndeclared    ChordErrorReason = "undeclared"
)

// chordErrorCodes lists every code in the order of upstream's CHORD_ERROR_REASONS.
var chordErrorCodes = []ChordErrorCode{
	CodeInvalid, CodeContractMismatch, CodeForbidden, CodeGone, CodeNotReady, CodeReleased, CodeTooLarge, CodeUnavailable, CodeAborted,
}

// ChordErrorCodes returns every code in the order of upstream's CHORD_ERROR_REASONS.
func ChordErrorCodes() []ChordErrorCode { return slices.Clone(chordErrorCodes) }

// ChordErrorReasons is upstream's CHORD_ERROR_REASONS: the reasons a code may carry. `invalid` requires a reason; every
// other code may omit it. The second result reports whether code is known.
func ChordErrorReasons(code ChordErrorCode) ([]ChordErrorReason, bool) {
	switch code {
	case CodeInvalid:
		return []ChordErrorReason{ReasonArgs, ReasonParams, ReasonMember, ReasonHostAddress, ReasonReentrant, ReasonReadAfterBulk, ReasonCycle}, true
	case CodeContractMismatch:
		return []ChordErrorReason{ReasonUpdating}, true
	case CodeForbidden:
		return []ChordErrorReason{ReasonRevoked}, true
	case CodeGone, CodeNotReady, CodeAborted:
		return []ChordErrorReason{}, true
	case CodeReleased:
		return []ChordErrorReason{ReasonEnded, ReasonClosed, ReasonSettled, ReasonVersion, ReasonDiscarded}, true
	case CodeTooLarge:
		return []ChordErrorReason{ReasonPart}, true
	case CodeUnavailable:
		return []ChordErrorReason{ReasonLocked, ReasonParkTimeout, ReasonBusy, ReasonStorageFailed, ReasonUndeclared}, true
	}
	return nil, false
}

// ChordErrorInit is the wire form of a ChordError and its constructor argument. Upstream's tagged union makes a wrong
// code/reason pairing a type error; Go checks the pairing in ChordErrorFromJson only, as upstream's runtime does.
type ChordErrorInit struct {
	Code    ChordErrorCode
	Reason  ChordErrorReason
	Message string
	// Details is the JSON details; HasDetails distinguishes an absent details from a JSON null.
	Details    delta.JsonValue
	HasDetails bool
}

// ChordError is a Chord failure. Cause is upstream's `cause` option; stack and cause never cross a link.
type ChordError struct {
	Code       ChordErrorCode
	Reason     ChordErrorReason
	Message    string
	Details    delta.JsonValue
	HasDetails bool
	Cause      error
}

// NewChordError creates a ChordError: NewChordError(ChordErrorInit{Code: CodeInvalid, Reason: ReasonArgs, Message: m}, nil).
func NewChordError(init ChordErrorInit, cause error) *ChordError {
	return &ChordError{Code: init.Code, Reason: init.Reason, Message: init.Message, Details: init.Details, HasDetails: init.HasDetails, Cause: cause}
}

// Error returns the message.
func (err *ChordError) Error() string { return err.Message }

// Unwrap returns the cause.
func (err *ChordError) Unwrap() error { return err.Cause }

// ToJSON is the wire form: code, reason, message, details in that order, an absent reason or details left out.
func (err *ChordError) ToJSON() *delta.JsonObject {
	object := delta.NewJsonObject(4)
	object.Set("code", string(err.Code))
	if err.Reason != "" {
		object.Set("reason", string(err.Reason))
	}
	object.Set("message", err.Message)
	if err.HasDetails {
		object.Set("details", err.Details)
	}
	return object
}

// MarshalJSON is JSON.stringify's call of toJSON: the wire form, so a ChordError marshals without stack or cause.
func (err *ChordError) MarshalJSON() ([]byte, error) { return err.ToJSON().MarshalJSON() }

// IsChordError reports whether err is a ChordError and returns it. It is upstream's `instanceof`: an error that wraps
// a ChordError (with %w, or as Broken's cause) is not one; AsChordError and OpenFailure read the causes they own.
func IsChordError(err error) (*ChordError, bool) {
	chordErr, ok := err.(*ChordError) //nolint:errorlint // upstream's instanceof: a wrapper of a ChordError is not one
	return chordErr, ok && chordErr != nil
}

// IsChordErrorCode is IsChordError with the given code.
func IsChordErrorCode(err error, code ChordErrorCode) (*ChordError, bool) {
	chordErr, ok := IsChordError(err)
	if !ok || chordErr.Code != code {
		return nil, false
	}
	return chordErr, true
}

// PluginError is an error a plugin returns for its callers: its own Code, a message and JSON Details. Only these cross a
// link (never a stack or a cause); a consumer returns it as a PluginError.
type PluginError struct {
	Code       string
	Message    string
	Details    delta.JsonValue
	HasDetails bool
}

// NewPluginError creates a PluginError; details is optional.
func NewPluginError(code, message string, details ...delta.JsonValue) *PluginError {
	err := &PluginError{Code: code, Message: message}
	if len(details) > 0 {
		err.Details, err.HasDetails = details[0], true
	}
	return err
}

// Error returns the message.
func (err *PluginError) Error() string { return err.Message }

// Invalid returns an `invalid` error with the given reason; details is optional.
func Invalid(reason ChordErrorReason, message string, details ...delta.JsonValue) *ChordError {
	init := ChordErrorInit{Code: CodeInvalid, Reason: reason, Message: message}
	if len(details) > 0 {
		init.Details, init.HasDetails = details[0], true
	}
	return NewChordError(init, nil)
}

// AsChordError is err itself when it is a ChordError, otherwise an `unavailable` error naming label, with err as cause.
func AsChordError(err error, label string) *ChordError {
	if chordErr, ok := IsChordError(err); ok {
		return chordErr
	}
	return NewChordError(ChordErrorInit{Code: CodeUnavailable, Message: label + ": " + err.Error()}, err)
}

var envelopeKeys = []string{"code", "reason", "message", "details"}

// ChordErrorFromJson rebuilds a ChordError from a link. It checks the envelope only: a known code, a reason allowed for
// it (required for `invalid`), a string message, no other keys. Details is kept by reference. raw is a
// *delta.JsonObject or a map[string]any.
func ChordErrorFromJson(raw any) (*ChordError, error) {
	var keys []string
	var get func(string) (any, bool)
	switch typed := raw.(type) {
	case *delta.JsonObject:
		keys = typed.Keys()
		get = typed.Get
	case map[string]any:
		for key := range typed {
			keys = append(keys, key)
		}
		slices.Sort(keys) // a map has no order; the first unknown key named is deterministic
		get = func(key string) (any, bool) { value, ok := typed[key]; return value, ok }
	default:
		return nil, envelopeError("not an object")
	}
	for _, key := range keys {
		if !slices.Contains(envelopeKeys, key) {
			return nil, envelopeError(fmt.Sprintf("unknown key %q", key))
		}
	}
	codeValue, _ := get("code")
	code, isString := codeValue.(string)
	reasons, known := ChordErrorReasons(ChordErrorCode(code))
	if !isString || !known {
		return nil, envelopeError("unknown code")
	}
	reasonValue, hasReason := get("reason")
	reason, reasonIsString := reasonValue.(string)
	if !hasReason {
		if ChordErrorCode(code) == CodeInvalid {
			return nil, envelopeError("reason not allowed for " + code)
		}
	} else if !reasonIsString || !slices.Contains(reasons, ChordErrorReason(reason)) {
		return nil, envelopeError("reason not allowed for " + code)
	}
	messageValue, _ := get("message")
	message, ok := messageValue.(string)
	if !ok {
		return nil, envelopeError("message is not a string")
	}
	details, hasDetails := get("details")
	return NewChordError(ChordErrorInit{Code: ChordErrorCode(code), Reason: ChordErrorReason(reason), Message: message, Details: details, HasDetails: hasDetails}, nil), nil
}

func envelopeError(problem string) error { return errors.New("Invalid ChordError JSON: " + problem) }
