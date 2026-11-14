// Package fit is `@earendil-works/pi-durable/images/fit`: images entering a conversation, fitted to its model's limits by
// any tools.ImageProcessor before they are stored, so the transcript (and the provider's prompt cache) never changes
// later: a host's input before it is submitted (FitImage, FitImages), and the results of the model's tool calls
// (CreateFitImagesExtension). It does not load an image library; pass a processor.
//
// Ports packages/durable/src/images/fit.ts.
package fit

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/tools"
)

// The kinds of FittedImage and FittedImages.
const (
	// FittedImageFitted is the kind of a FittedImage with its image and notes.
	FittedImageFitted = "fitted"
	// FittedImageOmitted is the kind of a FittedImage that was left out, with its note.
	FittedImageOmitted = "omitted"
	// FittedImagesUnchanged is the kind of FittedImages when every image came back as it was.
	FittedImagesUnchanged = "unchanged"
	// FittedImagesFitted is the kind of FittedImages with the content fitted and notes for the model.
	FittedImagesFitted = "fitted"
)

// FittedImage is one image fitted by FitImage, with notes for the model saying what changed; or why it was left out.
// Kind is FittedImageFitted (Image, Notes) or FittedImageOmitted (Note).
type FittedImage struct {
	Kind  string
	Image ai.ImageContent
	Notes []string
	Note  string
}

// FittedImages is what FitImages did: nothing, or the content with its images fitted and notes for the model. Kind is
// FittedImagesUnchanged, or FittedImagesFitted (Content, Notes).
type FittedImages struct {
	Kind    string
	Content []ai.ToolResultMessageContent
	Notes   []string
}

// FitImage is the image in bytes, declared as mimeType, prepared by processor to fit limits. Every image is decoded, as the
// coding agent's are: one the provider would reject (damaged, or too large) never reaches the transcript, where it would
// fail every later request. Its type is read from its bytes, since providers reject a declared type that differs. An image
// the processor cannot prepare, or fails on, is left out with a note.
func FitImage(ctx context.Context, bytes []byte, mimeType string, limits tools.ImageLimits, processor tools.ImageProcessor) FittedImage {
	detected := tools.DetectSupportedImageMimeType(bytes)
	if detected == "" {
		detected = mimeType
	}
	prepared, err := processor.Prepare(ctx, bytes, detected, limits)
	if err != nil {
		// The image is known to need preparing: keeping it would commit what the provider rejects. The note carries the
		// failure into the transcript.
		return omitted(mimeType, ": "+durable.ErrorMessage(err))
	}
	if prepared == nil {
		return omitted(mimeType, "")
	}
	return FittedImage{
		Kind:  FittedImageFitted,
		Image: ai.ImageContent{Data: prepared.Data, MimeType: prepared.MimeType},
		Notes: tools.PreparedNotes(prepared),
	}
}

func omitted(mimeType, reason string) FittedImage {
	return FittedImage{
		Kind: FittedImageOmitted,
		Note: fmt.Sprintf("An image (%s) cannot be prepared for the model and was left out%s.", mimeType, reason),
	}
}

// FitImages is content with each image fitted by FitImage (malformed base64 is left out as an image that cannot be
// prepared); text stays where it is. FittedImagesUnchanged when every image came back as it was.
func FitImages(ctx context.Context, content []ai.ToolResultMessageContent, limits tools.ImageLimits, processor tools.ImageProcessor) FittedImages {
	fitted := make([]ai.ToolResultMessageContent, 0, len(content))
	notes := []string{}
	changed := false
	for _, item := range content {
		image, isImage := item.(ai.ImageContent)
		if !isImage {
			fitted = append(fitted, item)
			continue
		}
		result := fitBase64(ctx, image, limits, processor)
		switch {
		case result.Kind == FittedImageOmitted:
			changed = true
			notes = append(notes, result.Note)
		case result.Image.Data == image.Data && result.Image.MimeType == image.MimeType:
			fitted = append(fitted, item)
		default:
			changed = true
			fitted = append(fitted, result.Image)
			notes = append(notes, result.Notes...)
		}
	}
	if !changed {
		return FittedImages{Kind: FittedImagesUnchanged}
	}
	return FittedImages{Kind: FittedImagesFitted, Content: fitted, Notes: notes}
}

func fitBase64(ctx context.Context, image ai.ImageContent, limits tools.ImageLimits, processor tools.ImageProcessor) FittedImage {
	bytes, err := tools.FromBase64(image.Data)
	if err != nil {
		// Wire input: a tool's base64 may be malformed.
		return omitted(image.MimeType, ": "+durable.ErrorMessage(err))
	}
	return FitImage(ctx, bytes, image.MimeType, limits, processor)
}

// CreateFitImagesExtension is the `fit-images` extension: an afterTool hook that fits the images of every result the model
// gets to the conversation model's current limits (FitImages) before it is committed, whichever tool made them (an MCP
// tool, code mode's image()), and says what changed in an info diagnostic (code image). Hooks of extensions installed before
// it see the images unfitted. Nested calls' results are left as they are: the program that made the call gets them, not the
// model.
func CreateFitImagesExtension(processor tools.ImageProcessor) *durable.Extension {
	return new(durable.Extension{
		Name: "fit-images",
		Hooks: []durable.HookRegistration{
			harness.Hook(harness.ToolTask, &harness.ToolHooks{
				AfterTool: func(ctx context.Context, call harness.ToolHookCall, result durable.ToolExecutionResult, api harness.HookApi) (*durable.ToolExecutionResult, error) {
					return fitResult(ctx, call, result, processor, api)
				},
			}),
		},
	})
}

func fitResult(ctx context.Context, call harness.ToolHookCall, result durable.ToolExecutionResult, processor tools.ImageProcessor, api harness.HookApi) (*durable.ToolExecutionResult, error) {
	if call.Parent != nil || !slices.ContainsFunc(result.Output, func(item ai.ToolResultMessageContent) bool {
		_, isImage := item.(ai.ImageContent)
		return isImage
	}) {
		return nil, nil
	}
	state, err := durable.Snapshot[harness.AgentState](ctx, api, harness.AgentDoc, api.ConversationId())
	if err != nil {
		return nil, err
	}
	var model *ai.Model
	if state != nil && state.Model != nil {
		model = api.Models().GetModel(state.Model.Provider, state.Model.ModelId)
	}
	fitted := FitImages(ctx, result.Output, tools.ImageLimitsOf(model), processor)
	if fitted.Kind == FittedImagesUnchanged {
		return nil, nil
	}
	out := result
	out.Output = fitted.Content
	if len(fitted.Notes) > 0 {
		note := durable.ToolDiagnostic{Severity: durable.SeverityInfo, Code: "image", Message: strings.Join(fitted.Notes, " ")}
		out.Diagnostics = append(slices.Clone(result.Diagnostics), note)
	}
	return &out, nil
}
