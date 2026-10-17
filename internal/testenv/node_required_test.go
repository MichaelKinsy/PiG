package testenv

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// nodeRecorder captures Fatalf and Skip. Fatalf panics with the recorder so requireNode stops there, as the real Fatalf stops the test.
type nodeRecorder struct {
	testing.TB
	fatal, skip string
}

func (r *nodeRecorder) Helper() {}
func (r *nodeRecorder) Fatalf(f string, a ...any) {
	r.fatal = fmt.Sprintf(f, a...)
	panic(r)
}
func (r *nodeRecorder) Skip(a ...any) { r.skip = "skipped" }

func call(r *nodeRecorder, path string, err error, getenv func(string) string) string {
	defer func() {
		if v := recover(); v != nil && v != r {
			panic(v)
		}
	}()
	return requireNode(r, path, err, getenv)
}

func TestRequireNodeFailsLoudlyInManagedEnvironments(t *testing.T) {
	missing := errors.New("not found")
	for _, name := range managedEnvSignals {
		r := &nodeRecorder{TB: t}
		call(r, "", missing, func(k string) string {
			if k == name {
				return "1"
			}
			return ""
		})
		if r.skip != "" || !strings.Contains(r.fatal, "mise") || !strings.Contains(r.fatal, name) {
			t.Errorf("%s set: skip=%q fatal=%q, want a fatal naming the variable and the mise fix", name, r.skip, r.fatal)
		}
	}
}

func TestRequireNodeSkipsOnAPlainDeveloperRun(t *testing.T) {
	r := &nodeRecorder{TB: t}
	call(r, "", errors.New("not found"), func(string) string { return "" })
	if r.skip == "" || r.fatal != "" {
		t.Errorf("skip=%q fatal=%q, want a skip", r.skip, r.fatal)
	}
}

func TestRequireNodeReturnsTheFoundPath(t *testing.T) {
	r := &nodeRecorder{TB: t}
	if got := call(r, "/x/node", nil, func(string) string { return "1" }); got != "/x/node" || r.skip != "" || r.fatal != "" {
		t.Errorf("got %q skip=%q fatal=%q", got, r.skip, r.fatal)
	}
}
