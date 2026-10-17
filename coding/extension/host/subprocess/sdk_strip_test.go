package subprocess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// D92: a Go source extension that needs the staged SDK reports the strip when
// extension-sdk-go is stripped, instead of telling the user to run `pig
// reload`, which would stage nothing. Unstripped, the missing stage is
// reported as before.
func TestStripExtensionSDKGoRuntimeReportsStrip(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module example.com/hello\n\ngo 1.22\n\nrequire "+goSDKModule+" v0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extension.go"), []byte("package hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	builder := NewBuilderWithConfigRoot(t.TempDir(), t.TempDir())
	if _, err := builder.BuildContext(t.Context(), "hello", src); err == nil || !strings.Contains(err.Error(), "staged Go SDK is missing") {
		t.Fatalf("stock build error = %v, want the missing staged SDK", err)
	}

	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.ExtensionSDKGo))
	want := "The Go extension SDK is stripped from this Piglet (strip.features: extension-sdk-go)"
	if _, err := builder.BuildContext(t.Context(), "hello", src); err == nil || err.Error() != want {
		t.Fatalf("stripped build error = %v, want %q", err, want)
	}
}
