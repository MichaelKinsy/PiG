package tui

import (
	"reflect"
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
	c := NewToolExecutionComponent("custom_tool", "")
	c.ImageBlocks = blocks
	c.SetResult("", false, time.Second)
	return c
}

// Mirrors upstream tool-execution-component.test.ts "keeps the final tool image
// when a partial image conversion finishes late" (issue #8577).
func TestToolExecutionKeepsFinalImageWhenPartialConversionFinishesLate(t *testing.T) {
	withImageCapabilities(t, ImageProtocolKitty)
	c := NewToolExecutionComponent("custom_tool", "")
	c.ImageBlocks = []ImageBlock{{Data: "partial-jpeg", MIMEType: "image/jpeg"}}
	pending := c.PendingKittyImageConversions()
	if len(pending) != 1 {
		t.Fatalf("pending = %+v, want the partial jpeg", pending)
	}

	c.ImageBlocks = []ImageBlock{{Data: "final-png", MIMEType: "image/png"}}
	c.SetResult("", false, 0)
	if got := strings.Join(c.Render(120), "\n"); !strings.Contains(got, "final-png") {
		t.Fatalf("final png not rendered:\n%q", got)
	}

	if c.ApplyConvertedImage(pending[0], &ConvertedImage{Data: "converted-partial", MimeType: "image/png"}) {
		t.Fatal("late conversion of a replaced image was applied")
	}
	rendered := strings.Join(c.Render(120), "\n")
	if !strings.Contains(rendered, "final-png") || strings.Contains(rendered, "converted-partial") {
		t.Fatalf("render after late conversion:\n%q", rendered)
	}
}

func TestPendingKittyImageConversionsSelectsOnlyUnconvertedNonPNG(t *testing.T) {
	withImageCapabilities(t, ImageProtocolKitty)
	c := imageToolComponent(
		ImageBlock{Data: "png-data", MIMEType: "image/png"},
		ImageBlock{Data: "jpeg-data", MIMEType: "image/jpeg"},
		ImageBlock{Data: "", MIMEType: "image/gif"},
		ImageBlock{Data: "no-mime", MIMEType: ""},
		ImageBlock{Data: "webp-data", MIMEType: "image/webp"},
	)
	want := []KittyImageConversion{
		{Index: 1, Data: "jpeg-data", MimeType: "image/jpeg"},
		{Index: 4, Data: "webp-data", MimeType: "image/webp"},
	}
	if got := c.PendingKittyImageConversions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("pending = %+v, want %+v", got, want)
	}

	// A cached conversion for the current source is not converted again.
	if !c.ApplyConvertedImage(want[0], &ConvertedImage{Data: "jpeg-as-png", MimeType: "image/png"}) {
		t.Fatal("conversion for the current source was not applied")
	}
	if got := c.PendingKittyImageConversions(); !reflect.DeepEqual(got, want[1:]) {
		t.Fatalf("pending after cache = %+v, want %+v", got, want[1:])
	}

	// A new source at the same index invalidates the cached entry.
	c.ImageBlocks[1] = ImageBlock{Data: "jpeg-data-2", MIMEType: "image/jpeg"}
	wantAgain := []KittyImageConversion{
		{Index: 1, Data: "jpeg-data-2", MimeType: "image/jpeg"},
		{Index: 4, Data: "webp-data", MimeType: "image/webp"},
	}
	if got := c.PendingKittyImageConversions(); !reflect.DeepEqual(got, wantAgain) {
		t.Fatalf("pending after source change = %+v, want %+v", got, wantAgain)
	}
}

func TestPendingKittyImageConversionsRequiresKitty(t *testing.T) {
	for _, images := range []ImageProtocol{"", ImageProtocolITerm2} {
		withImageCapabilities(t, images)
		c := imageToolComponent(ImageBlock{Data: "jpeg-data", MIMEType: "image/jpeg"})
		if got := c.PendingKittyImageConversions(); got != nil {
			t.Fatalf("images=%q pending = %+v, want none", images, got)
		}
	}
}

func TestApplyConvertedImageIgnoresFailureAndOutOfRange(t *testing.T) {
	withImageCapabilities(t, ImageProtocolKitty)
	c := imageToolComponent(ImageBlock{Data: "jpeg-data", MIMEType: "image/jpeg"})
	req := KittyImageConversion{Index: 0, Data: "jpeg-data", MimeType: "image/jpeg"}
	if c.ApplyConvertedImage(req, nil) {
		t.Fatal("failed (nil) conversion was applied")
	}
	if c.ApplyConvertedImage(KittyImageConversion{Index: 3, Data: "jpeg-data", MimeType: "image/jpeg"}, &ConvertedImage{Data: "x", MimeType: "image/png"}) {
		t.Fatal("out-of-range conversion was applied")
	}
	if c.ApplyConvertedImage(KittyImageConversion{Index: 0, Data: "jpeg-data", MimeType: "image/gif"}, &ConvertedImage{Data: "x", MimeType: "image/png"}) {
		t.Fatal("conversion for a different source MIME was applied")
	}
	if len(c.PendingKittyImageConversions()) != 1 {
		t.Fatal("ignored conversions must leave the image pending")
	}
}

// Upstream updateDisplay skips a non-PNG image on Kitty (no spacer, no image)
// until its conversion lands, then renders the converted PNG.
func TestKittyRendersNonPNGOnlyAfterConversion(t *testing.T) {
	withImageCapabilities(t, ImageProtocolKitty)
	c := imageToolComponent(ImageBlock{Data: "jpeg-data", MIMEType: "image/jpeg"})
	before := c.Render(120)
	if got := strings.Join(before, "\n"); strings.Contains(got, "jpeg-data") || strings.Contains(got, "\x1b_G") {
		t.Fatalf("unconverted jpeg rendered on kitty:\n%q", got)
	}
	noImage := NewToolExecutionComponent("custom_tool", "")
	noImage.SetResult("", false, time.Second)
	if len(before) != len(noImage.Render(120)) {
		t.Fatalf("skipped image left rows: %d vs %d", len(before), len(noImage.Render(120)))
	}

	pending := c.PendingKittyImageConversions()
	if !c.ApplyConvertedImage(pending[0], &ConvertedImage{Data: "converted-png", MimeType: "image/png"}) {
		t.Fatal("conversion not applied")
	}
	if !c.IsDirty() {
		t.Fatal("applied conversion did not invalidate the component")
	}
	after := strings.Join(c.Render(120), "\n")
	if !strings.Contains(after, "converted-png") || !strings.Contains(after, "\x1b_G") || strings.Contains(after, "jpeg-data") {
		t.Fatalf("converted png not rendered:\n%q", after)
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
