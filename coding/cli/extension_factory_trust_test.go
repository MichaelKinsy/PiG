//go:build !pig_strip_codemode || !pig_strip_llama_cpp || !pig_strip_mcp || !pig_strip_pig_login || !pig_strip_tool_search

package cli

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestReloadRunsBuiltInFactoriesAgainAndRetiresTheEarlierAPI is spec test 4 through the production extension set: a reload runs the
// compiled-in factory again, and the API the first run captured fails with the stale message while the new run's API works.
func TestReloadRunsBuiltInFactoriesAgainAndRetiresTheEarlierAPI(t *testing.T) {
	_, cwd, agentDir := resourceExtensionFixture(t)
	var captured []extension.API
	result := loadProductionExtensionSetIn(t, cwd, agentDir, []string{"-e", "builtin:sentinel"},
		extension.NamedInlineExtension{Name: "sentinel", Builtin: true, Factory: func(pi extension.API) error { captured = append(captured, pi); return nil }})
	if _, err := (&cliRuntimeBuilder{}).reloadBuildExtensions(t.Context(), result.build); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 2 {
		t.Fatalf("the factory ran %d times, want 2", len(captured))
	}
	stale := func(pi extension.API) (message string) {
		defer func() {
			if recovered := recover(); recovered != nil {
				message = fmt.Sprint(recovered)
			}
		}()
		pi.GetFlag("x")
		return ""
	}
	if got := stale(captured[0]); got != extension.DefaultStaleRuntimeMessage {
		t.Errorf("the first run's API says %q, want the stale message", got)
	}
	if got := stale(captured[1]); got != "" {
		t.Errorf("the second run's API says %q", got)
	}
}
