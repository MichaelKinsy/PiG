package ai

// Ports packages/ai/src/api/openai-codex-responses.ts.

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

var codexTerminalRateLimit = lazyregexp.NewJSIgnoreCase(`GoUsageLimitError|FreeUsageLimitError|Monthly usage limit reached|available balance|insufficient_quota|out of budget|quota exceeded|billing`)
var codexRetryableMessage = lazyregexp.NewJSIgnoreCase(`rate.?limit|overloaded|service.?unavailable|upstream.?connect|connection.?refused`)

type codexRetryDelayExceeded struct{ message string }

func (e *codexRetryDelayExceeded) Error() string { return e.message }

func codexRetryDelay(headers http.Header, attempt, maxDelayMs int) (time.Duration, error) {
	delay := math.NaN()
	// openai-codex-responses.ts:140-155 reads each header with Number(headers.get(name)): Headers.get joins repeated lines with ", ", and the value is a ByteString, one code unit per byte.
	if values, present := headers[http.CanonicalHeaderKey("retry-after-ms")]; present && len(values) > 0 {
		delay = jsnumber.Parse(jsstring.FromLatin1(strings.Join(values, ", ")))
	}
	if math.IsNaN(delay) || math.IsInf(delay, 0) {
		delay = math.NaN()
		if value := strings.Join(headers.Values("retry-after"), ", "); value != "" {
			if seconds := jsnumber.Parse(jsstring.FromLatin1(value)); !math.IsInf(seconds, 0) && !math.IsNaN(seconds) {
				delay = seconds * 1000
			} else if date := jsDateParse(value); !math.IsNaN(date) {
				// openai-codex-responses.ts:158-160: Date.parse(retryAfter) is accepted unless it is NaN.
				delay = date - float64(time.Now().UnixMilli())
			}
		}
	}
	if math.IsNaN(delay) {
		return time.Duration(math.Pow(2, float64(attempt))) * time.Second, nil
	}
	delay = max(0, delay)
	if maxDelayMs > 0 && delay > float64(maxDelayMs) {
		return 0, &codexRetryDelayExceeded{fmt.Sprintf("Server requested %ds retry delay (max: %ds)", int(math.Ceil(delay/1000)), int(math.Ceil(float64(maxDelayMs)/1000)))}
	}
	return time.Duration(delay * float64(time.Millisecond)), nil
}

// statusText is the response's reason phrase (fetchStatusText).
func codexHTTPError(status int, statusText string, raw []byte) error {
	// `raw || response.statusText || "Request failed"` (openai-codex-responses.ts:1598).
	message := cmp.Or(string(raw), statusText, "Request failed")
	// parseErrorResponse (openai-codex-responses.ts:1596-1621) reads `parsed?.error` and acts only when it is truthy; a body without one (an object
	// without `error`, an array, null, text) keeps the raw message. Its members are JavaScript values: `code || type` is coerced to a string by the
	// RegExp test, a truthy non-string plan_type throws inside the try (so the raw message stays), resets_at is coerced to a number, and a truthy
	// non-string message is converted with String.
	var top map[string]json.RawMessage
	if json.Unmarshal(raw, &top) == nil {
		if errValue := top["error"]; jsTruthyJSON(errValue) {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(errValue, &fields)
			codeValue := fields["code"]
			if !jsTruthyJSON(codeValue) {
				codeValue = fields["type"]
			}
			code := ""
			if jsTruthyJSON(codeValue) {
				code = jsnumber.StringFromJSON(codeValue)
			}
			if status == 429 || strings.Contains(jsstring.ToLower(code), "usage_limit_reached") || strings.Contains(jsstring.ToLower(code), "usage_not_included") || strings.Contains(jsstring.ToLower(code), "rate_limit_exceeded") {
				plan := ""
				if planValue := fields["plan_type"]; jsTruthyJSON(planValue) {
					var planType string
					if json.Unmarshal(planValue, &planType) != nil {
						// err.plan_type.toLowerCase is not a function: the TypeError is caught and the raw message stays.
						return errors.New(message)
					}
					plan = " (" + jsstring.ToLower(planType) + " plan)"
				}
				when := ""
				// resets_at is checked for truthiness (openai-codex-responses.ts:1610): zero means no reset time.
				if resetsAt := fields["resets_at"]; jsTruthyJSON(resetsAt) {
					//portlint:allow clock openai-codex-responses.ts:1612 reads Date.now() where it formats the error; the package's clock seams belong to OAuth and backoff structs
					minutes := math.Max(0, jsRound((float64(jsnumber.FromJSON(resetsAt)*1000)-float64(time.Now().UnixMilli()))/60000))
					when = " Try again in ~" + jsnumber.String(minutes) + " min."
				}
				return errors.New(strings.TrimSpace("You have hit your ChatGPT usage limit" + plan + "." + when))
			}
			if messageValue := fields["message"]; jsTruthyJSON(messageValue) {
				message = jsnumber.StringFromJSON(messageValue)
			}
		}
	}
	return errors.New(message)
}

