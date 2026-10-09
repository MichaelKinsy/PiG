package ai

// Ports packages/ai/src/api/openai-codex-responses.ts.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

var codexTerminalRateLimit = lazyregexp.NewJSIgnoreCase(`GoUsageLimitError|FreeUsageLimitError|Monthly usage limit reached|available balance|insufficient_quota|out of budget|quota exceeded|billing`)
var codexRetryableMessage = lazyregexp.NewJSIgnoreCase(`rate.?limit|overloaded|service.?unavailable|upstream.?connect|connection.?refused`)

type codexRetryDelayExceeded struct{ message string }

func (e *codexRetryDelayExceeded) Error() string { return e.message }

func codexRetryDelay(headers http.Header, attempt, maxDelayMs int) (time.Duration, error) {
	delay := math.NaN()
	if values, present := headers[http.CanonicalHeaderKey("retry-after-ms")]; present && len(values) > 0 {
		value := strings.TrimSpace(values[0])
		if value == "" {
			delay = 0
		} else {
			delay, _ = strconv.ParseFloat(value, 64)
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				delay = math.NaN()
			}
		}
	}
	if math.IsNaN(delay) || math.IsInf(delay, 0) {
		delay = math.NaN()
		if value := headers.Get("retry-after"); value != "" {
			if seconds, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil && !math.IsInf(seconds, 0) && !math.IsNaN(seconds) {
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

func codexHTTPError(status int, raw []byte) error {
	message := string(raw)
	if message == "" {
		message = http.StatusText(status)
	}
	var parsed struct {
		Error struct {
			Code     string   `json:"code"`
			Type     string   `json:"type"`
			Message  string   `json:"message"`
			Plan     string   `json:"plan_type"`
			ResetsAt *float64 `json:"resets_at"`
		}
	}
	if json.Unmarshal(raw, &parsed) == nil {
		e := parsed.Error
		code := e.Code
		if code == "" {
			code = e.Type
		}
		if status == 429 || strings.Contains(strings.ToLower(code), "usage_limit_reached") || strings.Contains(strings.ToLower(code), "usage_not_included") || strings.Contains(strings.ToLower(code), "rate_limit_exceeded") {
			plan := ""
			if e.Plan != "" {
				plan = " (" + strings.ToLower(e.Plan) + " plan)"
			}
			when := ""
			// resets_at is checked for truthiness (openai-codex-responses.ts:1609): zero means no reset time.
			if e.ResetsAt != nil && *e.ResetsAt != 0 && !math.IsNaN(*e.ResetsAt) {
				minutes := max(0, math.Floor((*e.ResetsAt*1000-float64(time.Now().UnixMilli()))/60000+0.5))
				when = fmt.Sprintf(" Try again in ~%.0f min.", minutes)
			}
			return errors.New(strings.TrimSpace("You have hit your ChatGPT usage limit" + plan + "." + when))
		}
		if e.Message != "" {
			message = e.Message
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
			raw, readErr := io.ReadAll(response.Body)
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
					err = codexHTTPError(status, raw)
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
