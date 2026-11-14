// SPDX-License-Identifier: MIT

package pioracle

import (
	"errors"
	"strings"
	"testing"
)

// A test whose Pi source is absent skips on a machine that never built the oracle and fails in a gate that installed it.
func TestMissingSkipsUnlessAGateRequiresTheOracle(t *testing.T) {
	absent := errors.New("no such file")
	for _, value := range []string{"", "0"} {
		t.Setenv(RequireEnv, value)
		if o := endOf(func(tb testing.TB) { Missing(tb, "the Pi mirror", absent) }); o.failed != "" || !strings.Contains(o.skipped, "the Pi mirror is not installed: no such file") {
			t.Errorf("%s=%q: skipped %q, failed %q, want a skip that names the reason", RequireEnv, value, o.skipped, o.failed)
		}
	}
	t.Setenv(RequireEnv, "1")
	o := endOf(func(tb testing.TB) { Missing(tb, "the Pi mirror", absent) })
	if o.skipped != "" || !strings.Contains(o.failed, "the Pi mirror is not installed (no such file)") || !strings.Contains(o.failed, "blocker, not passing evidence") {
		t.Errorf("%s=1: skipped %q, failed %q, want a failure that names the reason", RequireEnv, o.skipped, o.failed)
	}
}
