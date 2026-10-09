package main

import (
	"slices"
	"testing"
)

// TestSourcePropsIgnoreTrailingComments: the members read from a pinned interface exclude its trailing `// Default: ...` comments, even
// when a comment holds a quote and a parenthesis (image-resize-core.ts:4-9 ImageResizeOptions, "Anthropic's 5MB limit)").
func TestSourcePropsIgnoreTrailingComments(t *testing.T) {
	src := "export interface ImageResizeOptions {\n" +
		"\tmaxWidth?: number; // Default: 2000\n" +
		"\tmaxHeight?: number; // Default: 2000\n" +
		"\tmaxBytes?: number; // Default: 4.5MB of base64 payload (below Anthropic's 5MB limit)\n" +
		"\tjpegQuality?: number; // Default: 80\n" +
		"}\n"
	aliases := aliasTable{}
	readInterfaces(src, aliases, ifacePrefix+"coding-agent")
	d := &detector{aliases: aliases}
	var names []string
	for _, p := range d.readSourceProps("coding-agent", "ImageResizeOptions", 0) {
		names = append(names, p.Name)
		if !p.Optional || p.Type != "number" {
			t.Errorf("member %s = %+v, want an optional number", p.Name, p)
		}
	}
	if want := []string{"maxWidth", "maxHeight", "maxBytes", "jpegQuality"}; !slices.Equal(names, want) {
		t.Errorf("members = %v, want %v", names, want)
	}
}
