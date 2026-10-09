package ai

// Ports the request plumbing shared by the classifier APIs: packages/ai/src/utils/provider-retry.ts
// (retryProviderRequest), packages/ai/src/api/classifier-shared.ts (postClassifierRequest, parseClassifierUsage), the per-attempt
// timeout of api/llama-cpp-classify.ts, and utils/headers.ts (providerHeadersToRecord).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsonparse"
)

// upstream: packages/ai/src/api/classifier-shared.ts:postClassifierRequest (maxRetries)
const defaultClassifierMaxRetries = 2

// ClassifierHTTPError is a failed HTTP attempt: a non-2xx response, or a timeout (no status, no headers). Only these
// errors are retried; a transport failure is not.
//
// Ports packages/ai/src/api/classifier-shared.ts (ClassifierHttpError: status, headers and body beside the message).
type ClassifierHTTPError struct {
	// Status is the HTTP status; nil for a timeout.
	Status *int
	// Headers are the response headers; nil for a timeout.
	Headers http.Header
	// Body is the response body text.
	Body    string
	Message string
}

func (e *ClassifierHTTPError) Error() string { return e.Message }

// Name is the `name` property (classifier-shared.ts:8-27): a timeout, which has no status or headers, is "TimeoutError" (timeoutError sets it),
// and an HTTP failure keeps Error's default name, "Error" (httpError sets none).
func (e *ClassifierHTTPError) Name() string {
	if e.Status == nil {
		return "TimeoutError"
	}
	return "Error"
}

// providerError is the error the shared provider error formatter reads.
func (e *ClassifierHTTPError) providerError() *providerError {
	return &providerError{status: e.Status, body: e.Body, message: e.Message}
}

func classifierHTTPError(label string, status int, headers http.Header, body string) *ClassifierHTTPError {
	return &ClassifierHTTPError{Status: &status, Headers: headers, Body: body, Message: fmt.Sprintf("%s returned %d", label, status)}
}

func classifierTimeoutError(timeoutMs int) *ClassifierHTTPError {
	return &ClassifierHTTPError{Message: fmt.Sprintf("Request timed out after %dms", timeoutMs)}
}

// classifierErrorMessage is formatProviderError(normalizeProviderError(error), `${label} error`).
func classifierErrorMessage(err error, label string) string {
	if request, ok := errors.AsType[*ClassifierHTTPError](err); ok {
		return FormatProviderError(NormalizeProviderError(request.providerError()), label+" error")
	}
	return FormatProviderError(NormalizeProviderError(err), label+" error")
}

var classifierClient = sync.OnceValue(streamingHTTPClientNoRetry)

// classifierHTTPClient executes requests with Fetch when given. Classifier retries are owned by retryClassifierRequest,
// so the client carries no retry transport.
func classifierHTTPClient(fetch *http.Client) *http.Client {
	return providerHTTPClient(classifierClient(), fetch)
}

// ProviderStatus and ProviderHeaders make a failed classifier attempt a ProviderRequestError: a timeout has neither.
func (e *ClassifierHTTPError) ProviderStatus() *int         { return e.Status }
func (e *ClassifierHTTPError) ProviderHeaders() http.Header { return e.Headers }

// retryClassifierRequest is retryProviderRequest with the classifier options: the retry budget defaults to 2 (classifier-shared.ts maxRetries).
// noRetryStatuses lists HTTP statuses that fail at once although they are normally retried (ProviderRetryOptions.noRetryStatuses).
func retryClassifierRequest[T any](ctx context.Context, options ClassifierOptions, request func() (T, error), noRetryStatuses ...int) (T, error) {
	maxRetries := defaultClassifierMaxRetries
	if options.MaxRetries != nil {
		maxRetries = *options.MaxRetries
	}
	return RetryProviderRequest(ctx, request, ProviderRetryOptions{MaxRetries: &maxRetries, MaxRetryDelayMs: options.MaxRetryDelayMs, NoRetryStatuses: noRetryStatuses})
}

type classifierResponse struct {
	status  int
	headers http.Header
	body    []byte
}

