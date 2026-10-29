package cli

import (
	"bytes"
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
