package ai

// Ports packages/ai/src/api/json-request.ts (Pi 39bfcfca1, a512c3fe2, d02145c49).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// JsonHttpError is an HTTP failure in the shape RetryProviderRequest and NormalizeProviderError understand.
type JsonHttpError = ClassifierHTTPError

// jsonRequestOptions is the shared subset of ClassifierOptions / ImagesOptions used by postJsonRequest.
type jsonRequestOptions struct {
	APIKey          string
	Headers         ProviderHeaders
	Identity        *ClientIdentity
	TimeoutMs       int
	MaxRetries      *int
	MaxRetryDelayMs *int
	Fetch           *http.Client
	OnPayload       func(payload any) (any, bool, error)
	OnResponse      func(response ProviderResponse) error
}

// postJsonRequest posts one JSON provider request with bearer auth, onPayload/onResponse hooks,
// a fresh timeout per attempt, and provider retries. Returns the raw response body bytes.
func postJsonRequest(
	ctx context.Context,
	label string,
	endpoint *url.URL,
	provider string,
	baseURL string,
	modelHeaders map[string]string,
	body any,
	options jsonRequestOptions,
	noRetryStatuses ...int,
) ([]byte, error) {
	if options.APIKey == "" {
		return nil, fmt.Errorf("No API key for provider: %s", provider)
	}
	payload := body
	if options.OnPayload != nil {
		transformed, replace, err := options.OnPayload(payload)
		if err != nil {
			return nil, err
		}
		if replace {
			payload = transformed
		}
	}
	encoded, err := marshalRequestBody(payload)
	if err != nil {
		return nil, err
	}

	maxRetries := defaultClassifierMaxRetries
	if options.MaxRetries != nil {
		maxRetries = *options.MaxRetries
	}
	client := classifierHTTPClient(options.Fetch)
	headers := providerHeadersToRecord(
		identityProviderHeaders(ClientIdentityTarget{Provider: provider, BaseURL: baseURL}, options.Identity),
		ProviderHeadersFromStrings(map[string]string{
			"authorization": "Bearer " + options.APIKey,
			"content-type":  "application/json",
		}),
		ProviderHeadersFromStrings(modelHeaders),
		options.Headers,
	)

	data, retryErr := RetryProviderRequest(ctx, func() ([]byte, error) {
		attemptCtx := ctx
		var cancel context.CancelFunc
		if options.TimeoutMs > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, timeoutDuration(options.TimeoutMs))
			defer cancel()
		}
		req, err := newSDKFetchRequest(attemptCtx, http.MethodPost, endpoint.String(), bytes.NewReader(encoded), options.Fetch != nil)
		if err != nil {
			return nil, err
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			if options.TimeoutMs > 0 && attemptCtx.Err() != nil && ctx.Err() == nil {
				return nil, classifierTimeoutError(options.TimeoutMs)
			}
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		// Reported before the body is read, so a body that fails to arrive still reports its status.
		if options.OnResponse != nil {
			if cbErr := options.OnResponse(ProviderResponse{Status: resp.StatusCode, Headers: headersToRecord(resp.Header)}); cbErr != nil {
				return nil, &retryCallbackError{err: cbErr}
			}
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			if options.TimeoutMs > 0 && attemptCtx.Err() != nil && ctx.Err() == nil {
				return nil, classifierTimeoutError(options.TimeoutMs)
			}
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, classifierHTTPError(label, resp.StatusCode, resp.Header.Clone(), string(data))
		}
		return data, nil
	}, ProviderRetryOptions{
		MaxRetries:      &maxRetries,
		MaxRetryDelayMs: options.MaxRetryDelayMs,
		NoRetryStatuses: noRetryStatuses,
	})
	if retryErr != nil {
		if cbErr, ok := errors.AsType[*retryCallbackError](retryErr); ok {
			return nil, cbErr.err
		}
		return nil, retryErr
	}
	return data, nil
}

// ParsePiUsage parses a canonical pi Usage object returned by pi-decision / pi-image backends.
// Trusts a provider-reported cost.total when present; otherwise computes cost from the model catalog.
func ParsePiUsage(raw json.RawMessage, model AnyModel) *Usage {
	obj, ok := jsonObjectOf(raw)
	if !ok {
		return nil
	}
	_, hasInput := finiteNumberOf(obj["input"])
	_, hasOutput := finiteNumberOf(obj["output"])
	hasTokens := hasInput || hasOutput

	costObj, hasCostObj := jsonObjectOf(obj["cost"])
	var providerCostTotal float64
	hasProviderCost := false
	if hasCostObj {
		if total, ok := finiteNumberOf(costObj["total"]); ok {
			providerCostTotal = total
			hasProviderCost = true
		}
	}
	if !hasTokens && !hasProviderCost {
		return nil
	}

	inputRaw, inputPresent := obj["input"]
	outputRaw, outputPresent := obj["output"]
	cacheReadRaw, cacheReadPresent := obj["cacheRead"]
	cacheWriteRaw, cacheWritePresent := obj["cacheWrite"]
	input := classifierTokenCount(inputRaw, inputPresent)
	output := classifierTokenCount(outputRaw, outputPresent)
	cacheRead := classifierTokenCount(cacheReadRaw, cacheReadPresent)
	cacheWrite := classifierTokenCount(cacheWriteRaw, cacheWritePresent)
	totalTokens := input + output + cacheRead + cacheWrite
	if t, ok := finiteNumberOf(obj["totalTokens"]); ok && t >= 0 {
		totalTokens = classifierTokenCount(obj["totalTokens"], true)
	}
	usage := &Usage{
		Input:       input,
		Output:      output,
		CacheRead:   cacheRead,
		CacheWrite:  cacheWrite,
		TotalTokens: totalTokens,
	}
	if r, ok := finiteNumberOf(obj["reasoning"]); ok && r >= 0 {
		v := classifierTokenCount(obj["reasoning"], true)
		usage.Reasoning = &v
	}
	if img, ok := finiteNumberOf(obj["imageOutput"]); ok && img >= 0 {
		v := classifierTokenCount(obj["imageOutput"], true)
		usage.ImageOutput = &v
	}
	if hasProviderCost {
		if providerCostTotal < 0 {
			providerCostTotal = 0
		}
		usage.Cost = UsageCost{
			Input:      piDecisionFiniteNonNegative(costObj["input"]),
			Output:     piDecisionFiniteNonNegative(costObj["output"]),
			CacheRead:  piDecisionFiniteNonNegative(costObj["cacheRead"]),
			CacheWrite: piDecisionFiniteNonNegative(costObj["cacheWrite"]),
			Total:      providerCostTotal,
		}
		var fromProvider bool
		if json.Unmarshal(costObj["totalFromProvider"], &fromProvider) == nil && len(bytes.TrimSpace(costObj["totalFromProvider"])) > 0 {
			trimmed := string(bytes.TrimSpace(costObj["totalFromProvider"]))
			if trimmed == "true" || trimmed == "false" {
				usage.Cost.TotalFromProvider = &fromProvider
			}
		}
	} else {
		CalculateCost(model, usage)
	}
	return usage
}
