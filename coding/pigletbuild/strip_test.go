package pigletbuild

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
)

// TestBinaryStripEntriesRecordTheEffectiveStrip pins that the Binary record
// input carries the effective strip list in canonical order, and nothing
// without one.
func TestBinaryStripEntriesRecordTheEffectiveStrip(t *testing.T) {
	if got := binaryStripEntries(&piglet.Piglet{Name: "plain"}); got != nil {
		t.Fatalf("no strip list recorded %v", got)
	}
	p, err := piglet.ParseBytes([]byte("name: lean\nstrip:\n  tools: [grep]\n  commands: [/share]\n  extensions: [mcp]\n  apis: [bedrock-converse-stream]\n  features: [themes, node-extensions, experimental-server]\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []pigletartifact.StripEntry{
		{Kind: "tool", ID: "grep", Disposition: "runtime"},
		{Kind: "command", ID: "/share", Disposition: "runtime"},
		{Kind: "extension", ID: "mcp", Disposition: "binary"},
		{Kind: "api", ID: "bedrock-converse-stream", Disposition: "binary"},
		{Kind: "feature", ID: "experimental-server", Disposition: "binary"},
		{Kind: "feature", ID: "node-extensions", Disposition: "binary"},
		{Kind: "feature", ID: "themes", Disposition: "runtime"},
	}
	if got := binaryStripEntries(p); !slices.Equal(got, want) {
		t.Fatalf("binaryStripEntries = %v, want %v", got, want)
	}
}

// TestBakePigletKeepsStrip pins that the baked runtime Piglet keeps the strip
// list, so the Binary disables the same built-ins at startup.
func TestBakePigletKeepsStrip(t *testing.T) {
	p, err := piglet.ParseBytes([]byte("name: lean\nstrip:\n  extensions: [mcp]\n"))
	if err != nil {
		t.Fatal(err)
	}
	baked, err := bakePiglet(p, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(baked), "strip:") || !strings.Contains(string(baked), "- mcp") {
		t.Fatalf("baked Piglet lost the strip list:\n%s", baked)
	}
}

// TestPigletBinaryBuildArgsPassStripTags pins that the native builder passes
// the effective strip list's build tags to go build, and none without one.
func TestPigletBinaryBuildArgsPassStripTags(t *testing.T) {
	p, err := piglet.ParseBytes([]byte("name: lean\nstrip:\n  tools: [grep]\n  extensions: [mcp, llama.cpp]\n  apis: [bedrock-converse-stream]\n  features: [themes, node-extensions, experimental-server]\n"))
	if err != nil {
		t.Fatal(err)
	}
	tags := p.Strip.BuildTags()
	want := []string{"pig_strip_bedrock_converse_stream", "pig_strip_llama_cpp", "pig_strip_mcp", "pig_strip_node_extensions"}
	if !slices.Equal(tags, want) {
		t.Fatalf("BuildTags = %v, want %v", tags, want)
	}
	args := pigletBinaryBuildArgs("/tmp/pig-x", "", "", tags)
	if i := slices.Index(args, "-tags"); i < 0 || args[i+1] != strings.Join(want, ",") || args[len(args)-1] != "./cmd/pig" {
		t.Fatalf("build args = %v, want -tags %s before the package", args, strings.Join(want, ","))
	}
	if slices.Contains(pigletBinaryBuildArgs("/tmp/pig-x", "", "", (&piglet.Piglet{}).Strip.BuildTags()), "-tags") {
		t.Fatal("a Piglet without a strip list builds with tags")
	}
}

// TestStripRuntimeConflictRefusesARuntimeTheBinaryNeeds pins that a Piglet
// cannot compile out the extension runtime one of its own cells runs on:
// Node and Python cells always need theirs; a Go or Rust cell needs its SDK
// only when the Binary builds it at runtime instead of embedding it.
func TestStripRuntimeConflictRefusesARuntimeTheBinaryNeeds(t *testing.T) {
	p, err := piglet.ParseBytes([]byte("name: lean\nstrip:\n  features: [node-extensions, extension-sdk-python, extension-sdk-go]\n"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		language string
		embedded bool
		fails    bool
	}{
		{"node", true, true},
		{"python", true, true},
		{"go", true, false},
		{"go", false, true},
		{"rust", false, false},
	} {
		err := stripRuntimeConflict(p, subprocess.CellSpec{Key: "cell", Language: tc.language}, tc.embedded)
		if (err != nil) != tc.fails {
			t.Errorf("%s embedded=%t: error = %v, want failure %t", tc.language, tc.embedded, err, tc.fails)
		}
		if err != nil && !strings.Contains(err.Error(), "strip.features names") {
			t.Errorf("%s: error %q does not name the strip entry", tc.language, err)
		}
	}
	if err := stripRuntimeConflict(&piglet.Piglet{}, subprocess.CellSpec{Key: "cell", Language: "node"}, true); err != nil {
		t.Fatalf("a Piglet without a strip list: %v", err)
	}
}
