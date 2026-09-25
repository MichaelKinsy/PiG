// Package managementhttp ports upstream coding-agent utils/management-http.ts:
// a bounded immediate retry for idempotent management requests (tool
// downloads, release lookups). Agent and model requests must not use it; their
// semantic callers own retries.
package managementhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

// retryableStatusCodes mirrors upstream RETRYABLE_STATUS_CODES.
var retryableStatusCodes = map[int]bool{408: true, 425: true, 429: true, 500: true, 502: true, 503: true, 504: true}

// FetchRetryOptions mirrors upstream FetchRetryOptions.
type FetchRetryOptions struct {
	// MaxRetries is the number of additional attempts after the first. Nil
	// means two; negative values mean none.
	MaxRetries *int
	// RetryOnStatus retries transient HTTP statuses as well as transport
	// failures. Nil means true.
	RetryOnStatus *bool
	// Timeout is the overall budget shared by every attempt. Zero means none.
	Timeout time.Duration
	// AttemptTimeout bounds each attempt separately, so a hung connection can
	// be retried. Zero means none.
	AttemptTimeout time.Duration
}

// FetchWithRetry mirrors upstream fetchWithRetry. It sends req (which must be
// safe to resend: bodiless, or with GetBody) up to 1+MaxRetries times, retrying
// transport failures and, unless disabled, retryable statuses, with no delay.
// Caller cancellation and the overall Timeout are terminal; an AttemptTimeout
// expiry is retried. The returned response body keeps the attempt's deadlines
// until it is closed.
func FetchWithRetry(client *http.Client, req *http.Request, options FetchRetryOptions) (*http.Response, error) {
	if client == nil {
		client = http.DefaultClient
	}
	maxRetries := 2
	if options.MaxRetries != nil {
		maxRetries = max(0, *options.MaxRetries)
	}
	retryOnStatus := options.RetryOnStatus == nil || *options.RetryOnStatus

	parent := req.Context()
	budget, cancelBudget := parent, context.CancelFunc(func() {})
	if options.Timeout > 0 {
		budget, cancelBudget = context.WithTimeout(parent, options.Timeout)
	}

	for attempt := 0; ; attempt++ {
		if err := budget.Err(); err != nil {
			cancelBudget()
			return nil, err
		}
		attemptCtx, cancelAttempt := budget, context.CancelFunc(func() {})
		if options.AttemptTimeout > 0 {
			attemptCtx, cancelAttempt = context.WithTimeout(budget, options.AttemptTimeout)
		}
		attemptReq, err := cloneRequest(req, attemptCtx)
		if err != nil {
			cancelAttempt()
			cancelBudget()
			return nil, err
		}
		// This CLI transport sends caller-selected management URLs, including local
		// test servers. Callers own destination policy; retrying adds no trust boundary.
		response, err := client.Do(attemptReq) //nolint:gosec // G704: caller-selected CLI management URL, not server-side user input.
		if err == nil {
			if retryOnStatus && retryableStatusCodes[response.StatusCode] && attempt < maxRetries {
				// Discard the transient response before the next attempt.
				_ = response.Body.Close()
				cancelAttempt()
				continue
			}
			response.Body = &cancelOnClose{ReadCloser: response.Body, cancel: func() { cancelAttempt(); cancelBudget() }}
			return response, nil
		}
		attemptTimedOut := errors.Is(attemptCtx.Err(), context.DeadlineExceeded) && budget.Err() == nil
		cancelAttempt()
		if budget.Err() != nil || attempt >= maxRetries ||
			(errors.Is(err, context.Canceled) && !attemptTimedOut && options.Timeout <= 0) {
			cancelBudget()
			return nil, err
		}
	}
}

func cloneRequest(req *http.Request, ctx context.Context) (*http.Request, error) {
	clone := req.Clone(ctx)
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		clone.Body = body
	}
	return clone, nil
}

// cancelOnClose releases the attempt and budget contexts once the caller is
// done with the body, the Go counterpart of the signal staying attached to the
// upstream Response.
type cancelOnClose struct {
	io.ReadCloser
	cancel func()
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
