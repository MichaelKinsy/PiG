//go:build race

package codingagent

import (
	"fmt"
	"os"
)

// exitRaceChildrenAtOnce sets GORACE for every process this test binary starts, which includes the copies of the binary that
// act as an editor, a trash command, a clipboard tool or a child that crashes (TestMain): a binary built with -race sleeps TSan's
// atexit_sleep_ms, one second by default, before it exits, and none of those tests is about what a child does in that second.
// This process has already read its own GORACE, so its race reports are unchanged, and an option an existing GORACE sets
// stays unless it is atexit_sleep_ms.
func exitRaceChildrenAtOnce() error {
	options := "atexit_sleep_ms=0"
	if existing := os.Getenv("GORACE"); existing != "" {
		options = existing + " " + options
	}
	if err := os.Setenv("GORACE", options); err != nil {
		return fmt.Errorf("set GORACE for helper processes: %w", err)
	}
	return nil
}
