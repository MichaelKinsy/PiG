package node

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// lostWrites reports success for every write and stores nothing, so the first conformance case that reads a file back must fail.
type lostWrites struct{ durableenv.ExecutionEnv }

func (lostWrites) WriteFile(context.Context, string, any) error { return nil }

// testing/env-conformance.ts createEnvConformance returns runner-independent cases with unique names; every case passes against a conforming environment, a failing check comes back as the case's error, and a watch case asks for the longer timeout its latency needs.
// packages/durable/src/testing/types.ts:28-45: EnvConformanceOptions has assertions, withEnv, shell and symlinks, and every case has a name and a run.
func TestCreateEnvConformanceCasesPassAgainstAConformingEnvAndFailAgainstABrokenOne(t *testing.T) {
	assertions := durabletest.EnvConformanceAssertions(durabletest.CreateTestingAssertions(t))
	provider := durabletest.EnvConformanceProvider(withTestEnv(t, NodeExecutionEnvOptions{}))
	shell, noSymlinks := conformanceShell()
	var symlinks *bool
	if noSymlinks {
		symlinks = new(false)
	}
	cases := durabletest.CreateEnvConformance(durabletest.EnvConformanceOptions{Assertions: assertions, WithEnv: provider, Shell: shell, Symlinks: symlinks})
	if len(cases) == 0 {
		t.Fatal("no environment conformance cases")
	}
	seen := map[string]bool{}
	var longest time.Duration
	for _, testCase := range cases {
		named := durabletest.EnvConformanceCase(testCase)
		if seen[named.Name] {
			t.Fatalf("duplicate case name %q", named.Name)
		}
		seen[named.Name] = true
		longest = max(longest, named.Timeout)
	}
	if longest == 0 {
		t.Fatal("no watch case asks for a longer timeout")
	}
	if err := cases[0].Run(); err != nil {
		t.Fatalf("%q failed against a conforming environment: %v", cases[0].Name, err)
	}
	broken := durabletest.CreateEnvConformance(durabletest.EnvConformanceOptions{
		Assertions: assertions,
		WithEnv: func(use func(durableenv.ExecutionEnv) error) error {
			return withTestEnv(t, NodeExecutionEnvOptions{})(func(executionEnv durableenv.ExecutionEnv) error { return use(lostWrites{executionEnv}) })
		},
		Shell: shell, Symlinks: symlinks,
	})
	if err := broken[0].Run(); err == nil {
		t.Fatalf("%q passed against an environment that drops every write", broken[0].Name)
	}
}
