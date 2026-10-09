package env

import (
	"errors"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// TestRemoteExecutionEnvTruncateFileRejectsUnsafeSizes ports remote-env.ts:636-643 truncateFile: a size that is not a
// non-negative safe integer fails with FileError("invalid", "File size must be a non-negative safe integer") naming the
// resolved path, before any request reaches the daemon; a valid size truncates and extends.
// Pi source: packages/env/src/remote-env.ts
func TestRemoteExecutionEnvTruncateFileRejectsUnsafeSizes(t *testing.T) {
	env, _ := remoteEnvironment(t)
	if err := env.WriteFile(background, "t.txt", "abcdef"); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int64{-1, maxSafeInteger + 1} {
		err := env.TruncateFile(background, "t.txt", size)
		fileError, ok := errors.AsType[*durableenv.FileError](err)
		if !ok || fileError.Code != durableenv.FileErrorInvalid || fileError.Message != "File size must be a non-negative safe integer" {
			t.Fatalf("TruncateFile(%d) = %v, want an invalid FileError about the size", size, err)
		}
	}
	if got := must(env.ReadTextFile(background, "t.txt")); got != "abcdef" {
		t.Fatalf("a rejected truncate changed the file to %q", got)
	}
	if err := env.TruncateFile(background, "t.txt", 3); err != nil {
		t.Fatal(err)
	}
	if got := must(env.ReadTextFile(background, "t.txt")); got != "abc" {
		t.Fatalf("after truncate to 3, file = %q, want abc", got)
	}
}
