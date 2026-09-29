package testenv

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRequireLiveEnv(t *testing.T) {
	const first, second = "PIG_TEST_LIVE_FIRST", "PIG_TEST_LIVE_SECOND"
	for _, tc := range []struct {
		name       string
		values     map[string]string
		names      []string
		want, skip string
	}{
		{name: "unset", names: []string{first, second}, skip: "live test: missing environment variables: " + first + ", " + second},
		{name: "empty", values: map[string]string{first: ""}, names: []string{first}, skip: "live test: missing environment variables: " + first},
		{name: "partially configured", values: map[string]string{first: "secret-not-in-diagnostic"}, names: []string{first, second}, skip: "live test: missing environment variables: " + second},
		{name: "configured", values: map[string]string{first: "first-key", second: "second-key"}, names: []string{first, second}, want: "first-key"},
		{name: "no requirements"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{first, second} {
				t.Setenv(name, "")
				if err := os.Unsetenv(name); err != nil {
					t.Fatal(err)
				}
			}
			for name, value := range tc.values {
				t.Setenv(name, value)
			}
			probe := &liveEnvProbe{TB: t}
			got := RequireLiveEnv(probe, tc.names...)
			if probe.skip != tc.skip {
				t.Fatalf("skip = %q, want %q", probe.skip, tc.skip)
			}
			if tc.skip == "" && got != tc.want {
				t.Fatalf("value = %q, want %q", got, tc.want)
			}
			if strings.Contains(probe.skip, "secret-not-in-diagnostic") {
				t.Fatal("diagnostic leaked a value")
			}
		})
	}
}

// The probe records the diagnostic without ending the enclosing unit test.
type liveEnvProbe struct {
	testing.TB
	skip string
}

func (p *liveEnvProbe) Skipf(format string, args ...any) { p.skip = fmt.Sprintf(format, args...) }
