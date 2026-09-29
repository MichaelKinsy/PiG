package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type fatalRecorder struct {
	testing.TB
	name, fatal string
}

func (r *fatalRecorder) Helper()      {}
func (r *fatalRecorder) Name() string { return r.name }
func (r *fatalRecorder) Fatal(args ...any) {
	r.fatal = fmt.Sprint(args...)
	runtime.Goexit()
}

func (r *fatalRecorder) run(fn func(testing.TB)) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(r)
	}()
	<-done
}

// A permission fixture outside RunUnprivileged fails on every host instead of passing as a non-root user and skipping or passing vacuously as root.
func TestReadOnlyDirRequiresRunUnprivileged(t *testing.T) {
	if RunUnprivileged(t) {
		return
	}
	missing := &fatalRecorder{TB: t, name: "TestWithoutRunUnprivileged"}
	missing.run(func(tb testing.TB) { ReadOnlyDir(tb, t.TempDir()) })
	if missing.fatal == "" {
		t.Fatal("ReadOnlyDir accepted a test that did not call RunUnprivileged")
	}
	dir := t.TempDir()
	subtest := &fatalRecorder{TB: t, name: t.Name() + "/subtest"}
	subtest.run(func(tb testing.TB) { ReadOnlyDir(tb, dir) })
	if subtest.fatal != "" {
		t.Fatalf("ReadOnlyDir rejected a subtest of a RunUnprivileged test: %s", subtest.fatal)
	}
	if err := os.Mkdir(filepath.Join(dir, "entry"), 0o755); err == nil {
		t.Fatal("ReadOnlyDir left the directory writable")
	}
}
