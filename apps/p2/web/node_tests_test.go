package web_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
)

// The browser client's pure modules are Pi's TypeScript, vendored verbatim (upstream/); their tests are Pi's, run unchanged under Node.

// Pi main a44127d9: home directories shown as `~` in the sidebar's paths (src/web/path-label.ts).
func TestPathLabelRunsPisTests(t *testing.T) {
	oracle.RunNodeTests(t, "test/p2/path-label.test.ts")
}

// Pi main 1d79ac79: the sidebar's rollups and rows as pure data (plugins/sessions/sidebar/model.ts).
func TestSidebarModelRunsPisTests(t *testing.T) {
	oracle.RunNodeTests(t, "test/p2/sidebar-model.test.ts")
}

// Pi main b30d54b8: the sidebar's rollups made from code, as pure data (plugins/sessions/sidebar/custom.ts).
func TestSidebarCustomRunsPisTests(t *testing.T) {
	oracle.RunNodeTests(t, "test/p2/sidebar-custom.test.ts")
}
