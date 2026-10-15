//go:build !pig_strip_mermaid

package codingagent

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi mermaid.ts:72-76 selects a top-level code token whose first info word is "mermaid" in any letter case (isMermaidInfo). The streaming transformer
// skips the parse when the text holds no such word, so the skip must be exact: every text with a selectable fence contains the word in some case.
func TestMermaidPreCheckNeverSkipsATextWithAMermaidFence(t *testing.T) {
	fences := []string{"mermaid", "MERMAID", "Mermaid", "mErMaId", "mermaid title", "  mermaid", "\tmermaid", "mermaidx", "mermai", "text", "go", ""}
	openers := []string{"```", "~~~", "````", " ```", "  ~~~", "    ```"}
	rng := rand.New(rand.NewPCG(7, 11))
	nonASCII := []string{"İ", "ı", "K", "ſ", "é", "☃", "\u2028"}
	for range 4000 {
		var b strings.Builder
		for j := rng.IntN(4); j >= 0; j-- {
			if rng.IntN(3) == 0 {
				b.WriteString(nonASCII[rng.IntN(len(nonASCII))] + " paragraph\n\n")
			}
			b.WriteString(openers[rng.IntN(len(openers))] + fences[rng.IntN(len(fences))] + "\ngraph TD\n  A --> B\n")
			if rng.IntN(4) != 0 {
				b.WriteString("```\n")
			}
			b.WriteString("\n")
		}
		text := b.String()
		if rng.IntN(5) == 0 {
			text = strings.ReplaceAll(text, "\n", "\r\n")
		}
		normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
		if len(topLevelMermaidFences(normalized)) > 0 && !containsFoldMermaid(normalized) {
			t.Fatalf("the pre-check would skip a text with a mermaid fence:\n%q", normalized)
		}
	}
}

func TestContainsFoldMermaidMatchesAnyLetterCaseOnly(t *testing.T) {
	for text, want := range map[string]bool{
		"": false, "merma": false, "mermai d": false, "mermaid": true, "MERMAID": true, "xx MeRmAiD yy": true,
		"```mermaid\n": true, "ᴍermaid": false, "mermaİd": false, "mermaıd": false,
	} {
		if got := containsFoldMermaid(text); got != want {
			t.Errorf("containsFoldMermaid(%q) = %v, want %v", text, got, want)
		}
	}
}

// A streaming frame without a mermaid word is returned without parsing: the pre-check keeps the per-frame work and allocation of a long answer at nothing,
// whereas the parse allocates a document tree. Frames that do hold a fence still render.
func TestMermaidTransformerSkipsTheParseForTextWithoutTheWord(t *testing.T) {
	plain := strings.Repeat("A paragraph with `code` and **bold** text.\n\n```go\nfmt.Println(1)\n```\n\n", 1500)
	context := extension.MarkdownTransformContext{IsStreaming: true, AvailableWidth: 100, MessageType: extension.MarkdownMessageAssistant}
	if got := transformMermaidBlocks(plain, context, nil); got != plain {
		t.Fatal("a text without the word changed")
	}
	if allocs := testing.AllocsPerRun(20, func() { transformMermaidBlocks(plain, context, nil) }); allocs > 0 {
		t.Fatalf("a %d-byte frame without the word allocated %.0f times, want 0 (the parse was not skipped)", len(plain), allocs)
	}
	withFence := plain + "```MERMAID\ngraph TD\n  A --> B\n```\n"
	if got := transformMermaidBlocks(withFence, context, nil); got == withFence {
		t.Fatal("a MERMAID fence after a long answer was not rendered")
	}
}
