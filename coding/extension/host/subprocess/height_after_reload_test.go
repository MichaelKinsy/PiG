package subprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeHeightReportingModule writes a packed Go factory whose "height" tool
// returns the terminal height the extension currently observes via
// ctx.Height(). The value only reaches the extension through the ready payload
// the host sends on the packed-Go path, so the tool result is a faithful proxy
// for "what height did this extension receive".
func writeHeightReportingModule(t *testing.T, modulePath, extName string) string {
	t.Helper()
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(findModuleRoot(t), "extensions", "sdk"))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), fmt.Appendf(nil, "module %s\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n", modulePath), 0o644); err != nil {
		t.Fatal(err)
	}
	src := fmt.Sprintf(`package ext

import (
	"fmt"
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func Extension() *sdk.Extension {
	e := sdk.New(%q)
	e.Tool("height", "report ctx.Height()", sdk.Schema{"type": "object"}, func(ctx sdk.Context, _ map[string]any) (any, error) {
		return map[string]any{"content": fmt.Sprintf("height=%%d width=%%d", ctx.Height(), ctx.Width())}, nil
	})
	return e
}
`, extName)
	if err := os.WriteFile(filepath.Join(dir, "extension.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// assertPackedHeight executes the height tool on the loaded packed extension
// and asserts it observed wantHeight.
func assertPackedHeight(t *testing.T, ctx context.Context, h *Host, wantHeight int) {
	t.Helper()
	exts := h.Extensions()
	if len(exts) != 1 {
		t.Fatalf("loaded extensions = %d, want 1", len(exts))
	}
	tool, ok := exts[0].Tools["height"]
	if !ok {
		t.Fatalf("height tool missing; tools = %#v", exts[0].Tools)
	}
	result, err := tool.Definition.Execute(ctx, "height-call", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("height tool execute: %v", err)
	}
	want := fmt.Sprintf("height=%d", wantHeight)
	if got := fmt.Sprint(result); !strings.Contains(got, want) {
		t.Fatalf("packed extension observed %q, want it to contain %q", got, want)
	}
}

// TestPackedGoExtensionReceivesHeightThroughReload is the regression guard for
// GitHub issue #115: Go factory extensions on the packed-Go path lost the
// terminal height after /reload because acceptPackedExt built the ready payload
// with only Width, never Height. On a fresh start a one-time NotifyHeight
// broadcast masked it, but a reloaded process re-ran acceptPackedExt and saw
// Height: 0 until the next terminal resize.
//
// The host's heightFunc is registered before the load here, which is exactly
// the reload condition (the TUI already exists). Before the fix the packed
// ready payload ignored heightFunc and the extension observed height=0; after
// it, the extension observes the real height on every reload, matching the
// isolated path.
func TestPackedGoExtensionReceivesHeightThroughReload(t *testing.T) {
	root := writeHeightReportingModule(t, "example.com/heightcell", "height-ext")
	h := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) {
		return []ExtConfig{packedFactoryConfig("height-ext", root, "example.com/heightcell", "h1")}, nil
	})
	t.Cleanup(func() { h.Shutdown("test done") })

	// Register geometry up front, as interactive wiring does once the TUI
	// exists. A resize updates these before a later reload.
	var height atomic.Int64
	height.Store(42)
	h.SetHeightFunc(func() int { return int(height.Load()) })
	h.SetWidthFunc(func() int { return 123 })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// First reload loads the packed cell through acceptPackedExt with the
	// height already wired. The ready payload must carry it.
	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("initial reload: %v", err)
	}
	assertPackedHeight(t, ctx, h, 42)

	// A terminal resize changes the height; the next reload re-handshakes the
	// packed process, which must advertise the current height, not 0 or the
	// stale value.
	height.Store(55)
	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("second reload: %v", err)
	}
	assertPackedHeight(t, ctx, h, 55)
}
