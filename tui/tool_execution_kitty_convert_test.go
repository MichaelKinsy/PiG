package tui

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func withImageCapabilities(t *testing.T, images ImageProtocol) {
	t.Helper()
	prev := GetCapabilities()
	t.Cleanup(func() { SetCapabilities(prev) })
	SetCapabilities(TerminalCapabilities{Images: images, TrueColor: true, Hyperlinks: true})
}

func imageToolComponent(blocks ...ImageBlock) *ToolExecutionComponent {
	c := newToolCardForTest("custom_tool", "")
	c.ImageBlocks = blocks
	c.SetResult("", false, time.Second)
	return c
}

const toolImagePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

// toolImageTranscoder installs a loader like coding-agent's ensurePngTranscoder: on Kitty it registers a transcoder that turns the
// jpeg "final-jpeg" into toolImagePNG, then runs onRegistered. loads counts the loader calls.
func toolImageTranscoder(t *testing.T) (loads *int) {
	t.Helper()
	loads = new(int)
	SetImageTranscoder(nil)
	t.Cleanup(func() {
		SetImageTranscoder(nil)
		SetImageTranscoderLoader(nil)
	})
	SetImageTranscoderLoader(func(onRegistered func()) {
		*loads++
		if GetCapabilities().Images != ImageProtocolKitty || ImageTranscoderRegistered() {
			return
		}
		SetImageTranscoder(func(data, _ string) (string, bool) {
			if data == "final-jpeg" {
				return toolImagePNG, true
			}
			return "", false
		})
		onRegistered()
	})
	return loads
}

// tool-execution-component.test.ts "converts non-PNG tool images once the transcoder loads" (#10292, #8577): the card registers the
// transcoder itself, a replaced partial image does not resurface, and an invalidation reuses the converted Image, so the Kitty image
// ID stays the same.
func TestToolExecutionConvertsNonPNGImagesOnceTheTranscoderLoads(t *testing.T) {
	withImageCapabilities(t, ImageProtocolKitty)
	toolImageTranscoder(t)
	c := newToolCardForTest("tool", "")
	c.UpdateResult(ToolResultUpdate{Content: []ToolResultContent{{Type: "image", Data: "partial-jpeg", MimeType: "image/jpeg"}}}, true)
	c.UpdateResult(ToolResultUpdate{Content: []ToolResultContent{{Type: "image", Data: "final-jpeg", MimeType: "image/jpeg"}}})

	rendered := strings.Join(c.Render(120), "\n")
	if !strings.Contains(rendered, ";"+toolImagePNG) {
		t.Fatalf("converted PNG not rendered:\n%q", rendered)
	}
	if strings.Contains(rendered, "partial-jpeg") {
		t.Fatalf("the replaced partial image resurfaced:\n%q", rendered)
	}
	c.Invalidate()
	if again := strings.Join(c.Render(120), "\n"); again != rendered {
		t.Fatalf("invalidation changed the image (kitty id must stay):\n%q\n%q", again, rendered)
	}
}

// A card asks the loader only for a non-PNG image, and an Image is reused while its source (data, MIME type, width) is unchanged
// (tool-execution.ts imageSources).
func TestToolExecutionImageComponentReuseAndLoaderRequests(t *testing.T) {
	withImageCapabilities(t, ImageProtocolKitty)
	loads := toolImageTranscoder(t)
	c := imageToolComponent(ImageBlock{Data: toolImagePNG, MIMEType: "image/png"})
	c.Render(120)
	if *loads != 0 {
		t.Fatalf("a PNG asked for the transcoder %d times", *loads)
	}
	c = imageToolComponent(ImageBlock{Data: "final-jpeg", MIMEType: "image/jpeg"})
	c.Render(120)
	first := c.imageComponents[0]
	if *loads != 1 {
		t.Fatalf("loader calls = %d, want 1", *loads)
	}
	c.Render(120)
	if c.imageComponents[0] != first {
		t.Fatal("an unchanged source must reuse its Image")
	}
	c.SetImageWidthCells(30)
	c.Render(120)
	if c.imageComponents[0] == first {
		t.Fatal("a changed width must build a new Image")
	}
	c.ImageBlocks = []ImageBlock{{Data: "other-jpeg", MIMEType: "image/jpeg"}}
	c.Render(120)
	if c.imageComponents[0] == first || len(c.imageComponents) != 1 {
		t.Fatalf("a replaced source must build a new Image, got %d", len(c.imageComponents))
	}
}

// tool-execution.ts updateDisplay shows an image only with both data and a MIME type.
func TestToolExecutionSkipsImagesWithoutDataOrMimeType(t *testing.T) {
	withImageCapabilities(t, ImageProtocolKitty)
	toolImageTranscoder(t)
	c := imageToolComponent(ImageBlock{Data: "", MIMEType: "image/png"}, ImageBlock{Data: toolImagePNG, MIMEType: ""})
	plain := newToolCardForTest("custom_tool", "")
	plain.SetResult("", false, time.Second)
	if got, want := c.Render(120), plain.Render(120); !slices.Equal(got, want) || len(c.imageComponents) != 0 {
		t.Fatalf("rows = %q, want the card without images %q", got, want)
	}
}

// image.ts render (1.0.1) through the card: on Kitty a non-PNG image shows its text fallback after the spacer until the transcoder
// is registered, then the converted PNG; a failing conversion keeps the fallback.
func TestKittyRendersNonPNGOnlyOnceTheTranscoderIsRegistered(t *testing.T) {
	withImageCapabilities(t, ImageProtocolKitty)
	SetImageTranscoder(nil)
	t.Cleanup(func() { SetImageTranscoder(nil) })
	c := imageToolComponent(ImageBlock{Data: "final-jpeg", MIMEType: "image/jpeg"})
	before := c.Render(120)
	if got := strings.Join(before, "\n"); strings.Contains(got, "\x1b_G") || !strings.Contains(got, "[Image: [image/jpeg]") {
		t.Fatalf("unconverted jpeg on kitty:\n%q", got)
	}
	noImage := newToolCardForTest("custom_tool", "")
	noImage.SetResult("", false, time.Second)
	if len(before) != len(noImage.Render(120))+2 {
		t.Fatalf("fallback rows: %d vs %d without the image", len(before), len(noImage.Render(120)))
	}
	SetImageTranscoder(func(string, string) (string, bool) { return toolImagePNG, true })
	c.Invalidate()
	if !c.IsDirty() {
		t.Fatal("invalidating did not mark the component dirty")
	}
	after := strings.Join(c.Render(120), "\n")
	if !strings.Contains(after, ";"+toolImagePNG) || !strings.Contains(after, "\x1b_G") {
		t.Fatalf("converted png not rendered:\n%q", after)
	}
	// upstream: tool-execution-component.test.ts:61-65: invalidation reuses the converted Image, so the Kitty image ID stays the same.
	c.Invalidate()
	if again := strings.Join(c.Render(120), "\n"); again != after {
		t.Fatalf("render after Invalidate changed the converted image:\n%q\nwant\n%q", again, after)
	}
}

// iTerm2 displays any format, so a non-PNG image renders unconverted there.
func TestITerm2RendersNonPNGWithoutConversion(t *testing.T) {
	withImageCapabilities(t, ImageProtocolITerm2)
	c := imageToolComponent(ImageBlock{Data: "jpeg-data", MIMEType: "image/jpeg"})
	if got := strings.Join(c.Render(120), "\n"); !strings.Contains(got, "jpeg-data") {
		t.Fatalf("iTerm2 did not render the jpeg:\n%q", got)
	}
}
