package env

// pi: packages/durable/src/env/index.ts

import (
	"errors"
	"testing"
)

// toError keeps an Error, wraps a string, and otherwise uses JSON.stringify, which does not escape HTML characters
// (env/index.ts toError).
func TestToErrorMatchesJSONStringify(t *testing.T) {
	cause := errors.New("cause")
	if got := ToError(cause); !errors.Is(got, cause) || got.Error() != "cause" {
		t.Fatalf("error = %v", got)
	}
	for _, tc := range []struct {
		value any
		want  string
	}{
		{"plain <text>", "plain <text>"},
		{map[string]any{"a": "<b>&"}, `{"a":"<b>&"}`},
		{nil, "null"},
		{3.5, "3.5"},
	} {
		if got := ToError(tc.value).Error(); got != tc.want {
			t.Fatalf("ToError(%#v) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

// upstream: packages/durable/src/env/env.ts getOrThrow returns the value of a success and throws the failure of an error result.
func TestGetOrThrow(t *testing.T) {
	value, err := GetOrThrow(Ok[string, error]("contents"))
	if err != nil || value != "contents" {
		t.Fatalf("GetOrThrow(ok) = %q, %v", value, err)
	}
	failure := errors.New("not found")
	value, err = GetOrThrow(Err[string, error](failure))
	if !errors.Is(err, failure) || value != "" {
		t.Fatalf("GetOrThrow(err) = %q, %v; want the failure and the zero value", value, err)
	}
}

// env/index.ts:45-55,65-75: FileError(code, message, path?, cause?) and ExecutionError(code, message, cause?) keep every argument, name themselves
// "FileError" / "ExecutionError", and expose the cause; path and spillPath are absent unless set.
func TestFileErrorAndExecutionErrorKeepTheirArguments(t *testing.T) {
	cause := errors.New("EACCES")
	file := NewFileError(FileErrorPermissionDenied, "cannot read", "/tmp/a", cause)
	if file.Code != FileErrorPermissionDenied || file.Error() != "cannot read" || file.Path != "/tmp/a" || !errors.Is(file, cause) || file.Name() != "FileError" {
		t.Fatalf("FileError = %+v", file)
	}
	if bare := NewFileError(FileErrorNotFound, "missing", "", nil); bare.Path != "" || bare.Unwrap() != nil {
		t.Fatalf("bare FileError = %+v", bare)
	}
	execution := NewExecutionError(ExecutionErrorTimeout, "timed out", cause)
	execution.SpillPath = "/tmp/spill"
	if execution.Code != ExecutionErrorTimeout || execution.Error() != "timed out" || !errors.Is(execution, cause) || execution.Name() != "ExecutionError" || execution.SpillPath != "/tmp/spill" {
		t.Fatalf("ExecutionError = %+v", execution)
	}
	if bare := NewExecutionError(ExecutionErrorUnknown, "x", nil); bare.SpillPath != "" || bare.Unwrap() != nil {
		t.Fatalf("bare ExecutionError = %+v", bare)
	}
}

// env/index.ts:19-21 getOrUndefined(result): the value of a successful result, undefined (nil) for a failure.
func TestGetOrUndefinedReturnsTheValueOnlyForSuccess(t *testing.T) {
	value := FileInfo{Name: "a"}
	if got := GetOrUndefined(Ok[*FileInfo, *FileError](&value)); got != &value {
		t.Fatalf("ok result = %v, want the value", got)
	}
	if got := GetOrUndefined(Err[*FileInfo](NewFileError(FileErrorNotFound, "missing", "a", nil))); got != nil {
		t.Fatalf("failed result = %v, want nil", got)
	}
	// A failed result that still carries a value is undefined: only ok decides.
	if got := GetOrUndefined(Result[*FileInfo, *FileError]{Value: &value}); got != nil {
		t.Fatalf("not-ok result with a value = %v, want nil", got)
	}
}
