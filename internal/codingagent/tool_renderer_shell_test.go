package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// types.ts:658 ToolRenderers picks renderShell with renderCall and renderResult, and tool-execution.ts:117 reads it to choose the card's shell: edit declares renderShell "self"
// (edit.ts:157), every other built-in keeps the default shell. A card built from the shared renderers renders edit without the default content box.
func TestBuiltInToolRenderersCarryRenderShellIntoTheirCards(t *testing.T) {
	renderers := CreateAllToolRenderers()
	for name, pair := range renderers {
		want := extension.ToolRenderShellDefault
		if name == "edit" {
			want = extension.ToolRenderShellSelf
		}
		if pair.RenderShell != want {
			t.Errorf("%s renderShell = %q, want %q", name, pair.RenderShell, want)
		}
	}
	args := []byte(`{"path":"a.txt","edits":[{"oldText":"a","newText":"b"}]}`)
	cwd := t.TempDir()
	self := NewToolRendererCard(t.Context(), "edit", "c1", cwd, args, renderers["edit"], nil)
	defer self.Dispose()
	boxed := renderers["edit"]
	boxed.RenderShell = extension.ToolRenderShellDefault
	chrome := NewToolRendererCard(t.Context(), "edit", "c2", cwd, args, boxed, nil)
	defer chrome.Dispose()
	if slices.Equal(self.Component.Render(80), chrome.Component.Render(80)) {
		t.Fatal("the card ignored the renderers' renderShell: self and default render the same")
	}
}
