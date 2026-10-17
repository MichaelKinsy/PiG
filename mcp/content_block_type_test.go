package mcp

import (
	"encoding/json"
	"testing"
)

// protocol/content.ts ContentBlock = TextContent | ImageContent | AudioContent | ResourceLinkContent | EmbeddedResourceContent: "type" is the closed union
// text | image | audio | resource_link | resource, and a decoded block carries exactly the literal the server sent.
func TestContentBlockTypesAreTheUpstreamLiterals(t *testing.T) {
	for constant, literal := range map[ContentBlockType]string{
		ContentText: "text", ContentImage: "image", ContentAudio: "audio", ContentResourceLink: "resource_link", ContentResource: "resource",
	} {
		if string(constant) != literal {
			t.Errorf("constant %q, want %q", constant, literal)
		}
		var block ContentBlock
		if err := json.Unmarshal([]byte(`{"type":"`+literal+`"}`), &block); err != nil || block.Type != constant {
			t.Errorf("%s decoded to %q (%v)", literal, block.Type, err)
		}
	}
}
