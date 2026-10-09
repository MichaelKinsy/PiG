//go:build !pig_strip_node_extensions

package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's Markdown cache belongs to the component, not to an extension-wide (text, width) key. Separate identical messages invoke a stateful transformer separately. Exercise the real wire in both Node placements.
func TestNodeMarkdownTransformerDoesNotCacheAcrossMessages(t *testing.T) {
	nodeCellRequireNode(t)
	for _, isolation := range []string{"strict", "shared-ok"} {
		t.Run(isolation, func(t *testing.T) {
			entry := filepath.Join(t.TempDir(), "transform.mjs")
			if err := os.WriteFile(entry, []byte(`export default function(pi) {
  let calls = 0;
  pi.registerMarkdownTransformer((text, context) => {
    if (text === "throws") throw new Error("transform error");
    if (text === "invalid") return false;
    return text + ":" + context.messageType + ":" + context.availableWidth + ":" + (++calls);
  });
}`), 0o644); err != nil {
				t.Fatal(err)
			}
			h := NewHost(t.TempDir())
			t.Cleanup(func() { h.Shutdown("test complete") })
			loaded, errs := h.LoadAll(t.Context(), []ExtConfig{{Name: "transform", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("load: %v, %v", loaded, errs)
			}
			transform := loaded[0].MarkdownTransformer
			ctx := extension.MarkdownTransformContext{Context: t.Context(), MessageType: extension.MarkdownMessageUser, AvailableWidth: 78}
			for i := 1; i <= 2; i++ {
				want := fmt.Sprintf("same:user:78:%d", i)
				if got := transform("same", ctx); got != want {
					t.Fatalf("transform returned before the reply or reused another message: got %q, want %q", got, want)
				}
			}
			for _, text := range []string{"throws", "invalid"} {
				if got := transform(text, ctx); got != text {
					t.Fatalf("Pi preserves input on exception/non-string: got %q, want %q", got, text)
				}
			}
		})
	}
}
