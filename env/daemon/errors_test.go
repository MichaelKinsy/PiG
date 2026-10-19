package daemon

// pi: packages/env/daemon/src/errors.rs

// Ports the message and JSON rules of packages/env/daemon/src/errors.rs: a Node-style code, libuv's uv_strerror text
// (errors.rs:96-126), the `CODE: description, syscall 'path'` message (errors.rs:54-66) and the error JSON (errors.rs:76-93).

import (
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestIOFailureMessagesFollowNodeAndLibuv(t *testing.T) {
	for _, test := range []struct {
		errno       syscall.Errno
		code, text  string
		description string
	}{
		{syscall.ENOENT, "ENOENT", "", "no such file or directory"},
		{syscall.EACCES, "EACCES", "", "permission denied"},
		{syscall.EEXIST, "EEXIST", "", "file already exists"},
		{syscall.ENOTDIR, "ENOTDIR", "", "not a directory"},
		{syscall.ENOTEMPTY, "ENOTEMPTY", "", "directory not empty"},
		{syscall.EISDIR, "EISDIR", "", "illegal operation on a directory"},
	} {
		if runtime.GOOS == "windows" && test.errno == syscall.ENOTDIR {
			// syscall.ENOTDIR is ERROR_PATH_NOT_FOUND on Windows, which libuv's uv_translate_sys_error names ENOENT; Windows has no ENOTDIR.
			test.code, test.description = "ENOENT", "no such file or directory"
		}
		failure := ioFailure(test.errno, "open", "/some/path")
		want := test.code + ": " + test.description + ", open '/some/path'"
		if failure.Code != test.code || failure.Message != want || failure.Syscall != "open" || failure.Path != "/some/path" {
			t.Errorf("%s: %+v, want message %q", test.code, failure, want)
		}
	}
}

func TestIOFailureBetweenNamesBothPaths(t *testing.T) {
	failure := ioFailureBetween(syscall.ENOENT, "rename", "/from", "/to")
	if want := "ENOENT: no such file or directory, rename '/from' -> '/to'"; failure.Message != want {
		t.Fatalf("message %q, want %q", failure.Message, want)
	}
	if failure.Path != "/from" {
		t.Fatalf("path %q", failure.Path)
	}
}

func TestFailureJSONCarriesSyscallPathAndExtraFields(t *testing.T) {
	plain := newFailure("timeout", "timeout").toJSON()
	if want := (Object{"code": "timeout", "message": "timeout"}); !reflect.DeepEqual(plain, want) {
		t.Fatalf("plain %v, want %v", plain, want)
	}
	failure := ioFailure(syscall.ENOENT, "lstat", "/x")
	failure.Extra = Object{"spillPath": "/tmp/spill"}
	got := failure.toJSON()
	want := Object{
		"code": "ENOENT", "message": "ENOENT: no such file or directory, lstat '/x'",
		"syscall": "lstat", "path": "/x", "spillPath": "/tmp/spill",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("json %v, want %v", got, want)
	}
	if strings.Contains(newFailure("a", "b").Error(), "a") {
		t.Fatalf("Error() is the message only")
	}
}
