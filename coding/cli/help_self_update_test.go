//go:build !pig_strip_self_update

package cli

import (
	"os"
	"strings"
	"testing"
)

// A build that strips self-update lists `pig update [source]` (TestHelpWithoutSelfUpdateListsOnlyTheSourceTarget), so the stock line
// is checked in a build that keeps the command.
func TestHelpTextUsesPigSelfUpdateTargets(t *testing.T) {
	help := captureStdout(func() { printHelp(os.Stdout, false) })
	// Pi 1.1.0 args.ts:293 lists `${APP_NAME} update [source|self|pi]`: the app's own name is the third self target, so pig lists `pig`, never `pi`.
	if !strings.Contains(help, "pig update [source|self|pig]") || strings.Contains(help, "source|self|pi]") {
		t.Fatalf("help has stale self-update targets:\n%s", help)
	}
}
