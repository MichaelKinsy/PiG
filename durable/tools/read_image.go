package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/durable/src/tools/read.ts (readImage, imageModel, imageError) and the limits of image-processor.ts.

// imageModel is what the conversation's model takes: whether images, and within which limits.
type imageModel struct {
	vision bool
	limits ImageLimits
}

// imageModelOf is the conversation model's image input: the defaults overridden by its inputLimits.images.resize. Without a
// resolvable model, it counts as one that takes images within the defaults.
func imageModelOf(ctx context.Context, api durable.ToolExecutionApi) (imageModel, error) {
	agent, err := api.Agent(ctx)
	if err != nil {
		return imageModel{}, err
	}
	var model *ai.Model
	if agent.Model != nil {
		model = api.Models().GetModel(agent.Model.Provider, agent.Model.ModelId)
	}
	out := imageModel{vision: true, limits: DEFAULT_IMAGE_LIMITS}
	if model == nil {
		return out, nil
	}
	out.vision = slices.Contains(model.Input, "image")
	if model.InputLimits != nil && model.InputLimits.Images != nil && model.InputLimits.Images.Resize != nil {
		resize := model.InputLimits.Images.Resize
		if resize.MaxWidth != 0 {
			out.limits.MaxWidth = resize.MaxWidth
		}
		if resize.MaxHeight != 0 {
			out.limits.MaxHeight = resize.MaxHeight
		}
		if resize.MaxBytes != 0 {
			out.limits.MaxBytes = resize.MaxBytes
		}
	}
	return out, nil
}

// readImage is one image block, prepared by processor or, without one, as the file is when its format is inline and it
// fits the limits (its size is checked before reading it, its dimensions from its header); otherwise an error result saying
// why. What happened to the image is an info diagnostic. A model without image input sees pi-ai's placeholder instead; a
// diagnostic says so.
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
		if prepared.ConvertedFrom != "" {
			notes = append(notes, fmt.Sprintf("Converted from %s to %s.", prepared.ConvertedFrom, prepared.MimeType))
		}
		if prepared.Resized != nil {
			from, to := prepared.Resized.From, prepared.Resized.To
			scale := jsstring.ToFixed(float64(from.Width)/float64(to.Width), 2)
			notes = append(notes, fmt.Sprintf("Resized from %dx%d to %dx%d. Multiply coordinates by %s to map them to the original.", from.Width, from.Height, to.Width, to.Height, scale))
		}
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
	if !model.vision {
		notes = append(notes, "The current model does not support images; it sees a placeholder instead.")
	}
	message := strings.Join(append([]string{fmt.Sprintf("Read image file [%s].", image.MimeType)}, notes...), " ")
	return durable.ToolExecutionResult{
		Output:      []ai.ToolResultMessageContent{image},
		Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityInfo, Code: "image", Message: message}},
	}, nil
}

func imageError(message string) durable.ToolExecutionResult {
	isError := true
	return durable.ToolExecutionResult{
		Output:      []ai.ToolResultMessageContent{},
		IsError:     &isError,
		Diagnostics: []durable.ToolDiagnostic{{Severity: durable.SeverityError, Code: "unsupported_image", Message: message}},
	}
}
