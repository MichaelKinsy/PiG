package ai

// Ports the request plumbing shared by the classifier APIs: packages/ai/src/utils/provider-retry.ts
// (retryProviderRequest), the per-attempt timeout of api/system-one-shared.ts and api/llama-cpp-classify.ts, and
// utils/headers.ts (providerHeadersToRecord).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// upstream: packages/ai/src/api/system-one-shared.ts:maxRetries
const defaultClassifierMaxRetries = 2

var errClassifierRequestAborted = errors.New("Request aborted")

// classifierRequestError is a failed HTTP attempt: a non-2xx response, or a timeout (no status, no headers).
// Only these errors are retried; a transport failure is not.
type classifierRequestError struct {
	providerError
	headers http.Header
}

func (e *classifierRequestError) Error() string { return e.message }

func classifierHTTPError(label string, status int, headers http.Header, body string) *classifierRequestError {
	return &classifierRequestError{providerError: providerError{status: &status, body: body, message: fmt.Sprintf("%s returned %d", label, status)}, headers: headers}
}

func classifierTimeoutError(timeoutMs int) *classifierRequestError {
	return &classifierRequestError{providerError: providerError{message: fmt.Sprintf("Request timed out after %dms", timeoutMs)}}
}

// classifierErrorMessage is formatProviderError(normalizeProviderError(error), `${label} error`).
func classifierErrorMessage(err error, label string) string {
	if request, ok := errors.AsType[*classifierRequestError](err); ok {
		return FormatProviderError(NormalizeProviderError(&request.providerError), label+" error")
	}
	return FormatProviderError(NormalizeProviderError(err), label+" error")
}

var classifierClient = sync.OnceValue(streamingHTTPClientNoRetry)

// classifierHTTPClient executes requests with Fetch when given. Classifier retries are owned by retryClassifierRequest,
// so the client carries no retry transport.
func classifierHTTPClient(fetch *http.Client) *http.Client {
	return providerHTTPClient(classifierClient(), fetch)
}

// classifierRetryable is isRetryableProviderError: a missing status is retried.
func classifierRetryable(err *classifierRequestError) bool {
	status := 0
	if err.status != nil {
		status = *err.status
	}
	return isRetryableProviderResponse(status, err.headers)
}

// retryClassifierRequest reproduces retryProviderRequest: the request runs again after a retryable failure, the backoff
// sleep is interruptible, and a provider-requested delay above the cap fails at once.
func retryClassifierRequest[T any](ctx context.Context, options ClassifierOptions, request func() (T, error)) (T, error) {
	var zero T
	maxRetries := defaultClassifierMaxRetries
	if options.MaxRetries != nil {
		maxRetries = *options.MaxRetries
	}
	maxRetryDelayMs := defaultProviderMaxRetryDelayMs
	if options.MaxRetryDelayMs != nil {
		maxRetryDelayMs = *options.MaxRetryDelayMs
	}
	retriesRemaining := maxRetries
	for {
		value, err := request()
		if err == nil {
			return value, nil
		}
		if ctx.Err() != nil {
			return zero, errClassifierRequestAborted
		}
		var failure *classifierRequestError
		if retriesRemaining <= 0 || !errors.As(err, &failure) || !classifierRetryable(failure) {
			return zero, err
		}
		retryIndex := maxRetries - retriesRemaining
		retriesRemaining--
		delay, delayErr := providerRetryDelay(failure.headers, retryIndex, maxRetryDelayMs, failure.message)
		if delayErr != nil {
			return zero, delayErr
		}
		if abortableSleep(ctx, delay) != nil {
			return zero, errClassifierRequestAborted
		}
	}
}

type classifierResponse struct {
	status  int
	headers http.Header
	body    []byte
}

// classifierPost sends one JSON POST attempt with its own timeout. A response that is not 2xx is a
// classifierRequestError carrying its body.
func classifierPost(ctx context.Context, options ClassifierOptions, label, target string, headers []classifierHeader, body []byte) (classifierResponse, error) {
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
	for _, header := range headers {
		if strings.EqualFold(header.name, "host") {
			request.Host = header.value
			continue
		}
		request.Header.Set(header.name, header.value)
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

type classifierHeader struct{ name, value string }

// classifierRequestHeaders is providerHeadersToRecord: sources merge case-insensitively in order, a later source
// replaces an earlier one and moves the header to the end, and a nil value removes it.
func classifierRequestHeaders(sources ...ProviderHeaders) []classifierHeader {
	var merged []classifierHeader
	for _, source := range sources {
		for name, value := range source {
			normalized := strings.ToLower(name)
			merged = slices.DeleteFunc(merged, func(header classifierHeader) bool { return strings.ToLower(header.name) == normalized })
			if value != nil {
				merged = append(merged, classifierHeader{name: name, value: *value})
			}
		}
	}
	return merged
}

func timeoutDuration(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }
