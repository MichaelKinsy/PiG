package ai

import (
	"errors"
	"fmt"
	"testing"
)

type codedTestError struct {
	message string
	code    any
}

func (e *codedTestError) Error() string       { return e.message }
func (e *codedTestError) DiagnosticCode() any { return e.code }

// packages/ai/src/utils/diagnostics.ts formatThrownValue. Expected values come from the pinned 1.0.4 implementation under Node; Go has no null/undefined distinction, so nil is `undefined`.
func TestFormatThrownValueMatchesUpstream(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"error with message", errors.New("boom"), "boom"},
		{"error without message falls back to its name", &codedTestError{}, "Error"},
		{"string", "text", "text"},
		{"number", 42, "42"},
		{"nil", nil, "undefined"},
	} {
		if got := FormatThrownValue(tc.value); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// packages/ai/src/utils/diagnostics.ts extractDiagnosticError: a non-error is a ThrownValue; an error keeps its name and message, and only a string or numeric code survives.
// packages/ai/src/utils/diagnostics.ts:6,29 (DiagnosticErrorInfo.stack).
func TestExtractDiagnosticErrorMatchesUpstream(t *testing.T) {
	if got := ExtractDiagnosticError(&codedTestError{message: "m", code: 7}); got.Name != "Error" || got.Message != "m" || got.Code != 7 || got.Stack != "" {
		t.Errorf("numeric code = %+v", got)
	}
	if got := ExtractDiagnosticError(&codedTestError{message: "m", code: "E1"}); got.Code != "E1" {
		t.Errorf("string code = %+v", got)
	}
	if got := ExtractDiagnosticError(&codedTestError{message: "m", code: struct{}{}}); got.Code != nil {
		t.Errorf("object code kept: %+v", got)
	}
	if got := ExtractDiagnosticError(errors.New("plain error")); got.Code != nil || got.Message != "plain error" {
		t.Errorf("uncoded error = %+v", got)
	}
	if got := ExtractDiagnosticError(&codedTestError{message: "m", code: int32(7)}); got.Code != int32(7) {
		t.Errorf("int32 code dropped: %+v", got)
	}
	if got := ExtractDiagnosticError(&codedTestError{message: "m", code: true}); got.Code != nil {
		t.Errorf("boolean code kept: %+v", got)
	}
	if got := ExtractDiagnosticError("plain"); got != (DiagnosticErrorInfo{Name: "ThrownValue", Message: "plain"}) {
		t.Errorf("string = %+v", got)
	}
	if got := ExtractDiagnosticError(5); got != (DiagnosticErrorInfo{Name: "ThrownValue", Message: "5"}) {
		t.Errorf("number = %+v", got)
	}
}

// packages/ai/src/utils/diagnostics.ts createAssistantMessageDiagnostic and appendAssistantMessageDiagnostic: the appended list is a new slice, so a list shared with another message is not written through.
func TestCreateAndAppendAssistantMessageDiagnosticMatchUpstream(t *testing.T) {
	diagnostic := CreateAssistantMessageDiagnostic("kind", &codedTestError{message: "m", code: "E"}, JsonObject{"d": 1})
	if diagnostic.Type != "kind" || diagnostic.Timestamp == 0 || diagnostic.Error == nil || diagnostic.Error.Code != "E" || diagnostic.Details["d"] != 1 {
		t.Fatalf("diagnostic = %+v", diagnostic)
	}
	shared := make([]AssistantMessageDiagnostic, 1, 4)
	message := &AssistantMessage{Diagnostics: shared}
	AppendAssistantMessageDiagnostic(message, diagnostic)
	if len(message.Diagnostics) != 2 || len(shared) != 1 || shared[:2][1].Type != "" {
		t.Errorf("append wrote through the shared backing array: %+v / %+v", message.Diagnostics, shared[:2])
	}
	empty := &AssistantMessage{}
	AppendAssistantMessageDiagnostic(empty, diagnostic)
	if len(empty.Diagnostics) != 1 {
		t.Errorf("append to empty = %+v", empty.Diagnostics)
	}
}

// Expected values from the pinned 1.0.4 dist under Node: `class Coded extends Error {}` reports name "Error" (the class name is not the name), an error with an empty message reports "Error" as its message, a boolean code is dropped, and `this.name = ""` omits the name.
func TestExtractDiagnosticErrorNameIsErrorUnlessAssigned(t *testing.T) {
	for _, err := range []error{errors.New("plain"), fmt.Errorf("wrapped: %w", errors.New("inner")), &codedTestError{message: "m"}} {
		if got := ExtractDiagnosticError(err); got.Name != "Error" {
			t.Errorf("%T name = %q, want Error", err, got.Name)
		}
	}
	if got := ExtractDiagnosticError(&codedTestError{code: "E"}); got != (DiagnosticErrorInfo{Name: "Error", Message: "Error", Code: "E"}) {
		t.Errorf("empty message = %+v", got)
	}
}

type emptyNameTestError struct{}

func (emptyNameTestError) Error() string { return "x" }
func (emptyNameTestError) Name() string  { return "" }

type namedTestError struct{}

func (namedTestError) Error() string { return "named" }
func (namedTestError) Name() string  { return "CustomName" }

// packages/ai/src/utils/diagnostics.ts extractDiagnosticError reports `error.name`; a Go error states it with a Name method. ModelsError and PiMessagesResponseError set `this.name` in their constructors.
func TestErrorNameFollowsTheNameMethod(t *testing.T) {
	if got := ExtractDiagnosticError(namedTestError{}); got.Name != "CustomName" {
		t.Errorf("name = %q", got.Name)
	}
	if got := ExtractDiagnosticError(emptyNameTestError{}); got != (DiagnosticErrorInfo{Message: "x"}) {
		t.Errorf("empty name = %+v", got)
	}
	if got := FormatThrownValue(struct{ error }{namedTestError{}}); got != "named" {
		t.Errorf("message = %q", got)
	}
	if got := (&ModelsError{}).Name(); got != "ModelsError" {
		t.Errorf("ModelsError name = %q", got)
	}
	if got := (&PiMessagesResponseError{}).Name(); got != "PiMessagesResponseError" {
		t.Errorf("PiMessagesResponseError name = %q", got)
	}
	if got := ExtractDiagnosticError(NewModelsError(ModelsErrorAuth, "failed", errors.New("cause"))); got.Name != "ModelsError" || got.Message != "failed: cause" {
		t.Errorf("models error = %+v", got)
	}
}

// upstream: pi-messages.ts PiMessagesResponseError(message, code, diagnosticDetails) stores all three; an empty code reads as undefined.
func TestNewPiMessagesResponseErrorStoresItsArguments(t *testing.T) {
	err := NewPiMessagesResponseError("boom", "rate_limited", map[string]any{"k": "v"})
	if err.Error() != "boom" || err.DiagnosticCode() != "rate_limited" || err.DiagnosticDetails["k"] != "v" || err.Name() != "PiMessagesResponseError" {
		t.Fatalf("NewPiMessagesResponseError = %+v", err)
	}
	if NewPiMessagesResponseError("boom", "", nil).DiagnosticCode() != nil {
		t.Fatal("an empty code must read as undefined")
	}
}
