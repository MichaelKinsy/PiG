package codingagent

import (
	"os"
	"strings"
	"testing"
)

// envVar reads as os.Getenv does, for an unset, empty, one-unit, two-unit and long value.
func TestEnvVarMatchesGetenv(t *testing.T) {
	const name = "PIG_TEST_ENV_LOOKUP"
	v := newEnvVar(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	if v.get() != "" || v.isOne() {
		t.Fatalf("unset: get=%q isOne=%v", v.get(), v.isOne())
	}
	for _, value := range []string{"", "1", "0", "11", "10", "x", "é", strings.Repeat("v", 4096)} {
		t.Setenv(name, value)
		if got, want := v.get(), os.Getenv(name); got != want {
			t.Errorf("get with %.20q = %.20q, want %.20q", value, got, want)
		}
		if got, want := v.isOne(), os.Getenv(name) == "1"; got != want {
			t.Errorf("isOne with %.20q = %v, want %v", value, got, want)
		}
	}
}
