package ai

// Ports packages/ai/src/utils/provider-retry.ts.

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// defaultProviderMaxRetryDelayMs mirrors upstream provider-retry.ts
// DEFAULT_MAX_RETRY_DELAY_MS: a server-requested retry delay above this cap
// fails the request instead of waiting.
const defaultProviderMaxRetryDelayMs = 60_000

// Package-global provider-request retry policy, resolved from
// retry.provider.{maxRetries,maxRetryDelayMs}. maxRetries defaults to 0, so the
// retry transport is inert (one attempt, no retries) unless a user opts in -
// identical to the pre-configuration behavior. Mirrors upstream, where the SDK
// is always invoked with maxRetries:0 and retryProviderRequest supplies the
// retry loop from these same settings.
var (
	configuredProviderMaxRetries      atomic.Int64
	configuredProviderMaxRetryDelayMs atomic.Int64
)

func init() {
	configuredProviderMaxRetryDelayMs.Store(defaultProviderMaxRetryDelayMs)
}

// providerRetryJitter returns a value in [0,1) used to spread exponential
// backoff. It is a seam so tests can make the delay deterministic; production
// uses the same downward jitter as upstream's Math.random().
var providerRetryJitter = rand.Float64

// ConfigureProviderRetry sets the package-global provider-request retry policy.
// It mirrors the resolution of retry.provider.maxRetries / maxRetryDelayMs from
// settings (sdk.ts passes both into retryProviderRequest). maxRetries<=0 leaves
// the retry transport inert. Mirrors ConfigureHTTPDispatcher's global-config
// pattern for the sibling retry.provider.timeoutMs.
func ConfigureProviderRetry(maxRetries, maxRetryDelayMs int) error {
	if maxRetries < 0 {
		return fmt.Errorf("invalid provider maxRetries: %d", maxRetries)
	}
	if maxRetryDelayMs < 0 {
		return fmt.Errorf("invalid provider maxRetryDelayMs: %d", maxRetryDelayMs)
	}
	configuredProviderMaxRetries.Store(int64(maxRetries))
	configuredProviderMaxRetryDelayMs.Store(int64(maxRetryDelayMs))
	return nil
}

type providerMaxRetriesKey struct{}
type providerMaxRetryDelayKey struct{}

// WithProviderRequestRetry binds an independent caller's retry policy. A nil
// delay uses Pi's provider default rather than another Session's configuration.
func WithProviderRequestRetry(ctx context.Context, maxRetries int, maxRetryDelayMs *int) context.Context {
	delay := defaultProviderMaxRetryDelayMs
	if maxRetryDelayMs != nil {
		delay = max(0, *maxRetryDelayMs)
	}
	return context.WithValue(WithProviderMaxRetries(ctx, maxRetries), providerMaxRetryDelayKey{}, delay)
}

// WithProviderMaxRetries returns a context whose provider requests retry at
// most maxRetries times, overriding retry.provider.maxRetries. It carries
// upstream's per-request StreamOptions.maxRetries, which the transport-level
// retry policy cannot see.
func WithProviderMaxRetries(ctx context.Context, maxRetries int) context.Context {
	return context.WithValue(ctx, providerMaxRetriesKey{}, max(0, maxRetries))
}

// ProviderMaxRetries is the retry budget for requests made with ctx: the
// WithProviderMaxRetries override, else retry.provider.maxRetries.
func ProviderMaxRetries(ctx context.Context) int {
	if maxRetries, ok := ctx.Value(providerMaxRetriesKey{}).(int); ok {
		return maxRetries
	}
	return int(configuredProviderMaxRetries.Load())
}

// ConfiguredProviderRetry returns the currently configured provider retry
// policy: maxRetries and maxRetryDelayMs.
func ConfiguredProviderRetry() (maxRetries, maxRetryDelayMs int) {
	return int(configuredProviderMaxRetries.Load()), int(configuredProviderMaxRetryDelayMs.Load())
}

// isRetryableProviderResponse ports isRetryableProviderError. status 0 means no
// response was received (network failure), which upstream treats as an
// undefined status and retries.
func isRetryableProviderResponse(status int, headers http.Header) bool {
	switch headers.Get("x-should-retry") {
	case "true":
		return true
	case "false":
		return false
	}
	if status == 0 {
		return true
	}
	return status == 408 || status == 409 || status == 429 || status >= 500
}

// validateServerRetryDelay ports validateServerRetryDelayMs: a server-requested
// delay above the cap fails the request with the requested delay surfaced, so
// higher-level (agent) retry can handle it with user visibility. A cap of 0
// disables the limit. providerMsg is the provider's status line, the
// transport-level analog of the SDK APIError message upstream appends.
func validateServerRetryDelay(delayMs float64, maxRetryDelayMs int, providerMsg string) (time.Duration, error) {
	maxDelayMs := maxRetryDelayMs
	if maxDelayMs > 0 && delayMs > float64(maxDelayMs) {
		return 0, fmt.Errorf("Server requested %ds retry delay (max: %ds). %s",
			int(math.Ceil(delayMs/1000)), int(math.Ceil(float64(maxDelayMs)/1000)), providerMsg)
	}
	return time.Duration(delayMs) * time.Millisecond, nil
}

var jsFloatPrefix = lazyregexp.New(`^[+-]?(?:Infinity|(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)`)

