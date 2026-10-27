package host

// pi: packages/chord/src/errors.ts
// Pi's errors.ts run under Node 24 on the inputs in testdata/pi_errors.json (testdata/pi_errors.mjs regenerates it from
// the pinned mirror): ChordError, isChordError, forbidden, invalid, asChordError and runAll. Each case carries its
// inputs and what Pi returned; the Go code gets the same inputs.

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
)

type piErrorView struct {
	Name      string  `json:"name"`
	Message   string  `json:"message"`
	Code      string  `json:"code"`
	Reason    *string `json:"reason"`
	Retryable bool    `json:"retryable"`
	Details   any     `json:"details"`
	Cause     *string `json:"cause"`
}

type piErrorsOracle struct {
	Codes     []string `json:"codes"`
	Construct []struct {
		Code    string      `json:"code"`
		Message string      `json:"message"`
		Reason  *string     `json:"reason"`
		Details any         `json:"details"`
		Cause   *string     `json:"cause"`
		Expect  piErrorView `json:"expect"`
	} `json:"construct"`
	Is []struct {
		Kind   string `json:"kind"`
		Code   string `json:"code"`
		Checks []struct {
			Check  *string `json:"check"`
			Result bool    `json:"result"`
		} `json:"checks"`
	} `json:"is"`
	Helpers []struct {
		Fn     string      `json:"fn"`
		Args   []any       `json:"args"`
		Expect piErrorView `json:"expect"`
	} `json:"helpers"`
	As []struct {
		Kind    string       `json:"kind"`
		Context string       `json:"context"`
		Message string       `json:"message"`
		Same    bool         `json:"same"`
		Expect  *piErrorView `json:"expect"`
	} `json:"as"`
	Runs []struct {
		Steps []struct {
			Throw *string `json:"throw"`
		} `json:"steps"`
		Ran     []int `json:"ran"`
		Outcome struct {
			Result  string   `json:"result"`
			Message string   `json:"message"`
			Errors  []string `json:"errors"`
		} `json:"outcome"`
	} `json:"runs"`
}

