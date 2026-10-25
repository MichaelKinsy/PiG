package testenv

import (
	"strings"
	"testing"
)

func TestQualifiedNodeMismatchNamesBothReleases(t *testing.T) {
	if got := qualifiedNodeMismatch("v24.22.1", "24.22.1"); got != "" {
		t.Errorf("the qualified release reports a mismatch: %q", got)
	}
	for _, found := range []string{"v24.19.0", "v22.19.0", "v26.12.1", "24.22.1", "v24.22.1-nightly", ""} {
		got := qualifiedNodeMismatch(found, "24.22.1")
		if got == "" {
			t.Errorf("node %q passed as the qualified release 24.22.1", found)
			continue
		}
		if !strings.Contains(got, "v24.22.1") || !strings.Contains(got, ".node-version") || (found != "" && !strings.Contains(got, found)) {
			t.Errorf("node %q: message %q must name the found release, the qualified release and .node-version", found, got)
		}
	}
}

func TestCheckQualifiedNodeFailsTheTestOnAnotherRelease(t *testing.T) {
	r := &nodeRecorder{TB: t}
	func() {
		defer func() {
			if v := recover(); v != nil && v != r {
				panic(v)
			}
		}()
		checkQualifiedNode(r, "v24.19.0\n", "24.22.1\n")
	}()
	if !strings.Contains(r.fatal, "v24.19.0") || !strings.Contains(r.fatal, "v24.22.1") {
		t.Errorf("fatal = %q, want a failure naming v24.19.0 and v24.22.1", r.fatal)
	}
	ok := &nodeRecorder{TB: t}
	checkQualifiedNode(ok, "v24.22.1\n", "24.22.1\n")
	if ok.fatal != "" || ok.skip != "" {
		t.Errorf("the qualified release failed: fatal=%q skip=%q", ok.fatal, ok.skip)
	}
}
