package pigstrip

import (
	"slices"
	"testing"
)

func TestTagMapsIDsToBuildTags(t *testing.T) {
	for id, want := range map[string]string{
		"mcp":                     "pig_strip_mcp",
		"llama.cpp":               "pig_strip_llama_cpp",
		"bedrock-converse-stream": "pig_strip_bedrock_converse_stream",
		"node-extensions":         "pig_strip_node_extensions",
	} {
		if got := Tag(id); got != want {
			t.Errorf("Tag(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestStripHasAndUndo(t *testing.T) {
	if Has(ListFeatures, Docs) {
		t.Fatal("a fresh process strips docs")
	}
	undo := Strip(ListFeatures, Docs)
	again := Strip(ListFeatures, Docs)
	if !Has(ListFeatures, Docs) || Has(ListAPIs, Docs) {
		t.Fatal("Strip did not record exactly features/docs")
	}
	again()
	if !Has(ListFeatures, Docs) {
		t.Fatal("undoing a repeated Strip removed the first one")
	}
	undo()
	if Has(ListFeatures, Docs) {
		t.Fatal("undo left features/docs stripped")
	}
	if got, want := Error("HTML export", ListFeatures, ExportHTML).Error(), "HTML export is stripped from this Piglet (strip.features: export-html)"; got != want {
		t.Fatalf("Error = %q, want %q", got, want)
	}
}

// TestFeatureIDsAreStable pins the published feature IDs Piglets name.
func TestFeatureIDsAreStable(t *testing.T) {
	want := []string{
		"themes", "skills", "prompt-templates", "experimental-server",
		"node-extensions", "extension-sdk-go", "extension-sdk-rust", "extension-sdk-python",
		"syntax-highlight", "word-dictionaries", "export-html", "self-update", "changelog",
		"docs", "mermaid", "piglet-builder",
	}
	if got := Features(); !slices.Equal(got, want) {
		t.Fatalf("Features() = %v, want %v", got, want)
	}
}

// TestKnownCoversEveryList pins that the generated table has IDs for each
// strip list and that Known returns a copy.
func TestKnownCoversEveryList(t *testing.T) {
	for _, list := range Lists() {
		ids := Known(list)
		if len(ids) == 0 {
			t.Fatalf("Known(%q) is empty", list)
		}
		ids[0] = "changed"
		if Known(list)[0] == "changed" {
			t.Fatalf("Known(%q) returned the table itself", list)
		}
	}
	if !slices.Contains(Known(ListCommands), "/model") || !slices.Contains(Known(ListFeatures), Skills) {
		t.Fatalf("Known lacks /model or skills: %v %v", Known(ListCommands), Known(ListFeatures))
	}
}