func loadPiErrorsOracle(t *testing.T) piErrorsOracle {
	t.Helper()
	data, err := os.ReadFile("testdata/pi_errors.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle piErrorsOracle
	if err := json.Unmarshal(data, &oracle); err != nil {
		t.Fatal(err)
	}
	return oracle
}

// checkPiError compares a ChordError with Pi's view of the same error; Pi's absent and empty reason both read as "".
func checkPiError(t *testing.T, label string, got *ChordError, want piErrorView) {
	t.Helper()
	wantReason := ""
	if want.Reason != nil {
		wantReason = *want.Reason
	}
	if got.Name != want.Name || got.Message != want.Message || string(got.Code) != want.Code || got.Reason != wantReason || got.Retryable != want.Retryable {
		t.Errorf("%s: got name=%q message=%q code=%s reason=%q retryable=%v, Pi name=%q message=%q code=%s reason=%q retryable=%v",
			label, got.Name, got.Message, got.Code, got.Reason, got.Retryable, want.Name, want.Message, want.Code, wantReason, want.Retryable)
	}
	if !reflect.DeepEqual(got.Details, want.Details) {
		t.Errorf("%s: details %v, Pi %v", label, got.Details, want.Details)
	}
	if (got.Cause == nil) != (want.Cause == nil) || (got.Cause != nil && got.Cause.Error() != *want.Cause) {
		t.Errorf("%s: cause %v, Pi %v", label, got.Cause, want.Cause)
	}
}

func TestChordErrorMatchesPi(t *testing.T) {
	oracle := loadPiErrorsOracle(t)
	if len(oracle.Construct) < 40 {
		t.Fatalf("Pi oracle has %d constructor cases", len(oracle.Construct))
	}
	codes := make([]string, len(chordErrorCodes))
	for index, code := range chordErrorCodes {
		codes[index] = string(code)
	}
	if !reflect.DeepEqual(codes, oracle.Codes) {
		t.Fatalf("error codes %v, Pi %v", codes, oracle.Codes)
	}
	for _, item := range oracle.Construct {
		options := ChordErrorOptions{Details: item.Details}
		if item.Reason != nil {
			options.Reason = *item.Reason
		}
		if item.Cause != nil {
			options.Cause = errors.New(*item.Cause)
		}
		checkPiError(t, item.Code+"/"+item.Message, NewChordError(ChordErrorCode(item.Code), item.Message, options), item.Expect)
	}
	for _, item := range oracle.Helpers {
		var got *ChordError
		switch item.Fn {
		case "forbidden":
			args := make([]string, len(item.Args))
			for index, arg := range item.Args {
				args[index], _ = arg.(string)
			}
			got = Forbidden(args...)
		case "invalid":
			got = Invalid(item.Args[0].(string), item.Args[1].(string), item.Args[2])
		default:
			t.Fatalf("unknown helper %s", item.Fn)
		}
		checkPiError(t, item.Fn, got, item.Expect)
	}
}

func TestIsChordErrorMatchesPi(t *testing.T) {
	oracle := loadPiErrorsOracle(t)
	for _, item := range oracle.Is {
		var err error
		switch item.Kind {
		case "chord":
			err = NewChordError(ChordErrorCode(item.Code), "x", ChordErrorOptions{})
		case "plain":
			err = errors.New("plain")
		case "aggregate":
			gone := NewChordError(CodeGone, "g", ChordErrorOptions{})
			err = runAll([]func() error{func() error { return gone }, func() error { return gone }}, "two")
		default:
			t.Fatalf("unknown kind %s", item.Kind)
		}
		for _, check := range item.Checks {
			var got bool
			if check.Check == nil {
				got = IsChordError(err)
			} else {
				got = IsChordError(err, ChordErrorCode(*check.Check))
			}
			if got != check.Result {
				t.Errorf("isChordError(%s %s, %v) = %v, Pi %v", item.Kind, item.Code, check.Check, got, check.Result)
			}
		}
	}
}

func TestAsChordErrorMatchesPi(t *testing.T) {
	oracle := loadPiErrorsOracle(t)
	for _, item := range oracle.As {
		switch item.Kind {
		case "chord":
			own := NewChordError(CodeGone, item.Message, ChordErrorOptions{Reason: "r"})
			if got := asChordError(own, item.Context); (got == own) != item.Same {
				t.Errorf("asChordError(ChordError) kept the error = %v, Pi %v", got == own, item.Same)
			}
		case "plain":
			cause := errors.New(item.Message)
			got := asChordError(cause, item.Context)
			checkPiError(t, "asChordError(plain "+item.Message+")", got, *item.Expect)
			if got.Cause != cause { //nolint:errorlint // identity: the original error is the cause
				t.Errorf("asChordError cause = %v, want the original error", got.Cause)
			}
		case "wrapped":
			// Pi's `instanceof` does not look into a cause: an Error whose cause is a ChordError is wrapped like any other.
			wrapped := &wrapError{message: item.Message, cause: NewChordError(CodeGone, "g", ChordErrorOptions{Reason: "r"})}
			got := asChordError(wrapped, item.Context)
			want := *item.Expect
			want.Cause = nil
			got.Cause = nil
			checkPiError(t, "asChordError(wrapped)", got, want)
		default:
			t.Fatalf("unknown kind %s", item.Kind)
		}
	}
}

type wrapError struct {
	message string
	cause   error
}

func (err *wrapError) Error() string { return err.message }
func (err *wrapError) Unwrap() error { return err.cause }

func TestRunAllMatchesPi(t *testing.T) {
	oracle := loadPiErrorsOracle(t)
	for index, item := range oracle.Runs {
		var ran []int
		steps := make([]func() error, len(item.Steps))
		for position, step := range item.Steps {
			steps[position] = func() error {
				ran = append(ran, position)
				if step.Throw != nil {
					return errors.New(*step.Throw)
				}
				return nil
			}
		}
		err := runAll(steps, "several failed")
		if !reflect.DeepEqual(append([]int{}, ran...), append([]int{}, item.Ran...)) {
			t.Errorf("program %d ran steps %v, Pi %v", index, ran, item.Ran)
		}
		switch item.Outcome.Result {
		case "ok":
			if err != nil {
				t.Errorf("program %d failed with %v, Pi succeeded", index, err)
			}
		case "error":
			var aggregate *AggregateError
			if err == nil || errors.As(err, &aggregate) || err.Error() != item.Outcome.Message {
				t.Errorf("program %d = %v, Pi threw the one error %q", index, err, item.Outcome.Message)
			}
		case "aggregate":
			var aggregate *AggregateError
			if !errors.As(err, &aggregate) || aggregate.Message != item.Outcome.Message || len(aggregate.Errors) != len(item.Outcome.Errors) {
				t.Errorf("program %d = %v, Pi threw an AggregateError %q of %v", index, err, item.Outcome.Message, item.Outcome.Errors)
				continue
			}
			for position, inner := range aggregate.Errors {
				if inner.Error() != item.Outcome.Errors[position] {
					t.Errorf("program %d error %d = %q, Pi %q", index, position, inner.Error(), item.Outcome.Errors[position])
				}
			}
		}
	}
}
