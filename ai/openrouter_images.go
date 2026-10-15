package ai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Ports packages/ai/src/providers/images/register-builtins.ts (registerBuiltInImagesApiProviders and generateImagesOpenRouter; the lazy
// import and its load-error AssistantImages are JavaScript module loading, which Go links at build time).

// RegisterBuiltInImagesAPIProviders registers upstream's built-in image API
// providers. Currently upstream ships OpenRouter image generation only.
func RegisterBuiltInImagesAPIProviders() {
	RegisterImagesAPIProvider(ImagesAPIProvider{
		API:            APIImagesOpenRouter,
		GenerateImages: GenerateImagesOpenRouter,
	})
}

// OpenRouterImagesAPI is the OpenRouter image implementation a provider lists under APIImagesOpenRouter (api/openrouter-images.lazy.ts openrouterImagesApi).
func OpenRouterImagesAPI() *ProviderImages {
	return &ProviderImages{GenerateImages: func(ctx context.Context, model *ImageModel, request ImagesContext, options ImagesOptions) (AssistantImages, error) {
		return GenerateImagesOpenRouter(ctx, *model, request, options), nil
	}}
}

// GenerateImagesOpenRouter implements upstream api/openrouter-images.ts generateImages.
func GenerateImagesOpenRouter(ctx context.Context, model ImageModel, imagesCtx ImagesContext, options ProviderImagesOptions) AssistantImages {
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

	return generateOpenRouterImages(ctx, model, imagesCtx, options, out, apiKey, false)
}

// generateOpenRouterImages sends one request: to chat/completions, or with dedicated to the Images API (/images) that OpenRouter requires for
// some image generation models.
func generateOpenRouterImages(ctx context.Context, model ImageModel, imagesCtx ImagesContext, options ProviderImagesOptions, out AssistantImages, apiKey string, dedicated bool) AssistantImages {
	var payload any = buildOpenRouterImagesPayload(model, imagesCtx)
	endpoint := "/chat/completions"
	if dedicated {
		payload = buildOpenRouterImagesAPIPayload(model, imagesCtx)
		endpoint = "/images"
	}
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

	body, err := marshalRequestBody(payload)
	if err != nil {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = err.Error()
		return out
	}

	requestCtx := ctx
	// openrouter-images.ts:72-81 passes options.maxRetries and options.maxRetryDelayMs to retryProviderRequest for this request; the retry transport reads them from the context.
	if options.MaxRetries != nil {
		requestCtx = WithProviderMaxRetries(requestCtx, *options.MaxRetries)
	}
	if options.MaxRetryDelayMs != nil {
		requestCtx = WithProviderRequestRetry(requestCtx, ProviderMaxRetries(requestCtx), options.MaxRetryDelayMs)
	}
	if options.TimeoutMs > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(requestCtx, time.Duration(options.TimeoutMs)*time.Millisecond)
		defer cancel()
	}
	requestURL, err := sdkBuildURL(model.BaseURL, endpoint)
	if err != nil {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = err.Error()
		return out
	}
	req, err := newSDKFetchRequest(requestCtx, http.MethodPost, requestURL, bytes.NewReader(body), options.Fetch != nil)
	if err != nil {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = err.Error()
		return out
	}
	req.Header = openRouterImagesHeaders(apiKey, model.Headers, options.Headers)

	resp, err := providerHTTPClient(streamingHTTPClient(), options.Fetch).Do(req)
	if err != nil {
		out.StopReason = imagesStopReasonForContext(ctx)
		if sdkErr := openAISDKTransportError(ctx, err); sdkErr != nil {
			out.ErrorMessage = sdkErr.Error()
			return out
		}
		// net/http decorates rejected requests with method and URL; the provider error message is the underlying rejection.
		if requestError, ok := errors.AsType[*url.Error](err); ok {
			err = requestError.Err
		}
		out.ErrorMessage = err.Error()
		return out
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		out.StopReason = imagesStopReasonForContext(ctx)
		out.ErrorMessage = err.Error()
		return out
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// pig divergence (D128): Pi stops at the chat/completions 404 for an image generation model that OpenRouter serves only through /images (pi-mono #10652).
		if !dedicated && isOpenRouterImagesAPIOnlyError(resp.StatusCode, data) {
			return generateOpenRouterImages(ctx, model, imagesCtx, options, out, apiKey, true)
		}
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = openAIHTTPError(resp.StatusCode, data).Error()
		return out
	}
	if dedicated {
		return finishOpenRouterImagesAPIResponse(model, options, resp, data, out)
	}

	response, err := parseOpenRouterImagesBody(data)
	if err != nil {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = err.Error()
		return out
	}
	// The SDK's withResponse() resolves only after a 2xx body parses, so Pi awaits onResponse after both checks (openrouter-images.ts generateImages).
	if options.OnResponse != nil {
		if err := options.OnResponse(ProviderResponse{Status: resp.StatusCode, Headers: headersToRecord(resp.Header)}, model); err != nil {
			out.StopReason = ImagesStopReasonError
			out.ErrorMessage = err.Error()
			return out
		}
	}
	if err := readOpenRouterImagesChat(response, model, &out); err != nil {
		out.StopReason = ImagesStopReasonError
		out.ErrorMessage = err.Error()
	}
	return out
}

func imagesStopReasonForContext(ctx context.Context) ImagesStopReason {
	if ctx.Err() != nil {
		return ImagesStopReasonAborted
	}
	return ImagesStopReasonError
}

func openRouterImagesHeaders(apiKey string, modelHeaders map[string]string, optionsHeaders ProviderHeaders) http.Header {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+apiKey)
	headers.Set("Content-Type", "application/json")
	for key, value := range modelHeaders {
		if override, exists := optionsHeaders[key]; exists && override == nil {
			continue
		}
		headers.Set(key, value)
	}
	for key, value := range optionsHeaders {
		if value != nil {
			headers.Set(key, *value)
		}
	}
	return headers
}

func buildOpenRouterImagesPayload(model ImageModel, imagesCtx ImagesContext) map[string]any {
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

// parseDataImageURL is imageUrl.match(/^data:([^;]+);base64,(.+)$/) (openrouter-images.ts:101): a non-empty media type without ';' directly
// followed by ";base64,", then a non-empty payload without a line terminator. The payload is not decoded or validated.
func parseDataImageURL(raw string) (mediaType, data string, ok bool) {
	rest, found := strings.CutPrefix(raw, "data:")
	if !found {
		return "", "", false
	}
	mediaType, rest, found = strings.Cut(rest, ";")
	if !found || mediaType == "" {
		return "", "", false
	}
	data, found = strings.CutPrefix(rest, "base64,")
	if !found || data == "" || strings.ContainsAny(data, "\n\r\u2028\u2029") {
		return "", "", false
	}
	return mediaType, data, true
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
func parseOpenRouterImagesUsage(raw openRouterImagesUsage, model ImageModel) *Usage {
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
