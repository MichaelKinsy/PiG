package ai

// Ports packages/ai/src/api/openai-completions.ts (SDK HTTP error extraction).
// Ports packages/ai/src/api/openai-responses.ts (SDK HTTP error extraction).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// openAIRequestError preserves the OpenAI SDK's setup timeout message and provider-retry.ts's caller-abort message while retaining the Go cause. retryProviderRequest replaces whatever the SDK threw with createAbortError() once the signal is aborted (provider-retry.ts:68-72,124-125), so an abort before headers reads "Request aborted", not the SDK's "Request was aborted.". A fetch rejection that is neither is classified as the SDK does (openAISDKTransportError); any other failure keeps the provider prefix.
func openAIRequestError(ctx context.Context, err error, prefix string) error {
	wrapped := fmt.Errorf("%s: %w", prefix, err)
	if ctx.Err() != nil {
		return &nodeTransportError{message: "Request aborted", cause: wrapped}
	}
	if _, ok := errors.AsType[*providerRequestTimeoutError](err); ok {
		return &nodeTransportError{message: "Request timed out.", cause: wrapped}
	}
	if sdkErr := openAISDKTransportError(ctx, err); sdkErr != nil {
		return sdkErr
	}
	return wrapped
}

// openAIHTTPError reconstructs openai/core/error.APIError.generate's message and inner error object. Pi's shared error-body policy owns status/body composition and truncation.
func openAIHTTPError(status int, raw []byte, prefix ...string) error {
	var envelope map[string]json.RawMessage
	parsed := json.Valid(raw) && jsonValueTruthy(raw)
	if parsed {
		canonical, err := jsonstringify.Canonicalize(raw)
		if err == nil {
			_ = json.Unmarshal(canonical, &envelope)
		}
	}
	sdkError := envelope["error"]
	message, hasMessage := openAIStreamErrorMessage(sdkError)
	if !hasMessage && !parsed {
		message = string(raw)
	}
	if message == "" {
		message = fmt.Sprintf("%d status code (no body)", status)
	} else {
		message = fmt.Sprintf("%d %s", status, message)
	}
	return &providerError{status: new(status), body: sdkError, message: message, prefix: prefix}
}
