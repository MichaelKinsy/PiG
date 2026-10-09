package durabletest

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/env"
)

// upstream: packages/durable/src/testing/runner.ts:34 registerEnvConformance builds createEnvConformance({ ...options, assertions, withEnv }):
// the shell and symlinks options reach createEnvConformance, and the cases without symlinks lack the symbolic link ones.
func TestRegisterEnvConformanceOptionsReachTheCases(t *testing.T) {
	provider := EnvConformanceProvider(func(func(env.ExecutionEnv) error) error { return nil })
	assertions := CreateTestingAssertions(t)

	defaults := EnvConformanceRegisterOptions{}.conformanceOptions(assertions, provider)
	if defaults.Shell != nil || defaults.Symlinks != nil || defaults.WithEnv == nil {
		t.Fatalf("empty options = %+v, want the defaults with the provider", defaults)
	}
	shell := []string{"bash", "-c"}
	options := EnvConformanceRegisterOptions{Shell: shell, Symlinks: new(false)}.conformanceOptions(assertions, provider)
	if !slices.Equal(options.Shell, shell) || options.Symlinks == nil || *options.Symlinks {
		t.Fatalf("options = %+v, want shell %v and symlinks false", options, shell)
	}
	names := func(o EnvConformanceOptions) []string {
		var out []string
		for _, c := range CreateEnvConformance(o) {
			out = append(out, c.Name)
		}
		return out
	}
	with, without := names(defaults), names(options)
	if len(without) >= len(with) {
		t.Fatalf("symlinks false must drop the symbolic link cases: %d cases with, %d without", len(with), len(without))
	}
}
