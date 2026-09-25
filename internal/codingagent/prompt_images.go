package codingagent

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/imageprocessing"
)

// NormalizePromptContent applies the selected model's profile once, before new
// images enter history. Prompt failures become text hints, unlike tool results.
func NormalizePromptContent(content []ai.UserContentBlock, autoResize bool, model *ai.Model) []ai.UserContentBlock {
	var options *ai.ModelImageResizeOptions
	if model != nil && model.InputLimits != nil && model.InputLimits.Images != nil {
		options = model.InputLimits.Images.Resize
	}
	normalized := make([]ai.UserContentBlock, 0, len(content))
	var hints []string
	for _, block := range content {
		image, ok := block.(ai.ImageContent)
		if !ok {
			normalized = append(normalized, block)
			continue
		}
		decoded := imageprocessing.DecodeNodeBase64(image.Data)
		data, mime, hint, err := imageprocessing.ProcessImage(decoded, image.MimeType, autoResize, options)
		if err != nil {
			hints = append(hints, err.Error())
			continue
		}
		normalized = append(normalized, ai.ImageContent{Data: base64.StdEncoding.EncodeToString(data), MimeType: mime})
		if hint != "" {
			hints = append(hints, hint)
		}
	}
	if len(hints) == 0 {
		return normalized
	}
	hintText := "\n\n" + strings.Join(hints, "\n")
	for i, block := range normalized {
		if text, ok := block.(ai.TextContent); ok {
			text.Text += hintText
			normalized[i] = text
			return normalized
		}
	}
	return append([]ai.UserContentBlock{ai.TextContent{Text: hintText}}, normalized...)
}

func extensionImages(images []ai.ImageContent) []extension.ImageContent {
	if images == nil {
		return nil
	}
	result := make([]extension.ImageContent, len(images))
	for i, image := range images {
		result[i] = image
	}
	return result
}

func imagesFromExtension(images []extension.ImageContent) []ai.ImageContent {
	if images == nil {
		return nil
	}
	result := make([]ai.ImageContent, 0, len(images))
	for _, image := range images {
		if typed, ok := image.(ai.ImageContent); ok {
			result = append(result, typed)
			continue
		}
		data, err := json.Marshal(image)
		if err != nil {
			continue
		}
		var decoded ai.ImageContent
		if json.Unmarshal(data, &decoded) == nil {
			result = append(result, decoded)
		}
	}
	return result
}

func promptContent(text string, images []ai.ImageContent) []ai.UserContentBlock {
	content := make([]ai.UserContentBlock, 0, len(images))
	content = append(content, ai.TextContent{Text: text})
	for _, image := range images {
		content = append(content, image)
	}
	return content
}
