package cli

import (
	"bytes"
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Pi 0d23d534 package-manager-cli.ts: the progress callback prints start events dim and progress events (the stale-registry
// warning of `update`) yellow; completion and error events print nothing.
func TestPackageProgressPrinterPrintsProgressEventsInYellow(t *testing.T) {
	var out bytes.Buffer
	printer := packageProgressPrinter(&out)
	printer(packagemanager.ProgressEvent{Type: "start", Action: "update", Source: "npm:x", Message: new("Updating npm:x...")})
	printer(packagemanager.ProgressEvent{Type: "progress", Action: "update", Source: "npm:x", Message: new("npm:x is still 1.0.0 (registry reports 1.0.1); npm registry metadata may be stale, try again in a few minutes")})
	printer(packagemanager.ProgressEvent{Type: "progress", Action: "update", Source: "npm:x"})
	printer(packagemanager.ProgressEvent{Type: "complete", Action: "update", Source: "npm:x"})
	want := chalkDim("Updating npm:x...\n") + chalkYellow("npm:x is still 1.0.0 (registry reports 1.0.1); npm registry metadata may be stale, try again in a few minutes\n")
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}

// pi: packages/coding-agent/test/package-command-paths.test.ts, 342514bdf (#11707) and 806cdd68e (#11711): `update` lists the changed
// packages, claims "up to date" only when it checked the packages and nothing warned, and says so when offline.
func TestUpdateSummaryPrint(t *testing.T) {
	changed := &packagemanager.ProgressEvent{Type: "result", Action: "update", Source: "npm:foo", Message: new("npm:foo 1.2.0 -> 1.3.0")}
	warning := &packagemanager.ProgressEvent{Type: "progress", Action: "update", Source: "npm:foo", Message: new("npm:foo is still 1.2.0 (registry reports 1.3.0); npm registry metadata may be stale, try again in a few minutes")}
	for _, tc := range []struct {
		name    string
		offline bool
		source  string
		events  []*packagemanager.ProgressEvent
		want    string
	}{
		{name: "one changed", events: []*packagemanager.ProgressEvent{changed}, want: chalkGreen("Updated 1 package:") + "\n  npm:foo 1.2.0 -> 1.3.0\n"},
		{name: "nothing changed", want: chalkGreen("Packages are already up to date") + "\n"},
		{name: "nothing changed for a source", source: "npm:foo", want: chalkGreen("npm:foo is already up to date") + "\n"},
		{name: "stale registry", events: []*packagemanager.ProgressEvent{warning}, want: chalkYellow("No packages were updated, see the warnings above") + "\n"},
		{name: "stale registry for a source", source: "npm:foo", events: []*packagemanager.ProgressEvent{warning}, want: chalkYellow("npm:foo was not updated, see the warnings above") + "\n"},
		{name: "changed with a warning", events: []*packagemanager.ProgressEvent{warning, changed}, want: chalkGreen("Updated 1 package:") + "\n  npm:foo 1.2.0 -> 1.3.0\n"},
		{name: "offline", offline: true, want: chalkYellow("Skipped package updates (offline mode)") + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.offline {
				t.Setenv("PI_OFFLINE", "1")
			} else {
				t.Setenv("PI_OFFLINE", "")
				t.Setenv("PIG_OFFLINE", "")
			}
			var summary updateSummary
			progress := summary.progress(io.Discard)
			for _, event := range tc.events {
				progress(*event)
			}
			if got := captureStdout(func() { summary.print(tc.source) }); got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
		})
	}
}
