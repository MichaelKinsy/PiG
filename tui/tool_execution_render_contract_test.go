package tui

// pi: packages/coding-agent/src/modes/interactive/components/tool-execution.ts ToolExecutionComponent.render (236-268)

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const toolRenderContractPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

type fixedStreamedArgs struct{ fields map[string]any }

func (a fixedStreamedArgs) Object() map[string]any { return a.fields }
func (a fixedStreamedArgs) JSON() json.RawMessage {
	raw, _ := json.Marshal(a.fields)
	return raw
}

// A renderShell "self" card draws its components after one blank row, then for each image a one-row spacer and the image rows; with no components it
// draws the spacer and images without the blank row, and with neither it draws no rows.
func TestSelfShellCardDrawsBlankRowComponentsThenImages(t *testing.T) {
	withImageCapabilities(t, ImageProtocolKitty)
	const width = 30
	image := NewImage(toolRenderContractPNG, "image/png", ImageTheme{}, ImageOptions{}, nil)
	imageRows := len(image.Render(width))
	if imageRows == 0 {
		t.Fatal("the probe image renders no rows")
	}
	build := func(call string, images bool) *ToolExecutionComponent {
		card := newToolCardForTest("self", "")
		card.SetDefinition(&ToolDefinitionRenderers{
			Self: true,
			Call: func(ToolRenderInput) (Component, bool) { return NewPaddedText(call, 0, 0, nil), true },
		}, nil)
		if images {
			card.ShowImages = true
			card.ImageBlocks = []ImageBlock{{Data: toolRenderContractPNG, MIMEType: "image/png"}}
		}
		return card
	}
	t.Run("components and an image", func(t *testing.T) {
		got := build("first\nsecond", true).Render(width)
		if len(got) != 1+2+1+imageRows {
			t.Fatalf("rows = %d (%q), want blank + 2 + spacer + %d image rows", len(got), got, imageRows)
		}
		if got[0] != "" || !strings.Contains(got[1], "first") || !strings.Contains(got[2], "second") || got[3] != "" {
			t.Fatalf("rows = %q, want a blank row, the components, then the image spacer", got[:4])
		}
	})
	t.Run("an image and no components", func(t *testing.T) {
		got := build("", true).Render(width)
		if len(got) != 1+imageRows || got[0] != "" {
			t.Fatalf("rows = %d (%q), want the spacer and %d image rows without a leading blank row", len(got), got, imageRows)
		}
	})
	t.Run("components and no image", func(t *testing.T) {
		got := plainLines(build("first", false).Render(width))
		if !slices.Equal(got, []string{"", "first"}) {
			t.Fatalf("rows = %q, want a blank row and the component", got)
		}
	})
	t.Run("nothing", func(t *testing.T) {
		if got := build("", false).Render(width); len(got) != 0 {
			t.Fatalf("rows = %q, want none", got)
		}
	})
}

// Streamed arguments are applied when the card renders: a render after new arguments draws them without any other call.
func TestToolCardAppliesStreamedArgumentsAtRender(t *testing.T) {
	card := NewToolExecutionComponent("write", "id", nil, ToolExecutionOptions{}, nil, nil, "")
	card.SetStreamedArgs(fixedStreamedArgs{fields: map[string]any{"path": "first.txt"}})
	if got := strings.Join(plainLines(card.Render(60)), "\n"); !strings.Contains(got, "first.txt") {
		t.Fatalf("card = %q, want the streamed path", got)
	}
	card.SetStreamedArgs(fixedStreamedArgs{fields: map[string]any{"path": "second.txt"}})
	if got := strings.Join(plainLines(card.Render(60)), "\n"); !strings.Contains(got, "second.txt") || strings.Contains(got, "first.txt") {
		t.Fatalf("card = %q, want only the latest streamed path", got)
	}
}
