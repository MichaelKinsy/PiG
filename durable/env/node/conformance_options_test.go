package node

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// env-conformance.ts EnvConformanceOptions: assertions, withEnv, shell (default ["sh","-c"]) and symlinks (default true). The symlink
// cases exist unless symlinks is false, and withEnv supplies the environment each case runs against.
// Pi: packages/durable/src/testing/types.ts:40 (name).
// Pi: packages/durable/src/testing/types.ts:43 (run).
// Pi: packages/durable/src/testing/types.ts:31 (assertions).
// Pi: packages/durable/src/testing/types.ts:34 (shell).
// Pi: packages/durable/src/testing/types.ts:36 (symlinks).
// Pi: packages/durable/src/testing/types.ts:32 (withEnv).
func TestEnvConformanceOptionsSymlinksShellAndProvider(t *testing.T) {
	uses := 0
	var provider durabletest.EnvConformanceProvider = func(use func(durableenv.ExecutionEnv) error) error {
		uses++
		return withTestEnv(t, NodeExecutionEnvOptions{})(use)
	}
	base := durabletest.EnvConformanceOptions{Assertions: durabletest.CreateTestingAssertions(t), WithEnv: provider}
	symlinkCases := func(options durabletest.EnvConformanceOptions) []durabletest.EnvConformanceCase {
		var found []durabletest.EnvConformanceCase
		for _, testCase := range durabletest.CreateEnvConformance(options) {
			if strings.Contains(testCase.Name, "symlink") {
				found = append(found, testCase)
			}
		}
		return found
	}
	if len(symlinkCases(base)) == 0 {
		t.Fatal("the default (symlinks true) has no symlink case")
	}
	explicitTrue := base
	explicitTrue.Symlinks = new(true)
	if got, want := len(symlinkCases(explicitTrue)), len(symlinkCases(base)); got != want {
		t.Fatalf("symlinks true has %d symlink cases, want %d", got, want)
	}
	none := base
	none.Symlinks = new(false)
	if got := symlinkCases(none); len(got) != 0 {
		t.Fatalf("symlinks false kept %d symlink cases", len(got))
	}

	if shell, _ := conformanceShell(); shell == nil {
		withShell := base
		withShell.Shell = []string{"sh", "-c"}
		cases := durabletest.CreateEnvConformance(withShell)
		if len(cases) != len(durabletest.CreateEnvConformance(base)) {
			t.Fatalf("an explicit default shell changed the case list")
		}
	}
	first := durabletest.CreateEnvConformance(base)[0]
	if err := first.Run(); err != nil {
		t.Fatalf("%s: %v", first.Name, err)
	}
	if uses != 1 {
		t.Fatalf("the provider was used %d times by one case, want 1", uses)
	}
}
