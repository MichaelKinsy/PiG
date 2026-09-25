package subprocess_test

import (
	"context"
	"strings"
	"testing"
	"time"

	subprocess "github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestHost_LoadErrors_RetainedForVisibility verifies the error-visibility
// change: when LoadAll cannot load an extension, the formatted error is
// retained on the Host so the interactive session can surface it in-session
// instead of dropping it to a TUI-clobbered stderr.
func TestHost_LoadErrors_RetainedForVisibility(t *testing.T) {
	h := subprocess.NewHost(t.TempDir())
	h.SetUIBridge(subprocess.NewUIBridge(func() {}))
	defer h.Shutdown("test done")

	// Before any load, no errors.
	if got := h.LoadErrors(); len(got) != 0 {
		t.Fatalf("fresh host LoadErrors = %v, want empty", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A source-based extension pointing at a path that cannot be built/resolved.
	loaded, errs := h.LoadAll(ctx, []subprocess.ExtConfig{{
		Name:    "broken",
		Enabled: true,
		Source:  "/nonexistent/pig-extension-path-xyz",
	}})
	if len(errs) == 0 {
		t.Fatalf("expected LoadAll to error on a nonexistent source; loaded=%d", len(loaded))
	}

	issues := h.LoadErrors()
	if len(issues) == 0 {
		t.Fatal("LoadErrors() returned nothing after a failed load: error not retained for in-session visibility")
	}
	joined := strings.Join(issues, "\n")
	if !strings.Contains(joined, "broken") && !strings.Contains(joined, "nonexistent") {
		t.Fatalf("retained load error does not name the failing extension/source: %q", joined)
	}
	// Count matches the returned errors.
	if len(issues) != len(errs) {
		t.Fatalf("LoadErrors count %d != returned errs %d", len(issues), len(errs))
	}
}
