package plugins

import "testing"

// Proof of `pluginFile` outside a facet script (evaluate.ts): a Go program has no plugin file to give and gets Pi's error.

func TestPluginFileOutsideAFacetScriptGivesNoFileAndPisError(t *testing.T) {
	got, err := PluginFile("asset.txt")
	if got != "" {
		t.Fatalf("PluginFile returned the path %q, want none", got)
	}
	want := `pluginFile("asset.txt"): only a plugin's facet script, evaluated by the plugins component, has one`
	if err == nil || err.Error() != want {
		t.Fatalf("PluginFile error = %v, want %q", err, want)
	}
}
