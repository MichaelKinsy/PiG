package ai

import (
	"encoding/json"
	"net/http"
	"strings"
)

// isOpenRouterImagesAPIOnlyError reports OpenRouter's rejection of an image generation model sent to chat/completions:
// "<model> is an image generation model and cannot be used with the chat/completions endpoint. Use the /api/v1/images endpoint instead."
func isOpenRouterImagesAPIOnlyError(status int, body []byte) bool {
	if status != http.StatusNotFound && status != http.StatusBadRequest {
		return false
	}
	text := strings.ToLower(string(body))
	return strings.Contains(text, "cannot be used with the chat/completions endpoint") && strings.Contains(text, "/images")
}

// buildOpenRouterImagesAPIPayload is the OpenRouter Images API request (POST /images): the text input becomes the prompt and the image
// input becomes input_references.
func buildOpenRouterImagesAPIPayload(model ImageModel, imagesCtx ImagesContext) map[string]any {
	var prompt []string
	references := []map[string]any{}
	for _, item := range imagesCtx.Input {
		switch v := item.(type) {
		case TextContent:
			prompt = append(prompt, v.Text)
		case ImageContent:
			mediaType := v.MimeType
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}
			references = append(references, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:" + mediaType + ";base64," + v.Data}})
		}
	}
	payload := map[string]any{"model": model.ID, "prompt": strings.Join(prompt, "\n")}
	if len(references) > 0 {
		payload["input_references"] = references
	}
	return payload
}

type openRouterImagesAPIResponse struct {
	Data []struct {
		B64JSON   string `json:"b64_json"`
		MediaType string `json:"media_type"`
	} `json:"data"`
	Usage *openRouterImagesUsage `json:"usage"`
}

// finishOpenRouterImagesAPIResponse turns a successful Images API body into the result, after the same response callback the chat path runs.
func finishOpenRouterImagesAPIResponse(model ImageModel, options ProviderImagesOptions, resp *http.Response, data []byte, out AssistantImages) AssistantImages {
	var parsed openRouterImagesAPIResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = err.Error()
		return out
	}
	if options.OnResponse != nil {
		if err := options.OnResponse(ProviderResponse{Status: resp.StatusCode, Headers: headersToRecord(resp.Header)}, model); err != nil {
			out.StopReason = ImagesStopReasonError
			out.ErrorMessage = err.Error()
			return out
		}
	}
	if parsed.Usage != nil {
		out.Usage = parseOpenRouterImagesUsage(*parsed.Usage, model)
	}
	for _, image := range parsed.Data {
		if image.B64JSON == "" {
			continue
		}
		mediaType := image.MediaType
		if mediaType == "" {
			mediaType = "image/png"
		}
		out.Output = append(out.Output, ImageContent{MimeType: mediaType, Data: image.B64JSON})
	}
	return out
}
