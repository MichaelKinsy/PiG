package cli

import (
	"strings"
	"testing"

	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// D92 (opt-in strip): the active Piglet's strip list also names the Piglet, so a request for a stripped built-in says which Piglet strips it and how to keep it.
func TestApplyPigletStripNamesThePigletInStripErrors(t *testing.T) {
	p := &piglet.Piglet{Name: "a2a", Strip: &piglet.StripSpec{Features: []string{pigstrip.NodeExtensions}}}
	undo := applyPigletStrip(p, &Args{})
	got := pigstrip.Error("The TypeScript and JavaScript extension runtime", pigstrip.ListFeatures, pigstrip.NodeExtensions).Error()
	for _, want := range []string{`the Piglet "a2a"`, "strip.features: node-extensions", "remove node-extensions from strip.features in the Piglet manifest and rebuild"} {
		if !strings.Contains(got, want) {
			t.Errorf("error %q lacks %q", got, want)
		}
	}
	undo()
	if got := pigstrip.Error("x", pigstrip.ListFeatures, pigstrip.NodeExtensions).Error(); strings.Contains(got, "a2a") {
		t.Errorf("after undo the error still names the Piglet: %q", got)
	}
}
