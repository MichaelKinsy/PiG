package cli

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// Pi 0d23d534 (#11571) package-manager-cli.ts: a "progress" event with a message is printed in yellow; "start" stays dim and an empty message prints nothing.
func TestPackageProgressPrinterPrintsProgressEventsInYellow(t *testing.T) {
	t.Setenv("FORCE_COLOR", "1")
	var out bytes.Buffer
	print := packageProgressPrinter(&out)
	print(packagemanager.ProgressEvent{Type: "start", Action: "update", Source: "npm:demo", Message: new("Updating npm:demo...")})
	print(packagemanager.ProgressEvent{Type: "progress", Action: "update", Source: "npm:demo", Message: new("npm:demo is still 1.0.0 (registry reports 1.0.1); npm registry metadata may be stale, try again in a few minutes")})
	print(packagemanager.ProgressEvent{Type: "progress", Action: "update", Source: "npm:demo", Message: new("")})
	print(packagemanager.ProgressEvent{Type: "progress", Action: "update", Source: "npm:demo"})
	// chalk closes a style before each newline and reopens it after, so a message ending in a newline ends with an empty styled run.
	const want = "\x1b[2mUpdating npm:demo...\x1b[22m\n\x1b[2m\x1b[22m" +
		"\x1b[33mnpm:demo is still 1.0.0 (registry reports 1.0.1); npm registry metadata may be stale, try again in a few minutes\x1b[39m\n\x1b[33m\x1b[39m"
	if out.String() != want {
		t.Fatalf("output = %q, want %q", out.String(), want)
	}
}