// classifierPost sends one JSON POST attempt with its own timeout. A response that is not 2xx is a
// ClassifierHTTPError carrying its body.
func classifierPost(ctx context.Context, options ClassifierOptions, label, target string, headers map[string]string, body []byte) (classifierResponse, error) {
	attemptCtx, cancel := ctx, context.CancelFunc(func() {})
	if options.TimeoutMs != nil {
		attemptCtx, cancel = context.WithTimeout(ctx, timeoutDuration(*options.TimeoutMs))
	}
	defer cancel()
	fail := func(err error) (classifierResponse, error) {
		// A timeout that fired while the caller is still waiting is reported apart from a caller cancellation.
		if options.TimeoutMs != nil && attemptCtx.Err() != nil && ctx.Err() == nil {
			return classifierResponse{}, classifierTimeoutError(*options.TimeoutMs)
		}
		return classifierResponse{}, err
	}
	request, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return classifierResponse{}, err
	}
	for name, value := range headers {
		if strings.EqualFold(name, "host") {
			request.Host = value
			continue
		}
		request.Header.Set(name, value)
	}
	response, err := classifierHTTPClient(options.Fetch).Do(request)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return fail(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifierResponse{}, classifierHTTPError(label, response.StatusCode, response.Header, string(data))
	}
	return classifierResponse{status: response.StatusCode, headers: response.Header, body: data}, nil
}

// PostClassifierRequest posts one JSON classifier request with bearer auth, the OnPayload and OnResponse hooks, a fresh
// timeout per attempt, and provider retries. It returns the response body, which is valid JSON; failures are errors.
// noRetryStatuses lists HTTP statuses that fail at once although they are normally retried.
func PostClassifierRequest(ctx context.Context, label, target string, model ClassifierModel, payload any, options ClassifierOptions, noRetryStatuses []int) (json.RawMessage, error) {
	if options.APIKey == "" {
		return nil, fmt.Errorf("No API key for provider: %s", model.Provider)
	}
	if options.OnPayload != nil {
		transformed, replace, err := options.OnPayload(payload, model)
		if err != nil {
			return nil, err
		}
		if replace {
			payload = transformed
		}
	}
	body, err := marshalJSONValue(payload)
	if err != nil {
		return nil, err
	}
	headers := providerHeadersToRecord(ProviderHeadersFromStrings(map[string]string{"authorization": "Bearer " + options.APIKey, "content-type": "application/json"}), ProviderHeadersFromStrings(model.Headers), options.Headers)
	response, err := retryClassifierRequest(ctx, options, func() (classifierResponse, error) {
		response, err := classifierPost(ctx, options, label, target, headers, body)
		if err == nil {
			response.body, err = classifierJSONBody(response.body)
		}
		return response, err
	}, noRetryStatuses...)
	if err != nil {
		return nil, err
	}
	if options.OnResponse != nil {
		if err := options.OnResponse(ProviderResponse{Status: response.status, Headers: headersToRecord(response.headers)}, model); err != nil {
			return nil, err
		}
	}
	return response.body, nil
}

// classifierJSONBody is `await response.json()` (classifier-shared.ts:90): a UTF-8 byte order mark is dropped, and a body that JSON.parse rejects fails with
// V8's SyntaxError message.
func classifierJSONBody(body []byte) ([]byte, error) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	if err := jsonparse.Validate(body); err != nil {
		return nil, err
	}
	return body, nil
}

// IsRecord reports whether value is a JSON object, like classifier-shared.ts isRecord (a non-null, non-array object).
func IsRecord(value json.RawMessage) bool {
	_, ok := jsonObjectOf(value)
	return ok
}

// ParseClassifierUsage reads a { input_tokens, output_tokens } object and prices it from the model catalog like chat
// usage. A missing or malformed usage object leaves the result without usage instead of failing it.
func ParseClassifierUsage(raw json.RawMessage, model ClassifierModel) *Usage {
	value, ok := jsonObjectOf(raw)
	if !ok {
		return nil
	}
	inputRaw, hasInput := value["input_tokens"]
	outputRaw, hasOutput := value["output_tokens"]
	if !hasInput && !hasOutput {
		return nil
	}
	usage := Usage{Input: classifierTokenCount(inputRaw, hasInput), Output: classifierTokenCount(outputRaw, hasOutput)}
	usage.TotalTokens = usage.Input + usage.Output
	CalculateCost(&model, &usage)
	return &usage
}

func classifierTokenCount(raw json.RawMessage, present bool) int {
	if !present {
		return 0
	}
	if value, ok := finiteNumberOf(raw); ok && value > 0 {
		return int(math.Min(value, math.MaxInt32))
	}
	return 0
}

// providerHeadersToRecord ports utils/headers.ts providerHeadersToRecord: sources merge case-insensitively in order, a later source
// replaces an earlier one (keeping the later source's spelling of the name), and a nil value removes it. It returns nil when no header is left (undefined).
func providerHeadersToRecord(sources ...ProviderHeaders) map[string]string {
	merged := map[string][2]string{}
	for _, source := range sources {
		for name, value := range source {
			normalized := strings.ToLower(name)
			delete(merged, normalized)
			if value != nil {
				merged[normalized] = [2]string{name, *value}
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	record := make(map[string]string, len(merged))
	for _, header := range merged {
		record[header[0]] = header[1]
	}
	return record
}

func timeoutDuration(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }
