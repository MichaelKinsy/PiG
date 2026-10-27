package subprocess

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// The package's tests draw editors, as the pig command does, so they enable editor views once. A program that does not
// leaves the editor builder, tui.Editor and the dictionaries behind it out of its binary (TestHeadlessEmbedBinarySizeGate).
func init() { EnableEditorViews() }

func TestEditorViewBuildFailsUntilEditorViewsAreEnabled(t *testing.T) {
	_, err := buildEditorWith(nil, nil, &ViewNode{Kind: ViewKindEditor, ID: "e"}, &frontend.ViewNode{})
	if err == nil || !strings.Contains(err.Error(), "EnableEditorViews") {
		t.Fatalf("building an editor before EnableEditorViews: err = %v, want one that names EnableEditorViews", err)
	}
	if editorViewBuilder.Load() == nil {
		t.Fatal("EnableEditorViews left the editor builder unset")
	}
}
