package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi createContext (runner.ts:868-960) constructs enumerable own context fields, every getter of its object literal included (thinkingLevel at runner.ts:905-908). pi-cc-extensions/compact-thinking.ts spreads ctx before invoking its session_start delegate, so inherited fields alone are insufficient.
func TestNodeRequestContextSpreadsLikePi(t *testing.T) {
	nodeCellRequireNode(t)
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			entry := filepath.Join(t.TempDir(), "context.mjs")
			if err := os.WriteFile(entry, []byte(`export default function(pi) {
  pi.registerCommand("spread", {handler: async (_args, ctx) => {
    const copy = {...ctx};
    for (const key of ["ui", "mode", "hasUI", "cwd", "sessionManager", "modelRegistry", "model", "scopedModels", "thinkingLevel", "signal", "isIdle", "getSystemPrompt", "waitForIdle"]) {
      // An unbound runner's scopedModels is a fresh [] at every read, as Pi's default getScopedModels (runner.ts:366) is.
      if (!Object.hasOwn(copy, key) || (key !== "scopedModels" && copy[key] !== ctx[key])) throw new Error("context spread lost " + key);
    }
    if (!copy.ui.theme) throw new Error("context spread lost theme");
  }});
}`), 0o644); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "context", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v, %v", loaded, errs)
			}
			if err := loaded[0].Commands["spread"].Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}