// doCodexHeaders times only response-header acquisition. The child context stays alive until the caller runs the returned cancel function, after it finishes with the response body.
func (p *openAIResponsesProvider) doCodexHeaders(request *http.Request, timeout *int) (*http.Response, context.CancelFunc, error) {
	options, _ := request.Context().Value(providerRequestOptionsKey{}).(StreamOptions)
	// Codex owns a header-only timeout; the generic SDK zero-timeout rule does not apply.
	options.TimeoutMs = nil
	ctx, cancel := context.WithCancelCause(context.WithValue(WithProviderMaxRetries(request.Context(), 0), providerRequestOptionsKey{}, options))
	timeoutMs := 0
	if timeout != nil {
		timeoutMs = *timeout
	}
	timeoutErr := fmt.Errorf("Codex SSE response headers timed out after %dms", timeoutMs)
	var timer *time.Timer
	if timeoutMs > 0 {
		timer = time.AfterFunc(time.Duration(timeoutMs)*time.Millisecond, func() { cancel(timeoutErr) })
	}
	response, err := providerHTTPClient(p.client, options.Fetch).Do(request.WithContext(ctx))
	if timer != nil {
		timer.Stop()
	}
	if err != nil {
		cause := context.Cause(ctx)
		cancel(nil)
		if request.Context().Err() != nil {
			return nil, nil, errors.New("Request was aborted")
		}
		if errors.Is(cause, timeoutErr) {
			return nil, nil, timeoutErr
		}
		return nil, nil, err
	}
	return response, func() { cancel(nil) }, nil
}

// doCodexSSERequest returns the accepted response and the function that releases its request context.
func (p *openAIResponsesProvider) doCodexSSERequest(request *http.Request, opts StreamOptions) (*http.Response, context.CancelFunc, error) {
	retries := ProviderMaxRetries(request.Context())
	_, maxDelay := ConfiguredProviderRetry()
	if value, ok := request.Context().Value(providerMaxRetryDelayKey{}).(int); ok {
		maxDelay = value
	}
	if opts.MaxRetryDelayMs != nil {
		maxDelay = *opts.MaxRetryDelayMs
	}
	for attempt := 0; ; attempt++ {
		if request.Context().Err() != nil {
			return nil, nil, errors.New("Request was aborted")
		}
		current := request.Clone(request.Context())
		if attempt > 0 && request.GetBody != nil {
			body, err := request.GetBody()
			if err != nil {
				return nil, nil, err
			}
			current.Body = body
		}
		response, release, err := p.doCodexHeaders(current, opts.TimeoutMs)
		if err == nil {
			if observeErr := observeProviderResponse(request.Context(), opts, response, &Model{ID: p.cfg.Model, ProviderMeta: ProviderMetadata{ProviderID: p.cfg.ProviderID, API: p.api()}}); observeErr != nil {
				_ = response.Body.Close()
				release()
				return nil, nil, observeErr
			}
		}
		delay := time.Duration(math.Pow(2, float64(attempt))) * time.Second
		if err == nil && response.StatusCode >= 200 && response.StatusCode < 300 {
			return response, release, nil
		}
		if err == nil {
			raw, readErr := readWebText(response.Body)
			_ = response.Body.Close()
			release()
			if readErr != nil {
				err = readErr
			} else {
				status := response.StatusCode
				text := string(raw)
				retryable := (status != 429 || !codexTerminalRateLimit.MatchString(text)) && (status == 429 || status == 500 || status == 502 || status == 503 || status == 504 || codexRetryableMessage.MatchString(text))
				if attempt < retries && retryable {
					delay, err = codexRetryDelay(response.Header, attempt, maxDelay)
				} else {
					// parseErrorResponse reads `new Response(errorText)` with text() again, which drops up to two more byte order marks.
					err = codexHTTPError(status, fetchStatusText(response.Status, status), webTextBytes(raw))
				}
			}
		}
		if request.Context().Err() != nil {
			return nil, nil, errors.New("Request was aborted")
		}
		var delayExceeded *codexRetryDelayExceeded
		if attempt >= retries || errors.As(err, &delayExceeded) || (err != nil && strings.Contains(err.Error(), "usage limit")) {
			return nil, nil, err
		}
		if err := abortableSleep(request.Context(), delay); err != nil {
			return nil, nil, errors.New("Request was aborted")
		}
	}
}

// jsTruthyJSON is the JavaScript truthiness of the value JSON.parse produces for raw; a missing member (nil raw) is undefined.
func jsTruthyJSON(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return false
	}
	switch raw[0] {
	case 'n', 'f':
		return false
	case 't', '{', '[':
		return true
	case '"':
		return len(raw) > 2
	}
	value := jsnumber.FromJSON(raw)
	return value != 0 && !math.IsNaN(value)
}

// jsRound is Math.round: the nearest integer, a half rounding up.
func jsRound(value float64) float64 {
	rounded := math.Floor(value)
	if value-rounded >= 0.5 {
		rounded++
	}
	return rounded
}
