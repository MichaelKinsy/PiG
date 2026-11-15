package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// Ports packages/durable/src/tools/read.ts (readImage, imageModel, imageError) and the limits of image-processor.ts.

// imageModel is the conversation's model as images see it: its limits, and its id if it has no image input.
type imageModel struct {
	limits ImageLimits
	// imageUnsupportedByModel is nil for a model with image input, and without a resolvable model.
	imageUnsupportedByModel *string
}

// imageModelOf is the conversation model's image limits (ImageLimitsOf), and its id if it has no image input. Without a
// resolvable model, the defaults and no id.
func imageModelOf(ctx context.Context, api durable.ToolExecutionApi) (imageModel, error) {
	agent, err := api.Agent(ctx)
	if err != nil {
		return imageModel{}, err
	}
	var model *ai.Model
	if agent.Model != nil {
		model = api.Models().GetModel(agent.Model.Provider, agent.Model.ModelId)
	}
	out := imageModel{limits: ImageLimitsOf(model)}
	if model == nil {
		return out, nil
	}
	if !slices.Contains(model.Input, "image") {
		id := model.ID
		out.imageUnsupportedByModel = &id
	}
	return out, nil
}

// readImage is one image block, prepared by processor or, without one, as the file is when its format is inline and it
// fits the limits (its size is checked before reading it, its dimensions from its header); otherwise an error result saying
// why. What happened to the image is an info diagnostic. A model without image input sees pi-ai's placeholder instead,
// decided per request; details names it for UIs.
func readImage(ctx context.Context, reader env.BinaryReader, info env.FileInfo, path, mimeType string, processor ImageProcessor, model imageModel) (durable.ToolExecutionResult, error) {
	var image ai.ImageContent
	var notes []string
	if processor != nil {
		bytes, err := reader.Read(ctx, 0, info.Size)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		prepared, err := processor.Prepare(ctx, bytes, mimeType, model.limits)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if prepared == nil {
			return imageError(fmt.Sprintf("%s is an image (%s) that cannot be prepared for the model", path, mimeType)), nil
		}
		image = ai.ImageContent{Data: prepared.Data, MimeType: prepared.MimeType}
		notes = append(notes, PreparedNotes(prepared)...)
	} else {
		if !INLINE_IMAGE_TYPES[mimeType] {
			return imageError(fmt.Sprintf("%s is an image (%s) that needs converting, and no image processor is configured", path, mimeType)), nil
		}
		const unshrinkable = "and no image processor is configured to shrink it"
		if Base64Length(info.Size) > int64(model.limits.MaxBytes) {
			return imageError(fmt.Sprintf("%s is an image (%s) of %s, too large to send, %s", path, mimeType, durable.FormatSize(int(info.Size)), unshrinkable)), nil
		}
		bytes, err := reader.Read(ctx, 0, info.Size)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		width, height, ok := imageDimensions(bytes, mimeType)
		if !ok {
			return imageError(fmt.Sprintf("%s is an image (%s) whose size cannot be read", path, mimeType)), nil
		}
		if width > model.limits.MaxWidth || height > model.limits.MaxHeight {
			return imageError(fmt.Sprintf("%s is an image (%s) of %dx%d, larger than %dx%d, %s", path, mimeType, width, height, model.limits.MaxWidth, model.limits.MaxHeight, unshrinkable)), nil
		}
		image = ai.ImageContent{Data: ToBase64(bytes), MimeType: mimeType}
	}
	message := strings.Join(append([]string{fmt.Sprintf("Read image file [%s].", image.MimeType)}, notes...), " ")
	result := durable.ToolExecutionResult{
		Output:              []ai.ToolResultMessageContent{image},
		StructuredOutput:    delta.JsonObjectOf("type", "image", "data", image.Data, "mimeType", image.MimeType),
		HasStructuredOutput: true,
		Diagnostics:         []durable.ToolDiagnostic{{Severity: durable.SeverityInfo, Code: "image", Message: message}},
	}
	if model.imageUnsupportedByModel != nil {
		encoded, err := durable.ToJsonValue(&ReadToolDetails{ImageUnsupportedByModel: model.imageUnsupportedByModel})
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		result.Details, result.HasDetails = encoded, true
	}
	return result, nil
}

func imageError(message string) durable.ToolExecutionResult {
	isError := true
	return durable.ToolExecutionResult{
		Output:      []ai.ToolResultMessageContent{},
		IsError:     &isError,
		Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityError, Code: "unsupported_image", Message: message}},
	}
}