// jsParseFloat is Number.parseFloat: it reads the longest numeric prefix after leading whitespace and returns NaN when there is none.
func jsParseFloat(value string) float64 {
	value = strings.TrimLeftFunc(value, func(r rune) bool { return r == '\ufeff' || r != '\u0085' && unicode.IsSpace(r) })
	prefix := jsFloatPrefix.FindString(value)
	if prefix == "" {
		return math.NaN()
	}
	// A range error carries the Infinity value required by Number.parseFloat.
	parsed, _ := strconv.ParseFloat(prefix, 64)
	return parsed
}

// providerRetryDelay ports getRetryDelayMs (provider-retry.ts:52-66): a finite server-requested delay (retry-after-ms,
// or retry-after as seconds or any date Date.parse accepts) capped by maxRetryDelayMs, otherwise exponential backoff
// min(0.5*2^i, 8)s with downward jitter. A header whose delay is not finite (an unparseable date, Infinity) falls
// through to the next source. Server-requested delays are cap-validated; the exponential fallback is not (it never
// exceeds 8s).
func providerRetryDelay(headers http.Header, retryIndex, maxRetryDelayMs int, providerMsg string) (time.Duration, error) {
	if v := headers.Get("retry-after-ms"); v != "" {
		if ms := jsParseFloat(v); !math.IsNaN(ms) && !math.IsInf(ms, 0) {
			return validateServerRetryDelay(ms, maxRetryDelayMs, providerMsg)
		}
	}
	if v := headers.Get("retry-after"); v != "" {
		var delayMs float64
		if seconds := jsParseFloat(v); !math.IsNaN(seconds) {
			delayMs = seconds * 1000
		} else {
			// provider-retry.ts:60: Date.parse(retryAfter) - Date.now(); an unparseable date is NaN and falls through.
			delayMs = jsDateParse(v) - float64(time.Now().UnixMilli())
		}
		if !math.IsNaN(delayMs) && !math.IsInf(delayMs, 0) {
			return validateServerRetryDelay(delayMs, maxRetryDelayMs, providerMsg)
		}
	}
	exponentialMs := math.Min(0.5*math.Pow(2, float64(retryIndex)), 8) * 1000
	jitteredMs := exponentialMs * (1 - providerRetryJitter()*0.25)
	return time.Duration(jitteredMs) * time.Millisecond, nil
}

// abortableSleep waits for d or context cancellation, whichever comes first,
// mirroring upstream's interruptible abortableSleep.
func abortableSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryTransport wraps a base RoundTripper with the provider retry policy,
// reproducing upstream's retryProviderRequest. It retries only the initial
// request and its response headers: the RoundTrip boundary, which for a
// streaming response returns before the SSE body is read, matching upstream
// wrapping only `create().withResponse()`. Mid-stream errors are not retried.
type retryTransport struct {
	base http.RoundTripper
	// onRetryResponse observes responses consumed by retry handling rather than returned to the caller.
	onRetryResponse func(*http.Response) error
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	maxRetries := ProviderMaxRetries(req.Context())
	if maxRetries <= 0 {
		return t.base.RoundTrip(req)
	}
	maxRetryDelayMs, ok := req.Context().Value(providerMaxRetryDelayKey{}).(int)
	if !ok {
		maxRetryDelayMs = int(configuredProviderMaxRetryDelayMs.Load())
	}
	retriesRemaining := maxRetries
	for {
		resp, err := t.base.RoundTrip(req)

		// Cancellation takes precedence over the retryable check, mirroring
		// upstream's signal check before classifying the error.
		if ctxErr := req.Context().Err(); ctxErr != nil {
			drainClose(resp)
			return nil, ctxErr
		}

		status := 0
		var headers http.Header
		providerMsg := "provider request failed"
		if err == nil {
			status = resp.StatusCode
			headers = resp.Header
			providerMsg = resp.Status
			if status >= 200 && status < 300 {
				return resp, nil
			}
		}

		if retriesRemaining <= 0 || !isRetryableProviderResponse(status, headers) {
			return resp, err
		}

		// Retrying re-sends the request body; skip if it cannot be rewound.
		if req.Body != nil && req.GetBody == nil {
			return resp, err
		}

		if resp != nil && t.onRetryResponse != nil {
			if observeErr := t.onRetryResponse(resp); observeErr != nil {
				drainClose(resp)
				return nil, observeErr
			}
		}
		retryIndex := maxRetries - retriesRemaining
		delay, delayErr := providerRetryDelay(headers, retryIndex, maxRetryDelayMs, providerMsg)
		if delayErr != nil {
			// The SDK error used by Pi's retry policy already carries the response body.
			if resp != nil && resp.Body != nil {
				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if ctxErr := req.Context().Err(); ctxErr != nil {
					return nil, ctxErr
				}
				if readErr != nil {
					return nil, fmt.Errorf("%w: %w", delayErr, readErr)
				}
				if len(body) > 0 {
					return nil, fmt.Errorf("%w. %s", delayErr, body)
				}
			}
			return nil, delayErr
		}

		drainClose(resp)
		if req.GetBody != nil {
			newBody, gbErr := req.GetBody()
			if gbErr != nil {
				return nil, gbErr
			}
			req.Body = newBody
		}
		retriesRemaining--

		if sleepErr := abortableSleep(req.Context(), delay); sleepErr != nil {
			return nil, sleepErr
		}
	}
}

// drainClose discards and closes a failed response body so the connection can
// be reused for the retry. The drain is bounded; error bodies are small.
func drainClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
}
