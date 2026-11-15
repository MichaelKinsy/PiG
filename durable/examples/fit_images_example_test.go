// Ports packages/durable/test/examples/47-fit-images.ts: images fitted to the model's limits before they are stored, so the
// transcript (and the provider's prompt cache) never changes later. The host fits the photo a person attaches with
// fit.FitImage before it submits the message, and the fit-images extension fits a screenshot a tool returns before its
// result is committed. The model takes images up to 1000 pixels wide here. The script's printing is the transcript this
// test reads: the sizes and notes of what the entries hold.

package examples_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/images/fit"
	"github.com/MichaelKinsy/PiG/durable/images/node"
	"github.com/MichaelKinsy/PiG/durable/storage"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// greyPng is a width by height grey PNG, enough to show sizes.
func greyPng(t *testing.T, width, height int) []byte {
	t.Helper()
	grey := image.NewGray(image.Rect(0, 0, width, height))
	for index := range grey.Pix {
		grey.Pix[index] = 128
	}
	var out bytes.Buffer
	if err := png.Encode(&out, grey); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// sizeOf is an image block's size and type, from its header.
func sizeOf(t *testing.T, block ai.ImageContent) string {
	t.Helper()
	raw, err := tools.FromBase64(block.Data)
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%dx%d %s", config.Width, config.Height, block.MimeType)
}

// Pi: packages/durable/src/images/fit.ts (fitImage, createFitImagesExtension).
func TestExample47FitImages(t *testing.T) {
	// The faux provider plays a model whose images may be at most 1000 pixels wide: it asks for a screenshot, then answers.
	faux := ai.NewFauxProvider(ai.FauxConfig{Models: []ai.FauxModelDefinition{{
		ID:          "faux-1",
		Input:       []string{"text", "image"},
		InputLimits: &ai.ModelInputLimits{Images: &ai.ModelImageInputLimits{Resize: &ai.ModelImageResizeOptions{MaxWidth: 1000}}},
	}}})
	models := ai.CreateModels()
	models.SetProvider(faux.Provider())
	faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("screenshot", map[string]any{}, &ai.FauxToolCallOptions{ID: "s"})}, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("Both look grey.")}}),
	})

	// One processor (Photon, in a worker thread) for the extension and the host's own input.
	images, err := node.CreateNodePhotonImages(nil)
	if err != nil {
		t.Fatal(err)
	}
	screenshot := harness.DefineTool(durable.ToolRegistration{
		Name: "screenshot", Description: "Take a screenshot", Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
		Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.ImageContent{Data: tools.ToBase64(greyPng(t, 3000, 1000)), MimeType: "image/png"}}}, nil
		},
	})
	registry := harness.CreateRegistry()
	if err := registry.Install(new(durable.Extension{Name: "screen", Tools: []*durable.ToolRegistration{screenshot}})); err != nil {
		t.Fatal(err)
	}
	if err := registry.Install(fit.CreateFitImagesExtension(images)); err != nil {
		t.Fatal(err)
	}

	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{Models: models, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close(background) })
	root, err := opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-1"})}})
	if err != nil {
		t.Fatal(err)
	}

	// The host fits the attached photo to the conversation model's limits before it submits the message.
	model := models.GetModel("faux", "faux-1")
	photo := fit.FitImage(background, greyPng(t, 4000, 3000), "image/png", tools.ImageLimitsOf(model), images)
	if photo.Kind == fit.FittedImageOmitted {
		t.Fatal(photo.Note)
	}
	text := "Compare this photo with the screen."
	for _, note := range photo.Notes {
		text += "\n" + note
	}
	submission, err := root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserContentBlocks{ai.TextContent{Text: text}, photo.Image}})
	if err != nil {
		t.Fatal(err)
	}
	settled, err := submission.Wait(background)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != durable.SubmissionDone {
		t.Fatalf("status %v, want done", settled.Status)
	}

	// What the transcript holds, and so what every later request replays.
	page, err := root.Entries(background, durable.EntryQuery{}, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	var printed []string
	for _, entry := range slices.Backward(page.Items) {
		if len(entry.Model) == 0 {
			continue
		}
		switch message := entry.Model[0].(type) {
		case ai.UserMessage:
			blocks, ok := message.Content.(ai.UserContentBlocks)
			if !ok {
				continue
			}
			for _, item := range blocks {
				switch block := item.(type) {
				case ai.ImageContent:
					printed = append(printed, "user: "+sizeOf(t, block))
				case ai.TextContent:
					printed = append(printed, fmt.Sprintf("user: %q", block.Text))
				}
			}
		case ai.ToolResultMessage:
			for _, item := range message.Content {
				switch block := item.(type) {
				case ai.ImageContent:
					printed = append(printed, "tool result: "+sizeOf(t, block))
				case ai.TextContent:
					printed = append(printed, fmt.Sprintf("tool result: %q", block.Text))
				}
			}
		}
	}
	want := []string{
		`user: "Compare this photo with the screen.\nResized from 4000x3000 to 1000x750. Multiply coordinates by 4.00 to map them to the original."`,
		"user: 1000x750 image/png",
		"tool result: 1000x333 image/png",
		`tool result: "<harness>\n[info] Resized from 3000x1000 to 1000x333. Multiply coordinates by 3.00 to map them to the original.\n</harness>"`,
	}
	if !slices.Equal(printed, want) {
		t.Fatalf("transcript\n%q\nwant\n%q", printed, want)
	}
}
