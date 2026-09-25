package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"time"
)

// RegisterBuiltInImagesAPIProviders registers upstream's built-in image API
// providers. Currently upstream ships OpenRouter image generation only.
func RegisterBuiltInImagesAPIProviders() {
	RegisterImagesAPIProvider(ImagesAPIProvider{
		API:            APIImagesOpenRouter,
		GenerateImages: GenerateImagesOpenRouter,
	})
}

// GenerateImagesOpenRouter implements upstream providers/images/openrouter.ts.
func GenerateImagesOpenRouter(ctx context.Context, model ImagesModel, imagesCtx ImagesContext, options ProviderImagesOptions) AssistantImages {
	out := AssistantImages{
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Output:     []ContentBlock{},
		StopReason: ImagesStopReasonStop,
		Timestamp:  time.Now().UnixMilli(),
	}

	// Upstream openrouter.ts requires the apiKey via options only (no env
	// fallback). Matching that keeps the error path identical to upstream.
	apiKey := options.APIKey
	if apiKey == "" {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = fmt.Sprintf("No API key for provider: %s", model.Provider)
		return out
	}

	var payload any = buildOpenRouterImagesPayload(model, imagesCtx)
	if options.OnPayload != nil {
		next, replace, err := options.OnPayload(payload, model)
		if err != nil {
			out.StopReason = ImagesStopReasonError
			out.ErrorMessage = err.Error()
			return out
		}
		if replace {
			payload = next
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = err.Error()
		return out
	}

	requestCtx := ctx
	if options.TimeoutMs > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, time.Duration(options.TimeoutMs)*time.Millisecond)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, strings.TrimRight(model.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = err.Error()
		return out
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range options.Headers {
		req.Header.Set(k, v)
	}

	resp, err := streamingHTTPClient().Do(req)
	if err != nil {
		out.StopReason = imagesStopReasonForContext(ctx)
		out.ErrorMessage = err.Error()
		return out
	}
	defer func() { _ = resp.Body.Close() }()

	if options.OnResponse != nil {
		headers := make(map[string]string, len(resp.Header))
		for k, values := range resp.Header {
			headers[k] = strings.Join(values, ", ")
		}
		if err := options.OnResponse(ProviderResponse{Status: resp.StatusCode, Headers: headers}, model); err != nil {
			out.StopReason = ImagesStopReasonError
			out.ErrorMessage = err.Error()
			return out
		}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		out.StopReason = imagesStopReasonForContext(ctx)
		out.ErrorMessage = err.Error()
		return out
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = string(data)
		return out
	}

	var parsed openRouterImagesResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = err.Error()
		return out
	}
	out.ResponseID = parsed.ID
	if parsed.Usage != nil {
		out.Usage = parseOpenRouterImagesUsage(*parsed.Usage, model)
	}
	if len(parsed.Choices) > 0 {
		choice := parsed.Choices[0]
		if choice.Message.Content != "" {
			out.Output = append(out.Output, TextContent{Text: choice.Message.Content})
		}
		for _, image := range choice.Message.Images {
			imageURL := image.ImageURL.url()
			if !strings.HasPrefix(imageURL, "data:") {
				continue
			}
			mediaType, data, ok := parseDataImageURL(imageURL)
			if !ok {
				continue
			}
			out.Output = append(out.Output, ImageContent{MimeType: mediaType, Data: data})
		}
	}
	return out
}

func imagesStopReasonForContext(ctx context.Context) ImagesStopReason {
	if ctx.Err() != nil {
		return ImagesStopReasonAborted
	}
	return ImagesStopReasonError
}

