package subprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type dialogExpansionUI struct {
	extension.UIContext
	expanded atomic.Bool
}

func (u *dialogExpansionUI) GetToolsExpanded() bool { return u.expanded.Load() }
func (u *dialogExpansionUI) Select(_ context.Context, title string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	u.expanded.Store(title == "expand")
	if title == "cancel" {
		return "", context.Canceled
	}
	if title == "error" {
		return "", fmt.Errorf("dialog failed")
	}
	return title, nil
}

// Pi interactive-mode.ts:2585,2623 exposes expansion changes before the selector Promise resolves, including dismissal.
func TestNodeDialogPublishesExpansionBeforeCompletion(t *testing.T) {
	shortSockDir(t)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "dialog-state.mjs")
			if err := os.WriteFile(source, []byte(`export default function(pi) {
 pi.registerCommand("probe", {handler: async (_, ctx) => {
  if(ctx.ui.getToolsExpanded() !== false) throw new Error("initial state");
  for(const title of ["expand", "collapse", "expand", "cancel", "expand", "error"]) {
   let failed = false;
   try { await ctx.ui.select(title, ["chosen"]); } catch { failed = true; }
   if(failed !== (title === "error")) throw new Error("dialog error was lost");
   if(ctx.ui.getToolsExpanded() !== (title === "expand")) throw new Error("stale expansion after " + title);
  }
 }});
}`), 0o600); err != nil {
				t.Fatal(err)
			}
			h := newTestHost(t)
			ui := &dialogExpansionUI{UIContext: extension.NoopUIContext}
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(ui)
			h.SetUIBridge(bridge)
			defer h.Shutdown("test done")
			exts, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "dialog-state", Source: source, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			if err := exts[0].Commands["probe"].Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}
