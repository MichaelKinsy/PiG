package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"
)

// Pi eebd5d90 collapsible-message-components.test.ts "Markdown transformers": an expanded summary or skill block runs the transformers on its content
// as the surface's message type, with the persisted entry's id, and Pi's header stays outside what they rewrite.
func TestExpandedMessageComponentsRunMarkdownTransformersOnTheContentWithoutPisHeader(t *testing.T) {
	type seenCall struct {
		markdown string
		context  markdowntransform.MarkdownTransformContext
	}
	for _, tc := range []struct {
		kind, messageType, header string
		create                    func(transform func(string, int) string) interface {
			Component
			SetExpanded(bool)
		}
	}{
		{"compaction summary", "summary", "Compacted from 1,234 tokens", func(transform func(string, int) string) interface {
			Component
			SetExpanded(bool)
		} {
			return NewCompactionSummaryMessageComponentWithSurface(CompactionSummaryMessage{Summary: "details", TokensBefore: 1234}, nil, 1, MarkdownSurface{Transform: transform})
		}},
		{"branch summary", "summary", "Branch Summary", func(transform func(string, int) string) interface {
			Component
			SetExpanded(bool)
		} {
			return NewBranchSummaryMessageComponentWithSurface(BranchSummaryMessage{Summary: "details"}, nil, 1, MarkdownSurface{Transform: transform})
		}},
		{"skill", "custom", "example-skill", func(transform func(string, int) string) interface {
			Component
			SetExpanded(bool)
		} {
			return NewSkillInvocationMessageComponentWithSurface(ParsedSkillBlock{Name: "example-skill", Content: "details"}, nil, 1, MarkdownSurface{Transform: transform})
		}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			var seen []seenCall
			transformers := []markdowntransform.MarkdownTransformer{func(markdown string, ctx markdowntransform.MarkdownTransformContext) string {
				seen = append(seen, seenCall{markdown, ctx})
				return strings.Replace(markdown, "details", "transformed", 1)
			}}
			component := tc.create(markdowntransform.CreateMarkdownTransform(markdowntransform.MarkdownMessageType(tc.messageType), false, transformers, "entry-1", ""))
			component.SetExpanded(true)
			rendered := stripANSI(strings.Join(component.Render(80), "\n"))
			if !strings.Contains(rendered, tc.header) || !strings.Contains(rendered, "transformed") || strings.Contains(rendered, "details") {
				t.Fatalf("rendered %q: want the header and the transformed content", rendered)
			}
			if len(seen) == 0 || seen[0].markdown != "details" || string(seen[0].context.MessageType) != tc.messageType || seen[0].context.MessageID != "entry-1" {
				t.Fatalf("transformer saw %+v, want the content %q as %q for entry-1", seen, "details", tc.messageType)
			}
		})
	}
}

// Pi eebd5d90 custom-message.test.ts "runs Markdown transformers on default-rendered content".
func TestCustomMessageRunsMarkdownTransformersOnDefaultRenderedContent(t *testing.T) {
	var contexts []markdowntransform.MarkdownTransformContext
	transformers := []markdowntransform.MarkdownTransformer{func(markdown string, ctx markdowntransform.MarkdownTransformContext) string {
		contexts = append(contexts, ctx)
		return strings.Replace(markdown, "original", "transformed", 1)
	}}
	component := NewCustomMessageComponentWithSurface(&CustomMessage{CustomType: "test", Content: "original"}, nil, nil, 1, MarkdownSurface{Transform: markdowntransform.CreateMarkdownTransform(markdowntransform.MarkdownMessageCustom, false, transformers, "entry-1", "2025-01-15T10:30:00.000Z")})
	rendered := stripANSI(strings.Join(component.Render(40), "\n"))
	if !strings.Contains(rendered, "transformed") || strings.Contains(rendered, "original") {
		t.Fatalf("rendered %q", rendered)
	}
	if len(contexts) == 0 || contexts[0].MessageType != markdowntransform.MarkdownMessageCustom || contexts[0].IsStreaming || contexts[0].MessageID != "entry-1" || contexts[0].Timestamp != "2025-01-15T10:30:00.000Z" {
		t.Fatalf("context %+v", contexts)
	}
}

// Without a transform the expanded body is as before: the header and the content in one Markdown.
func TestExpandedMessageComponentsWithoutATransformKeepTheirHeader(t *testing.T) {
	component := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "details"}, nil, 1, nil, "", "")
	component.SetExpanded(true)
	rendered := stripANSI(strings.Join(component.Render(80), "\n"))
	if !strings.Contains(rendered, "Branch Summary") || !strings.Contains(rendered, "details") {
		t.Fatalf("rendered %q", rendered)
	}
}

// Pi's constructors take markdownTransformers, messageId and timestamp, not a surface: each builds createMarkdownTransform(messageType, false,
// markdownTransformers, messageId, timestamp) with "summary" for a branch or compaction summary and "custom" for a skill block or a custom message.
func TestPiShapedMessageComponentConstructorsRunTheTransformersAsTheirMessageType(t *testing.T) {
	type expandable interface {
		Component
		SetExpanded(bool)
	}
	for _, tc := range []struct {
		kind, messageType, content string
		create                     func(transformers []markdowntransform.MarkdownTransformer, id, timestamp string) expandable
	}{
		{"compaction summary", "summary", "details", func(tr []markdowntransform.MarkdownTransformer, id, ts string) expandable {
			return NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: "details", TokensBefore: 1234}, nil, 1, tr, id, ts)
		}},
		{"branch summary", "summary", "details", func(tr []markdowntransform.MarkdownTransformer, id, ts string) expandable {
			return NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: "details"}, nil, 1, tr, id, ts)
		}},
		{"skill", "custom", "details", func(tr []markdowntransform.MarkdownTransformer, id, ts string) expandable {
			return NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "example-skill", Content: "details"}, nil, 1, tr, id, ts)
		}},
		{"custom message", "custom", "details", func(tr []markdowntransform.MarkdownTransformer, id, ts string) expandable {
			return NewCustomMessageComponent(&CustomMessage{CustomType: "test", Content: "details"}, nil, nil, 1, tr, id, ts)
		}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			var seen []markdowntransform.MarkdownTransformContext
			transformers := []markdowntransform.MarkdownTransformer{func(markdown string, ctx markdowntransform.MarkdownTransformContext) string {
				seen = append(seen, ctx)
				return strings.Replace(markdown, tc.content, "transformed", 1)
			}}
			component := tc.create(transformers, "entry-7", "2025-01-15T10:30:00.000Z")
			component.SetExpanded(true)
			rendered := stripANSI(strings.Join(component.Render(80), "\n"))
			if !strings.Contains(rendered, "transformed") || strings.Contains(rendered, tc.content) {
				t.Fatalf("rendered %q: want the transformed content", rendered)
			}
			if len(seen) == 0 {
				t.Fatal("the transformers never ran")
			}
			ctx := seen[0]
			if string(ctx.MessageType) != tc.messageType || ctx.IsStreaming || ctx.MessageID != "entry-7" || ctx.Timestamp != "2025-01-15T10:30:00.000Z" {
				t.Fatalf("transformer context %+v, want %q, not streaming, entry-7, 2025-01-15T10:30:00.000Z", ctx, tc.messageType)
			}
		})
	}
}
