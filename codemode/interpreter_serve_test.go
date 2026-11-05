package codemode_test

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/codemode"
)

// pig additive (D263): ServeOptions.Interpreter reaches the runtime a served execution uses. The compiler writes its machine
// code under CacheDir and the interpreter writes nothing there.
func TestServeOptionsInterpreterReachesTheRuntime(t *testing.T) {
	for _, interpreter := range []bool{false, true} {
		dir := t.TempDir()
		serve := codemode.ServeOptions{CacheDir: dir, Interpreter: interpreter, MaxMemoryLimitBytes: 49 << 20}
		sandbox := newRemoteSandbox(t, codemode.RemoteSandboxOptions{}, clonedRemote{serve: serve})
		result := run(t, sandbox.Sandbox, "return 6 * 7")
		if !result.OK {
			t.Fatalf("interpreter=%v: result = %+v (%+v)", interpreter, result, result.Error)
		}
		sameJSON(t, "value", result.Value, "42")
		if written := writtenFiles(t, dir) > 0; written == interpreter {
			t.Errorf("interpreter=%v: CacheDir written = %v", interpreter, written)
		}
	}
}

func writtenFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	if err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return n
}