func buildOpenRouterImagesPayload(model ImagesModel, imagesCtx ImagesContext) map[string]any {
	content := make([]map[string]any, 0, len(imagesCtx.Input))
	for _, item := range imagesCtx.Input {
		switch v := item.(type) {
		case TextContent:
			content = append(content, map[string]any{"type": "text", "text": v.Text})
		case ImageContent:
			mediaType := v.MimeType
			if mediaType == "" {
				mediaType = "application/octet-stream"
			}
			url := "data:" + mediaType + ";base64," + v.Data
			content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
		}
	}
	modalities := []string{"image"}
	if slicesContainsString(model.Output, "text") {
		modalities = []string{"image", "text"}
	}
	return map[string]any{
		"model":      model.ID,
		"messages":   []map[string]any{{"role": "user", "content": content}},
		"stream":     false,
		"modalities": modalities,
	}
}

func slicesContainsString(values []string, target string) bool {
	return slices.Contains(values, target)
}

type openRouterImagesResponse struct {
	ID      string                   `json:"id"`
	Choices []openRouterImagesChoice `json:"choices"`
	Usage   *openRouterImagesUsage   `json:"usage"`
}

type openRouterImagesChoice struct {
	Message openRouterImagesMessage `json:"message"`
}

type openRouterImagesMessage struct {
	Content string                     `json:"content"`
	Images  []openRouterGeneratedImage `json:"images"`
}

type openRouterGeneratedImage struct {
	ImageURL dataImageURL `json:"image_url"`
}

type dataImageURL string

func (u *dataImageURL) UnmarshalJSON(data []byte) error {
	var asString string
	if err := json.Unmarshal(data, &asString); err == nil {
		*u = dataImageURL(asString)
		return nil
	}
	var asObject struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &asObject); err != nil {
		return err
	}
	*u = dataImageURL(asObject.URL)
	return nil
}

func (u dataImageURL) url() string { return string(u) }

func parseDataImageURL(raw string) (mediaType, data string, ok bool) {
	prefix, encoded, found := strings.Cut(raw, ",")
	if !found || !strings.HasPrefix(prefix, "data:") || !strings.Contains(prefix, ";base64") {
		return "", "", false
	}
	mt := strings.TrimPrefix(strings.Split(prefix, ";")[0], "data:")
	if mt == "" {
		mt = mime.TypeByExtension(".png")
	}
	if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
		return "", "", false
	}
	return mt, encoded, true
}

type openRouterImagesUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	PromptTokensDetails *struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
}

// parseOpenRouterImagesUsage mirrors upstream openrouter-images.ts parseUsage,
// which prices each bucket at the image model's flat rates.
func parseOpenRouterImagesUsage(raw openRouterImagesUsage, model ImagesModel) *Usage {
	reportedCachedTokens := 0
	cacheWriteTokens := 0
	if raw.PromptTokensDetails != nil {
		reportedCachedTokens = raw.PromptTokensDetails.CachedTokens
		cacheWriteTokens = raw.PromptTokensDetails.CacheWriteTokens
	}
	cacheReadTokens := reportedCachedTokens
	if cacheWriteTokens > 0 {
		cacheReadTokens = max(0, reportedCachedTokens-cacheWriteTokens)
	}
	input := max(0, raw.PromptTokens-cacheReadTokens-cacheWriteTokens)
	output := raw.CompletionTokens
	usage := &Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheReadTokens,
		CacheWrite:  cacheWriteTokens,
		TotalTokens: input + output + cacheReadTokens + cacheWriteTokens,
		// float64() rounds each product before the sum below, as JavaScript
		// does, instead of letting the compiler fuse a multiply-add.
		Cost: UsageCost{
			Input:      float64((model.Cost.Input / 1000000) * float64(input)),
			Output:     float64((model.Cost.Output / 1000000) * float64(output)),
			CacheRead:  float64((model.Cost.CacheRead / 1000000) * float64(cacheReadTokens)),
			CacheWrite: float64((model.Cost.CacheWrite / 1000000) * float64(cacheWriteTokens)),
		},
	}
	usage.Cost.Total = usage.Cost.Input + usage.Cost.Output + usage.Cost.CacheRead + usage.Cost.CacheWrite
	return usage
}
