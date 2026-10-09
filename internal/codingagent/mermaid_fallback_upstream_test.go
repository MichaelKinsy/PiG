//go:build !pig_strip_mermaid

package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"
	"github.com/MichaelKinsy/PiG/internal/mermaid"
	"github.com/MichaelKinsy/PiG/tui"
)

const wideMermaidSource = `flowchart LR
  subgraph NB["Notebook"]
    direction TB
    NBCFG["the config file has many short words in it"]
    NBENV["the env file also has short words in it"]
  end
  subgraph PG["Pig"]
    direction TB
    PGPIG["the piglet file lists the tools it may use"]
    PGGW["the model code is about one seven five lines"]
  end
  NBCFG -. "one two three" .-> PGPIG
  NBENV -. "four five six" .-> PGGW
  PGPIG ==> PGGW`

// Upstream packages/coding-agent/test/mermaid.test.ts:40 and components/mermaid.ts:76-77 return token.raw for unsupported or naturally oversized diagrams, without a warning or a second layout with narrowed labels.
func TestMermaidUnsupportedAndOversizedRemainSource(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		width      int
	}{
		{"unsupported pie", "pie\n  title Pets\n  \"Dogs\" : 4", 100},
		{"oversized original", "flowchart LR\n  A[Start] --> B[Done]", 10},
		{"would fit only by narrowing", wideMermaidSource, 80},
		{"unmeasured area", "sequenceDiagram\n  A->>B: hello", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "would fit only by narrowing" {
				art, ok := mermaid.Render(tc.body)
				if !ok || art.Width <= tc.width {
					t.Fatalf("fixture does not exceed area: width=%d rendered=%v", art.Width, ok)
				}
			}
			md := "```mermaid\n" + tc.body + "\n```"
			for _, streaming := range []bool{false, true} {
				got := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)(md, extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, IsStreaming: streaming, AvailableWidth: tc.width})
				if got != md {
					t.Fatalf("streaming=%v transformed raw fallback:\ngot %q\nwant %q", streaming, got, md)
				}
			}
		})
	}
}

func TestMermaidFallbackAssistantBlockMatchesDisabledTransform(t *testing.T) {
	for _, body := range []string{"pie\n  title Pets\n  \"Dogs\" : 4", wideMermaidSource} {
		source := "Before\n\n```mermaid\n" + body + "\n```\n\nAfter"
		render := func(mode string) []string {
			block := tui.NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
			transform := createMermaidMarkdownTransformer(func() string { return mode }, tui.ActiveTheme())
			block.SetMarkdownTransform(markdowntransform.CreateMarkdownTransform(extension.MarkdownMessageAssistant, false, []extension.MarkdownTransformer{transform}))
			block.SetTextDelta(source)
			rows := block.Render(80)
			if block.Text() != source {
				t.Fatal("rendering changed stored assistant text")
			}
			return rows
		}
		if got, want := render("streaming"), render("off"); !slices.Equal(got, want) {
			t.Fatalf("caller fallback differs from raw code block:\ngot %q\nwant %q", got, want)
		}
	}
}

// Partial-parse warnings are upstream display behavior and must survive removal of unsupported/oversize hints.
func TestMermaidPartialWarningRemainsDisplayOnly(t *testing.T) {
	source := "```mermaid\nflowchart LR\n  A[Foo] invalid\n```"
	block := tui.NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	transform := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)
	block.SetMarkdownTransform(markdowntransform.CreateMarkdownTransform(extension.MarkdownMessageAssistant, false, []extension.MarkdownTransformer{transform}))
	block.SetTextDelta(source)
	if rows := strings.Join(block.Render(100), "\n"); !strings.Contains(rows, "Mermaid diagram not rendered") {
		t.Fatalf("upstream partial warning missing: %q", rows)
	}
	if block.Text() != source {
		t.Fatal("display warning entered stored assistant text")
	}
}

// TestMermaidFallbackParityTrace exposes the production transform's complete bytes to the pinned-Pi CLI comparator.
func TestMermaidFallbackParityTrace(t *testing.T) {
	var outputs []string
	for _, tc := range []struct {
		body  string
		width int
	}{
		{"pie\n  title Pets\n  \"Dogs\" : 4", 100},
		{"flowchart LR\n  A[Start] --> B[Done]", 10},
		{wideMermaidSource, 80},
		{"flowchart LR\n  A[Foo] invalid\n  B[Bar] also-invalid", 100},
	} {
		md := "```mermaid\n" + tc.body + "\n```"
		for _, streaming := range []bool{false, true} {
			out := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)(md, extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, IsStreaming: streaming, AvailableWidth: tc.width})
			outputs = append(outputs, out)
		}
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(outputs); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("MERMAID_FALLBACK %s", data.String())
}

func BenchmarkMermaidOversizedTransform(b *testing.B) {
	transform := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)
	context := extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 80}
	markdown := "```mermaid\n" + wideMermaidSource + "\n```"
	b.ReportAllocs()
	for b.Loop() {
		transform(markdown, context)
	}
}
