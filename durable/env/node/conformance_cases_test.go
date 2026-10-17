package node

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// discardingWrites loses every written file, so the first conformance case cannot read its fixture back.
type discardingWrites struct{ durableenv.ExecutionEnv }

func (discardingWrites) WriteFile(context.Context, string, any) error { return nil }

// createEnvConformance (testing/runner.ts) returns runner-independent cases: each has a unique name, the first passes
// against a conforming environment and fails against one that loses writes, so the cases themselves detect a broken
// environment.
func TestCreateEnvConformanceDetectsABrokenEnvironment(t *testing.T) {
	shell, noSymlinks := conformanceShell()
	symlinks := !noSymlinks
	options := func(wrap func(durableenv.ExecutionEnv) durableenv.ExecutionEnv) durabletest.EnvConformanceOptions {
		return durabletest.EnvConformanceOptions{
			Assertions: durabletest.CreateTestingAssertions(t),
			WithEnv: func(use func(durableenv.ExecutionEnv) error) error {
				return use(wrap(NewNodeExecutionEnv(NodeExecutionEnvOptions{Cwd: t.TempDir()})))
			},
			Shell: shell, Symlinks: &symlinks,
		}
	}
	cases := durabletest.CreateEnvConformance(options(func(env durableenv.ExecutionEnv) durableenv.ExecutionEnv { return env }))
	if len(cases) == 0 {
		t.Fatal("no conformance cases")
	}
	seen := map[string]bool{}
	for _, testCase := range cases {
		if seen[testCase.Name] {
			t.Fatalf("duplicate case name %q", testCase.Name)
		}
		seen[testCase.Name] = true
	}
	if err := cases[0].Run(); err != nil {
		t.Fatalf("%q failed against a conforming environment: %v", cases[0].Name, err)
	}
	broken := durabletest.CreateEnvConformance(options(func(env durableenv.ExecutionEnv) durableenv.ExecutionEnv { return discardingWrites{env} }))
	if err := broken[0].Run(); err == nil {
		t.Fatalf("%q passed against an environment that loses writes", broken[0].Name)
	}
}
