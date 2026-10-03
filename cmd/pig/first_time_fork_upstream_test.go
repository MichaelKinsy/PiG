//go:build linux

package main

import "testing"

// Ports packages/coding-agent/test/first-time-setup-fork.test.ts:36.
// startup-ui.ts:122-140 excludes non-official distributions; PiG runs its own first-time setup on every interactive start
// without settings.json (D88), so this fork shows it.
func TestForkedDistributionDoesNotBlockOnOfficialFirstTimeSetup(t *testing.T) {
	if !firstTimeStartupShowsSetup(t, buildPigBinaryForSignalTest(t), true, false, false) {
		t.Fatal("PiG startup without settings.json did not display first-time setup (D88)")
	}
}
