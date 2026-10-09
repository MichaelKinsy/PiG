package markdowntransform

// pi: packages/coding-agent/src/modes/interactive/components/markdown-transform.ts

import (
	"slices"
	"testing"
)

// packages/coding-agent/src/modes/interactive/components/markdown-transform.ts:3-29 createMarkdownTransform/applyMarkdownTransformers: the
// transformers run in order, each on the previous result, with one context of
// message type, streaming state and the render width; a throwing transformer
// leaves the current Markdown and the chain continues.
func TestMarkdownTransformChainOrderContextAndThrow(t *testing.T) {
	var seen []MarkdownTransformContext
	record := func(suffix string) MarkdownTransformer {
		return func(markdown string, ctx MarkdownTransformContext) string {
			seen = append(seen, ctx)
			return markdown + suffix
		}
	}
	throwing := func(string, MarkdownTransformContext) string { panic("broken transformer") }
	transform := CreateMarkdownTransform(MarkdownMessageAssistantThinking, true, []MarkdownTransformer{record("A"), throwing, record("B")})
	if got := transform("x", 37); got != "xAB" {
		t.Fatalf("chain result = %q, want xAB", got)
	}
	want := MarkdownTransformContext{MessageType: MarkdownMessageAssistantThinking, IsStreaming: true, AvailableWidth: 37}
	if !slices.Equal(seen, []MarkdownTransformContext{want, want}) {
		t.Fatalf("contexts = %+v", seen)
	}
	if got := CreateMarkdownTransform(MarkdownMessageUser, false, nil)("same", 5); got != "same" {
		t.Fatalf("empty chain = %q", got)
	}
}
