package env

import "testing"

// packages/env/src/connection.ts:22-34: a RemoteError takes its message from fields.message when that is a string and is "Remote operation failed" otherwise; code defaults to "unknown", path is kept only when it is a string, and fields is the daemon's object.
func TestNewRemoteErrorTakesMessageCodeAndPathFromTheDaemonFields(t *testing.T) {
	err := NewRemoteError(Json{"message": "no such file", "code": "ENOENT", "path": "/a"})
	if err.Message != "no such file" || err.Error() != "no such file" || err.Code != "ENOENT" || err.Path != "/a" || err.Name() != "RemoteError" {
		t.Fatalf("error = %+v", err)
	}
	if err.Fields["code"] != "ENOENT" {
		t.Fatalf("fields = %v", err.Fields)
	}
	bare := NewRemoteError(Json{"message": 7, "code": 3, "path": 9})
	if bare.Message != "Remote operation failed" || bare.Code != "unknown" || bare.Path != "" {
		t.Fatalf("non-string fields must fall back: %+v", bare)
	}
}
