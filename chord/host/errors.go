// Package host ports the Chord host runtime of Pi's @earendil-works/chord root export (packages/chord/src): contracts,
// hosts, objects, cells, roots, plugins, hot reload and links. The shared base (Context, JSON, replicated state) is
// the parent package chord, which the durable core links without the runtime.
package host

// Ports packages/chord/src/errors.ts.

import "fmt"

// ChordErrorCode is a code callers see. A refused or aborted reload keeps the code of its cause.
type ChordErrorCode string

const (
	CodeAborted          ChordErrorCode = "aborted"
	CodeContractMismatch ChordErrorCode = "contract_mismatch"
	CodeForbidden        ChordErrorCode = "forbidden"
	CodeGone             ChordErrorCode = "gone"
	CodeInvalid          ChordErrorCode = "invalid"
	CodeNotInVariant     ChordErrorCode = "not_in_variant"
	CodeNotReady         ChordErrorCode = "not_ready"
	CodeReleased         ChordErrorCode = "released"
	CodeSuperseded       ChordErrorCode = "superseded"
	CodeUnavailable      ChordErrorCode = "unavailable"
	CodeUpgradeConflict  ChordErrorCode = "upgrade_conflict"
	CodeUpgradeRequired  ChordErrorCode = "upgrade_required"
)

// chordErrorCodes is errors.ts CHORD_ERROR_CODES: every ChordErrorCode, in declaration order. A wire error's code is
// one of them (wire.go frameSchema).
var chordErrorCodes = []ChordErrorCode{
	CodeAborted, CodeContractMismatch, CodeForbidden, CodeGone, CodeInvalid, CodeNotInVariant, CodeNotReady,
	CodeReleased, CodeSuperseded, CodeUnavailable, CodeUpgradeConflict, CodeUpgradeRequired,
}

// ChordError is a Chord failure: a code, an optional finer reason (for logs and tests: invalid + args, result, state,
// input, implementation, duplicate, cycle, read_only, not_host_lifetime, registry_conflict; unavailable + unknown_type), and whether
// a new root may retry it.
type ChordError struct {
	// Name is the error class name, "ChordError" (errors.ts:38 this.name).
	Name string
	Code ChordErrorCode
	// Reason is empty when the error has none.
	Reason    string
	Retryable bool
	Details   any
	// Message is the full message: "<code>[/<reason>]: <message>".
	Message string
	Cause   error
}

func (err *ChordError) Error() string { return err.Message }
func (err *ChordError) Unwrap() error { return err.Cause }

// ChordErrorOptions are the optional parts of a ChordError.
type ChordErrorOptions struct {
	Cause   error
	Details any
	Reason  string
}

// NewChordError is errors.ts new ChordError(code, message, options).
func NewChordError(code ChordErrorCode, message string, options ChordErrorOptions) *ChordError {
	prefix := string(code)
	if options.Reason != "" {
		prefix += "/" + options.Reason
	}
	return &ChordError{
		Name:      "ChordError",
		Code:      code,
		Reason:    options.Reason,
		Retryable: code == CodeSuperseded || code == CodeUpgradeConflict,
		Details:   options.Details,
		Message:   prefix + ": " + message,
		Cause:     options.Cause,
	}
}

// IsChordError is errors.ts isChordError(error, code?): whether err is a ChordError, of code when one is given. As
// upstream's instanceof, it does not look through a wrapper (an AggregateError, a %w wrap).
func IsChordError(err error, code ...ChordErrorCode) bool {
	chordError, ok := err.(*ChordError) //nolint:errorlint // Pi's instanceof does not unwrap
	return ok && chordError != nil && (len(code) == 0 || chordError.Code == code[0])
}

// Forbidden is errors.ts forbidden(message = "forbidden"), for factories and methods refusing a principal. The default
// applies only when message is omitted, as upstream's default parameter does.
func Forbidden(message ...string) *ChordError {
	text := "forbidden"
	if len(message) > 0 {
		text = message[0]
	}
	return NewChordError(CodeForbidden, text, ChordErrorOptions{})
}

// Invalid is errors.ts invalid(reason, message, details).
func Invalid(reason, message string, details any) *ChordError {
	return NewChordError(CodeInvalid, message, ChordErrorOptions{Reason: reason, Details: details})
}

// asChordError is errors.ts asChordError: it keeps a ChordError (the same error); anything else becomes unavailable,
// "<context>: <message>", with the original as cause. As upstream's instanceof, it does not look through a wrapper.
func asChordError(err error, context string) *ChordError {
	if chordError, ok := err.(*ChordError); ok && chordError != nil { //nolint:errorlint // Pi's instanceof does not unwrap
		return chordError
	}
	return NewChordError(CodeUnavailable, context+": "+err.Error(), ChordErrorOptions{Cause: err})
}

// AggregateError is JavaScript's AggregateError(errors, message), which runAll throws when several steps failed:
// Error() is the message alone, and the errors are reachable through Unwrap. Its shape is chord2-port S1's
// chord.AggregateError (chord/state.go), so the two merge into one when S1 lands.
type AggregateError struct {
	Message string
	Errors  []error
}

func (err *AggregateError) Error() string   { return err.Message }
func (err *AggregateError) Unwrap() []error { return err.Errors }

// runAll is errors.ts runAll: it runs every step in order, then returns nil, the one error, or an AggregateError of
// every error in order. A step that panics fails with the panic value, as a throwing step does upstream.
func runAll(steps []func() error, message string) error {
	var failures []error
	for _, step := range steps {
		if err := runStep(step); err != nil {
			failures = append(failures, err)
		}
	}
	switch len(failures) {
	case 0:
		return nil
	case 1:
		return failures[0]
	default:
		return &AggregateError{Message: message, Errors: failures}
	}
}

// runStep calls step and returns its error, or its panic value as an error.
func runStep(step func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if failure, ok := recovered.(error); ok {
				err = failure
			} else {
				err = fmt.Errorf("%v", recovered)
			}
		}
	}()
	return step()
}
