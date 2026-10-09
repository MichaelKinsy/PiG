package env

import (
	"context"
	"errors"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// pi: packages/env/src/errors.ts

// toFileError (errors.ts:10-31) maps a daemon error code onto a FileError code, keeps a FileError as it is, wraps anything else as
// "unknown" with the fallback path, and prefers the error's own path over the fallback.
func TestToFileErrorMapsCodesLikePi(t *testing.T) {
	codes := map[string]durableenv.FileErrorCode{
		"aborted":     durableenv.FileErrorAborted,
		"ENOENT":      durableenv.FileErrorNotFound,
		"EACCES":      durableenv.FileErrorPermissionDenied,
		"EPERM":       durableenv.FileErrorPermissionDenied,
		"ENOTDIR":     durableenv.FileErrorNotDirectory,
		"EISDIR":      durableenv.FileErrorIsDirectory,
		"EINVAL":      durableenv.FileErrorInvalid,
		"SYMLINK":     durableenv.FileErrorInvalid,
		"NOT_REGULAR": durableenv.FileErrorInvalid,
		"EOTHER":      durableenv.FileErrorUnknown,
	}
	for code, want := range codes {
		remote := &RemoteError{Code: code, Message: "m-" + code, Path: "/own"}
		got := toFileError(remote, "/fallback")
		if got.Code != want || got.Message != "m-"+code || got.Path != "/own" || !errors.Is(got, remote) {
			t.Errorf("%s: got {%s %q %q cause=%v}, want {%s %q /own cause=remote}", code, got.Code, got.Message, got.Path, got.Cause, want, "m-"+code)
		}
	}
	if got := toFileError(&RemoteError{Code: "ENOENT", Message: "gone"}, "/fallback"); got.Path != "/fallback" {
		t.Errorf("path = %q, want the fallback when the error has none", got.Path)
	}

	existing := durableenv.NewFileError(durableenv.FileErrorInvalid, "kept", "/p", nil)
	if got := toFileError(existing, "/fallback"); got != existing {
		t.Errorf("a FileError must be returned unchanged, got %+v", got)
	}

	cause := errors.New("boom")
	got := toFileError(cause, "/fallback")
	if got.Code != durableenv.FileErrorUnknown || got.Message != "boom" || got.Path != "/fallback" || !errors.Is(got.Cause, cause) {
		t.Errorf("plain error = %+v, want unknown/boom//fallback with the cause", got)
	}
}

// abortResult (errors.ts:4-6): an aborted signal is an "aborted" FileError carrying the path, a live one is no error.
func TestAbortErrorMatchesPi(t *testing.T) {
	if err := abortError(context.Background(), "/p"); err != nil {
		t.Fatalf("live context: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := abortError(ctx, "/p")
	var fileError *durableenv.FileError
	ok := errors.As(err, &fileError)
	if !ok || fileError.Code != durableenv.FileErrorAborted || fileError.Message != "aborted" || fileError.Path != "/p" {
		t.Fatalf("aborted context: %+v", err)
	}
}
