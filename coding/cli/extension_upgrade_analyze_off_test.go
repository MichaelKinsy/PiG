//go:build pig_strip_extension_sdk_go

package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// D92: in a Binary without the Go SDK, upgrading a Go source extension fails with a report that names the strip, and writes nothing.
func TestUpgradeInAGoSDKStrippedBinaryNamesTheStrip(t *testing.T) {
	dir := t.TempDir()
	report := newExtensionUpgrader(t.TempDir()).upgrade(context.Background(), subprocess.ExtConfig{Name: "demo", Source: dir, RuntimeLanguage: "go"}, false)
	if report.Status != upgradeFailed || !strings.Contains(report.Note, "compiled out the Go extension SDK") {
		t.Fatalf("report = %s %q; want a failure that names the compiled-out Go SDK", report.Status, report.Note)
	}
}
