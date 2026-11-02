package env

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// pi: packages/env/src/errors.ts

// toFileError (errors.ts:10-31) maps a daemon error code onto a FileError code, keeps a FileError as it is, wraps anything else as
// "unknown" with the fallback path, and prefers the error's own path over the fallback.
func TestToFileErrorMapsCodesLikePi(t *testing.T) {
	t.Parallel()
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
		remote := &RemoteError{Code: code, Message: "m-" + code, Path: new("/own")}
		got := toFileError(remote, "/fallback")
		if got.Code != want || got.Message != "m-"+code || got.Path != "/own" || !errors.Is(got, remote) {
			t.Errorf("%s: got {%s %q %q cause=%v}, want {%s %q /own cause=remote}", code, got.Code, got.Message, got.Path, got.Cause, want, "m-"+code)
		}
	}
	if got := toFileError(&RemoteError{Code: "ENOENT", Message: "gone"}, "/fallback"); got.Path != "/fallback" {
		t.Errorf("path = %q, want the fallback when the error has none", got.Path)
	}
	// errors.ts:15 uses ??, so a daemon-reported empty path is kept rather than replaced by the fallback.
	if got := toFileError(NewRemoteError(Json{"code": "ENOENT", "path": ""}), "/fallback"); got.Path != "" {
		t.Errorf("path = %q, want the reported empty path, not the fallback", got.Path)
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
	t.Parallel()
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

// to_file_error_cases.json is Pi's toFileError and abortResult (errors.ts) run on a fixed corpus by testdata/generate_to_file_error.mjs against
// the pinned source: every daemon error code (and codes it does not know), a reported path, an absent one and an empty one, each with and without a
// fallback path, plus a plain Error and a FileError as input. Regenerate it from the repository root with:
//
//	node env/testdata/generate_to_file_error.mjs > env/testdata/to_file_error_cases.json
type goldenFileError struct {
	Code     string  `json:"code"`
	Message  string  `json:"message"`
	Path     *string `json:"path"`
	HasCause bool    `json:"hasCause"`
}

func TestToFileErrorAndAbortResultMatchPisErrorsModule(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("testdata/to_file_error_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var golden struct {
		ToFile []struct {
			Kind         string          `json:"kind"`
			Fields       Json            `json:"fields"`
			Message      string          `json:"message"`
			FileCode     string          `json:"fileCode"`
			Path         *string         `json:"path"`
			Fallback     *string         `json:"fallback"`
			Result       goldenFileError `json:"result"`
			CauseIsInput bool            `json:"causeIsInput"`
			Same         bool            `json:"same"`
		} `json:"toFile"`
		Abort []struct {
			Signal string  `json:"signal"`
			Path   *string `json:"path"`
			Result *struct {
				OK    bool            `json:"ok"`
				Error goldenFileError `json:"error"`
			} `json:"result"`
		} `json:"abort"`
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	if len(golden.ToFile) < 150 || len(golden.Abort) != 9 {
		t.Fatalf("the golden file holds %d toFileError and %d abortResult cases; regenerate it with testdata/generate_to_file_error.mjs", len(golden.ToFile), len(golden.Abort))
	}
	// An absent path (undefined) and the empty string are the same Go string; the reported-empty-path case is told apart by the fields.
	orEmpty := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	for _, tc := range golden.ToFile {
		var input error
		switch tc.Kind {
		case "remote":
			input = NewRemoteError(tc.Fields)
		case "error":
			input = errors.New(tc.Message)
		case "file":
			input = durableenv.NewFileError(durableenv.FileErrorCode(tc.FileCode), tc.Message, orEmpty(tc.Path), nil)
		default:
			t.Fatalf("unknown golden kind %q", tc.Kind)
		}
		got := toFileError(input, orEmpty(tc.Fallback))
		label := tc.Kind + " " + tc.Message + " fields=" + fmt.Sprint(tc.Fields) + " fallback=" + orEmpty(tc.Fallback)
		if string(got.Code) != tc.Result.Code || got.Message != tc.Result.Message || got.Path != orEmpty(tc.Result.Path) || (got.Cause != nil) != tc.Result.HasCause {
			t.Errorf("%s: got {%s %q %q cause=%v}, Pi {%s %q %q cause=%v}", label, got.Code, got.Message, got.Path, got.Cause != nil, tc.Result.Code, tc.Result.Message, orEmpty(tc.Result.Path), tc.Result.HasCause)
		}
		if tc.Kind == "file" {
			if same := errors.Is(input, got); tc.Same != same {
				t.Errorf("%s: a FileError is returned as it is: same=%v, Pi %v", label, same, tc.Same)
			}
		} else if isCause := errors.Is(got.Cause, input); tc.CauseIsInput != isCause {
			t.Errorf("%s: the cause is the input error: %v, Pi %v", label, isCause, tc.CauseIsInput)
		}
	}
	for _, tc := range golden.Abort {
		ctx := context.Background()
		if tc.Signal == "aborted" {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			ctx = cancelled
		}
		err := abortError(ctx, orEmpty(tc.Path))
		if (tc.Result == nil) != (err == nil) {
			t.Errorf("signal %s path %v: error %v, Pi result %+v", tc.Signal, tc.Path, err, tc.Result)
			continue
		}
		if tc.Result == nil {
			continue
		}
		var fileError *durableenv.FileError
		if !errors.As(err, &fileError) || tc.Result.OK || string(fileError.Code) != tc.Result.Error.Code || fileError.Message != tc.Result.Error.Message || fileError.Path != orEmpty(tc.Result.Error.Path) || (fileError.Cause != nil) != tc.Result.Error.HasCause {
			t.Errorf("signal %s path %v: got %+v, Pi %+v", tc.Signal, tc.Path, err, tc.Result)
		}
	}
}
