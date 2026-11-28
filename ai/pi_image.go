package ai

// Ports packages/ai/src/api/pi-image.ts and packages/ai/src/api/pi-image.lazy.ts (Pi 39bfcfca1, c863bdf19, a512c3fe2, d02145c49).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

const piImageLabel = "Pi Image"

// PiImageOptions configures a pi-image generation request.
type PiImageOptions struct {
	ImagesOptions
	// SessionID is an optional session identifier for gateway correlation/analytics.
	SessionID *string
}

// PiImageRequestOptions is the nested options object on the wire.
type PiImageRequestOptions struct {
	AspectRatio string         `json:"aspectRatio,omitempty"`
	Size        string         `json:"size,omitempty"`
	SessionID   *string        `json:"sessionId,omitempty"`
	Metadata    map[string]any `json:"metadata,omitzero"`
}

// PiImageRequest is the wire request body sent by pi-image to POST <baseUrl>/image.
type PiImageRequest struct {
	Model   string                 `json:"model"`
	Context piImageWireContext     `json:"context"`
	Options *PiImageRequestOptions `json:"options,omitempty"`
}

type piImageWireContext struct {
	Input []ImagesInputContent `json:"input"`
}

// PiImageResponse is the wire response body returned by a pi-image backend on 200 OK.
type PiImageResponse struct {
	Output     []ImagesOutputContent `json:"output"`
	Usage      *Usage                `json:"usage,omitempty"`
	ResponseID string                `json:"responseId,omitempty"`
}

// PiImageAPI returns the pi-image implementation for a provider (packages/ai/src/api/pi-image.lazy.ts piImageApi).
func PiImageAPI() *ProviderImages {
	return &ProviderImages{
		GenerateImages: func(ctx context.Context, model *ImageModel, request ImagesContext, options ImagesOptions) (AssistantImages, error) {
			return GenerateImagesPiImage(ctx, *model, request, PiImageOptions{ImagesOptions: options}), nil
		},
	}
}

func parsePiImageOutput(raw json.RawMessage) ([]ImagesOutputContent, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, fmt.Errorf("%s returned an unexpected response", piImageLabel)
	}
	out := make([]ImagesOutputContent, 0, len(items))
	for i, itemRaw := range items {
		obj, ok := jsonObjectOf(itemRaw)
		if !ok {
			return nil, fmt.Errorf("%s returned an invalid output block at index %d", piImageLabel, i)
		}
		var blockType string
		if err := json.Unmarshal(obj["type"], &blockType); err != nil {
			return nil, fmt.Errorf("%s returned an unknown output block type at index %d", piImageLabel, i)
		}
		switch blockType {
		case "text":
			var text string
			if err := json.Unmarshal(obj["text"], &text); err != nil || len(obj["text"]) == 0 || obj["text"][0] != '"' {
				return nil, fmt.Errorf("%s returned an invalid text block at index %d", piImageLabel, i)
			}
			out = append(out, TextContent{Text: text})
		case "image":
			var data, mimeType string
			if err := json.Unmarshal(obj["data"], &data); err != nil || data == "" ||
				json.Unmarshal(obj["mimeType"], &mimeType) != nil || mimeType == "" {
				return nil, fmt.Errorf("%s returned an invalid image block at index %d", piImageLabel, i)
			}
			out = append(out, ImageContent{Data: data, MimeType: mimeType})
		default:
			return nil, fmt.Errorf("%s returned an unknown output block type at index %d", piImageLabel, i)
		}
	}
	return out, nil
}

// GenerateImagesPiImage generates images through pi's canonical POST <baseUrl>/image protocol.
func GenerateImagesPiImage(ctx context.Context, model ImageModel, imagesCtx ImagesContext, options PiImageOptions) AssistantImages {
	output := AssistantImages{
		API:        model.API,
		Provider:   model.Provider,
		Model:      model.ID,
		Output:     []ImagesOutputContent{},
		StopReason: ImagesStopReasonStop,
		Timestamp:  nowMillis(),
	}

	fail := func(err error) AssistantImages {
		output.Output = []ImagesOutputContent{}
		if ctx.Err() != nil {
			output.StopReason = ImagesStopReasonAborted
		} else {
			output.StopReason = ImagesStopReasonError
		}
		output.ErrorMessage = classifierErrorMessage(err, piImageLabel)
		return output
	}

	if model.API != APIImagesPiImage {
		return fail(fmt.Errorf("Unsupported image API: %s", model.API))
	}
	hasInputImages := false
	for _, item := range imagesCtx.Input {
		if _, ok := item.(ImageContent); ok {
			hasInputImages = true
			break
		}
	}
	if hasInputImages && !slices.Contains(model.Input, "image") {
		return fail(fmt.Errorf("%s model %s does not support image input", piImageLabel, model.ID))
	}

	base := strings.TrimRight(model.BaseURL, "/") + "/"
	baseParsed, err := url.Parse(base)
	if err != nil {
		return fail(err)
	}
	endpoint, err := baseParsed.Parse("image")
	if err != nil {
		return fail(err)
	}

	var wireOpts *PiImageRequestOptions
	if options.AspectRatio != "" || options.Size != "" || options.SessionID != nil || options.Metadata != nil {
		wireOpts = &PiImageRequestOptions{
			AspectRatio: options.AspectRatio,
			Size:        options.Size,
			SessionID:   options.SessionID,
			Metadata:    options.Metadata,
		}
	}
	inputSlice := imagesCtx.Input
	if inputSlice == nil {
		inputSlice = []ImagesInputContent{}
	}
	reqBody := PiImageRequest{
		Model:   model.ID,
		Context: piImageWireContext{Input: inputSlice},
		Options: wireOpts,
	}

	var onPayload func(any) (any, bool, error)
	if options.OnPayload != nil {
		onPayload = func(payload any) (any, bool, error) {
			return options.OnPayload(payload, model)
		}
	}
	var onResponse func(ProviderResponse) error
	if options.OnResponse != nil {
		onResponse = func(response ProviderResponse) error {
			return options.OnResponse(response, model)
		}
	}

	rawBody, err := postJsonRequest(ctx, piImageLabel, endpoint, model.Provider, model.BaseURL, model.Headers, reqBody, jsonRequestOptions{
		APIKey:          options.APIKey,
		Headers:         options.Headers,
		Identity:        options.Identity,
		TimeoutMs:       options.TimeoutMs,
		MaxRetries:      options.MaxRetries,
		MaxRetryDelayMs: options.MaxRetryDelayMs,
		Fetch:           options.Fetch,
		OnPayload:       onPayload,
		OnResponse:      onResponse,
	})
	if err != nil {
		return fail(err)
	}

	bodyObj, ok := jsonObjectOf(rawBody)
	if !ok {
		return fail(errors.New(piImageLabel + " returned an unexpected response"))
	}
	var responseID string
	if json.Unmarshal(bodyObj["responseId"], &responseID) == nil && responseID != "" {
		output.ResponseID = responseID
	}
	if usage := ParsePiUsage(bodyObj["usage"], &model); usage != nil {
		output.Usage = usage
	}
	parsedOutput, err := parsePiImageOutput(bodyObj["output"])
	if err != nil {
		return fail(err)
	}
	output.Output = parsedOutput
	return output
}
