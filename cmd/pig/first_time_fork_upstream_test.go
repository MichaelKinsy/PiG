//go:build linux

package main

import "testing"

// Ports packages/coding-agent/test/first-time-setup-fork.test.ts:36.
// startup-ui.ts:122-140 excludes non-official distributions before the experimental/default-directory gates.
func TestForkedDistributionDoesNotBlockOnOfficialFirstTimeSetup(t *testing.T) {
	if firstTimeStartupShowsSetup(t, buildPigBinaryForSignalTest(t), true, false, false) {
		t.Fatal("fork startup displayed official-only setup")
	}
}
