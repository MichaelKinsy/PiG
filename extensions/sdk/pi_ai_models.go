package sdk

// Ports pi-ai's pure model helpers: calculateCost, getSupportedThinkingLevels and clampThinkingLevel (packages/ai/src/models.ts), isContextOverflow and isRecoverableLength
// (utils/overflow.ts), isRetryableAssistantError, retryDelayMs and DEFAULT_MAX_AGENT_RETRY_DELAY_MS (utils/retry.ts), and envApiKeyAuth (auth/helpers.ts). A model and a message are the
// flat JSON objects the wire carries.

import (
	"context"
	"errors"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf16"
)

var extendedThinkingLevels = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// GetSupportedThinkingLevels is pi-ai's getSupportedThinkingLevels: the thinking levels the model accepts, in Pi's order (off, minimal, low, medium, high, xhigh, max). A model
// without reasoning accepts only "off". A level the model's thinkingLevelMap maps to null is not accepted, and xhigh and max are accepted only when the map names them.
func GetSupportedThinkingLevels(model map[string]any) []string {
	if !jsTruthy(model["reasoning"]) {
		return []string{"off"}
	}
	levels := model["thinkingLevelMap"]
	levelMap, _ := levels.(map[string]any)
	var supported []string
	for _, level := range extendedThinkingLevels {
		mapped, present := levelMap[level]
		if present && mapped == nil {
			continue
		}
		if (level == "xhigh" || level == "max") && !present {
			continue
		}
		supported = append(supported, level)
	}
	if supported == nil {
		supported = []string{}
	}
	return supported
}

// ClampThinkingLevel is pi-ai's clampThinkingLevel: level when the model accepts it, else the nearest accepted level at or above it, else the nearest below it, else the first
// accepted level, else "off". A level that is not one of Pi's is the first accepted level.
func ClampThinkingLevel(model map[string]any, level string) string {
	available := GetSupportedThinkingLevels(model)
	if slices.Contains(available, level) {
		return level
	}
	first := "off"
	if len(available) > 0 {
		first = available[0]
	}
	requested := slices.Index(extendedThinkingLevels, level)
	if requested == -1 {
		return first
	}
	for i := requested; i < len(extendedThinkingLevels); i++ {
		if slices.Contains(available, extendedThinkingLevels[i]) {
			return extendedThinkingLevels[i]
		}
	}
	for i := requested - 1; i >= 0; i-- {
		if slices.Contains(available, extendedThinkingLevels[i]) {
			return extendedThinkingLevels[i]
		}
	}
	return first
}

// jsArithmetic is a number as JavaScript's arithmetic reads it: null is 0, and an absent or non-numeric value is NaN.
func jsArithmetic(object map[string]any, key string) float64 {
	return usageAddend(object, key)
}

// CalculateCost is pi-ai's calculateCost: it prices usage at the model's per-million-token rates and stores the result in usage["cost"] (an object the caller supplies, as in
// Pi), which it also returns. The rates are the model's cost, or those of the tier with the highest inputTokensAbove that the request's input (input, cacheRead and cacheWrite
// tokens) exceeds. Cache writes that usage["cacheWrite1h"] reports are charged at twice the input rate. Numbers are float64 (an int or json.Number is not read: decode with
// the SDK's JSON decoder). usage["cost"] that is not an object is a TypeError in Pi and an error here.
func CalculateCost(model, usage map[string]any) (map[string]any, error) {
	cost, ok := usage["cost"].(map[string]any)
	if !ok {
		return nil, errors.New("TypeError: usage.cost is not an object")
	}
	modelCost, _ := model["cost"].(map[string]any)
	inputTokens := jsArithmetic(usage, "input") + jsArithmetic(usage, "cacheRead") + jsArithmetic(usage, "cacheWrite")
	rates := modelCost
	matchedThreshold := -1.0
	tiers, _ := modelCost["tiers"].([]any)
	for _, raw := range tiers {
		tier, _ := raw.(map[string]any)
		above := jsNumber(tier["inputTokensAbove"])
		if inputTokens > above && above > matchedThreshold {
			rates = tier
			matchedThreshold = above
		}
	}
	// Anthropic charges 2x base input for 1h cache writes.
	longWrite := 0.0
	if value, present := usage["cacheWrite1h"]; present && value != nil {
		longWrite = jsNumber(value)
	}
	cacheWrite := jsArithmetic(usage, "cacheWrite")
	shortWrite := cacheWrite - longWrite
	rateInput, rateOutput, rateCacheRead, rateCacheWrite := jsNumber(rates["input"]), jsNumber(rates["output"]), jsNumber(rates["cacheRead"]), jsNumber(rates["cacheWrite"])
	// float64(...) stops the compiler from fusing a multiply and an add into one rounding, which JavaScript never does.
	input := float64(float64(rateInput/1000000) * jsArithmetic(usage, "input"))
	output := float64(float64(rateOutput/1000000) * jsArithmetic(usage, "output"))
	cacheReadCost := float64(float64(rateCacheRead/1000000) * jsArithmetic(usage, "cacheRead"))
	cacheWriteCost := (float64(rateCacheWrite*shortWrite) + float64(float64(rateInput*2)*longWrite)) / 1000000
	cost["input"], cost["output"], cost["cacheRead"], cost["cacheWrite"] = input, output, cacheReadCost, cacheWriteCost
	cost["total"] = input + output + cacheReadCost + cacheWriteCost
	return cost, nil
}

// DefaultMaxAgentRetryDelayMs is pi-ai's DEFAULT_MAX_AGENT_RETRY_DELAY_MS: the cap on one retry delay when a policy names none.
const DefaultMaxAgentRetryDelayMs = 60_000

// RetryPolicy is the part of pi-ai's RetryPolicy that [RetryDelayMs] reads.
type RetryPolicy struct {
	// BaseDelayMs is the delay before the first retry; attempt n waits BaseDelayMs * 2^(n-1).
	BaseDelayMs float64
	// MaxAgentDelayMs caps each delay; nil is [DefaultMaxAgentRetryDelayMs].
	MaxAgentDelayMs *float64
}

// RetryDelayMs is pi-ai's retryDelayMs: the delay before retry attempt (1-indexed): the policy's base delay doubled for each earlier attempt, a result that is not a safe integer
// counting as the largest safe integer, capped by the policy's MaxAgentDelayMs. It returns a float64 because a non-integer base delay survives the cap.
func RetryDelayMs(policy RetryPolicy, attempt int) float64 {
	delay := policy.BaseDelayMs * math.Pow(2, math.Max(0, float64(attempt-1)))
	const maxSafeInteger = 9007199254740991
	safe := delay
	if math.IsNaN(delay) || math.IsInf(delay, 0) || delay != math.Trunc(delay) || math.Abs(delay) > maxSafeInteger {
		safe = maxSafeInteger
	}
	limit := float64(DefaultMaxAgentRetryDelayMs)
	if policy.MaxAgentDelayMs != nil {
		limit = *policy.MaxAgentDelayMs
	}
	if math.IsNaN(safe) || math.IsNaN(limit) {
		return math.NaN()
	}
	return math.Min(safe, limit)
}

// jsRegexp compiles a pattern written as the source of a JavaScript regular expression with the i flag (and no u flag) into one that matches the way V8 does on the strings
// toJSUnits produces: a letter matches its own two ASCII cases only, in a class too (so `[a-z0-9]` holds A-Z), `.` is any one UTF-16 unit but a line terminator, `\s` and `\S`
// are JavaScript's white space, `\d` an ASCII digit and `\w` an ASCII word character. Only the syntax pi-ai's patterns use is supported; lookaround is not, so a pattern that
// would use it is written with the neighbours it names matched instead.
func jsRegexp(source string) *regexp.Regexp {
	const space = `\t\n\x{0B}\x{0C}\r \x{A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}`
	var out strings.Builder
	for i := 0; i < len(source); i++ {
		c := source[i]
		switch {
		case c == '\\':
			i++
			switch source[i] {
			case 'd':
				out.WriteString(`[0-9]`)
			case 'w':
				out.WriteString(`[0-9A-Za-z_]`)
			case 's':
				out.WriteString(`[` + space + `]`)
			case 'S':
				out.WriteString(`[^` + space + `]`)
			default:
				out.WriteByte('\\')
				out.WriteByte(source[i])
			}
		case c == '[':
			var class string
			class, i = jsClass(source, i)
			out.WriteString(class)
		case c == '.':
			out.WriteString(`[^\n\r\x{2028}\x{2029}]`)
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			out.WriteString("[" + strings.ToLower(string(c)) + strings.ToUpper(string(c)) + "]")
		default:
			out.WriteByte(c)
		}
	}
	return regexp.MustCompile(out.String())
}

// jsClass translates the class that opens at source[open] and returns it with the index of its closing bracket. The i flag makes a class hold both cases of every ASCII letter it
// names, a range of letters included.
func jsClass(source string, open int) (string, int) {
	swap := func(c byte) byte { return c ^ 0x20 }
	isLetter := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	var out strings.Builder
	out.WriteByte('[')
	i := open + 1
	if source[i] == '^' {
		out.WriteByte('^')
		i++
	}
	for ; source[i] != ']'; i++ {
		c := source[i]
		switch {
		case c == '\\':
			i++
			switch source[i] {
			case 'd':
				out.WriteString(`0-9`)
			case 'w':
				out.WriteString(`0-9A-Za-z_`)
			default:
				out.WriteByte('\\')
				out.WriteByte(source[i])
			}
		case source[i+1] == '-' && source[i+2] != ']':
			high := source[i+2]
			out.WriteString(string(c) + "-" + string(high))
			if isLetter(c) && isLetter(high) {
				out.WriteString(string(swap(c)) + "-" + string(swap(high)))
			}
			i += 2
		case isLetter(c):
			out.WriteString(string(c) + string(swap(c)))
		default:
			out.WriteByte(c)
		}
	}
	out.WriteByte(']')
	return out.String(), i
}

// toJSUnits is the text as one rune per UTF-16 code unit, which is what a JavaScript regular expression without the u flag steps over: a character above U+FFFF becomes its two
// surrogate units, each written as a private-use rune that no pattern names.
func toJSUnits(text string) string {
	var out strings.Builder
	for _, unit := range jsUnits(text) {
		if utf16.IsSurrogate(rune(unit)) {
			out.WriteRune(0xF0000 + rune(unit) - 0xD800)
		} else {
			out.WriteRune(rune(unit))
		}
	}
	return out.String()
}

func jsAlternation(patterns ...string) *regexp.Regexp { return jsRegexp(strings.Join(patterns, "|")) }

// overflowPatterns, nonOverflowPatterns and cerebrasBodylessOverflowPattern are overflow.ts's OVERFLOW_PATTERNS, NON_OVERFLOW_PATTERNS and CEREBRAS_BODYLESS_OVERFLOW_PATTERN. Each
// alternative is tried on its own, as Pi's .some() does, so its anchors keep their meaning. Every pattern compiles on first use: the pig binary links this package, and compiling at
// package initialization cost startup allocations on every run (TestPackageInitWorkBudget).
var (
	overflowPatterns = lazyEach(
		`prompt (?:is )?too long`,
		`prompt exceeds max length`,
		`request_too_large`,
		`input is too long for requested model`,
		`exceeds the context window`,
		`exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))`,
		`input token count.*exceeds the maximum`,
		`maximum prompt length is \d+`,
		`reduce the length of the messages`,
		`maximum context length is \d+ tokens`,
		`exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`,
		`input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`,
		`exceeds the limit of \d+`,
		`exceeds the available context size`,
		`greater than the context length`,
		`context window exceeds limit`,
		`exceeded model token limit`,
		`too large for model with \d+ maximum context length`,
		`prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`,
		`model_context_window_exceeded`,
		`prompt too long; exceeded (?:max )?context length`,
		`range of input length should be`,
		`context[_ ]length[_ ]exceeded`,
		`too many tokens`,
		`token limit exceeded`,
		// HTTP 413 means request-size overflow regardless of provider or body text. Match raw SDK errors and formatProviderError's prefix, not a status mentioned in the body.
		`^(?:[\w -]+ API error \(413\):|413(?:\s|:|$))`,
	)
	nonOverflowPatterns = lazyEach(
		`^(Throttling error|Service unavailable):`,
		`rate limit`,
		`too many requests`,
	)
	cerebrasBodylessOverflowPattern = lazyRegexp(`^4(?:00|13)\s*(?:status code)?\s*\(no body\)`)

	nonRetryableProviderLimitErrorPattern = lazyAlternation(
		`GoUsageLimitError`,
		`FreeUsageLimitError`,
		`Monthly usage limit reached`,
		`available balance`,
		`insufficient_quota`,
		`out of budget`,
		`quota exceeded`,
		`billing`,
		`subscription_sharing_usage_limit_exceeded`,
		// Sign in with ChatGPT on a ChatGPT Business plan: a usage limit set by the workspace admin. Only the admin can raise it.
		`subscription_sharing_workplace_individual_usage_limit_exceeded`,
	)
	retryableProviderErrorPattern = lazyAlternation(
		`overloaded`,
		`server_busy`,
		`servers are currently busy`,
		`currently experiencing high demand`,
		`model is at capacity`,
		`rate.?limit`,
		`too many requests`,
		// Status codes must not be part of a longer word or number, e.g. a request id or byte count (#11057). Pi spells it (?<![a-z0-9])(?:429|500|502|503|504|520|524)(?![a-z0-9]);
		// RE2 has no lookaround, so the neighbours are matched, which a boolean test cannot tell apart.
		`(?:^|[^a-z0-9])(?:429|500|502|503|504|520|524)(?:[^a-z0-9]|$)`,
		`service.?(?:is.?)?(?:temporarily.?)?unavailable`,
		`server.?error`,
		`internal.?error`,
		`provider.?returned.?(?:an.?)?error`,
		`exceeded request buffer limit while retrying upstream`,
		`unable to forward upstream`,
		`upstream.?request.?failed`,
		`upstream_stream_read_error`,
		`stream was interrupted`,
		`http/?2 stream failed`,
		`network.?error`,
		`connection.?error`,
		`connection.?refused`,
		`connection.?lost`,
		`other side closed`,
		`fetch failed`,
		`request to \S+ failed, reason:`,
		`getaddrinfo`,
		`ENOTFOUND`,
		`EAI_AGAIN`,
		`upstream.?connect`,
		`reset before headers`,
		`socket hang up`,
		`socket connection was closed`,
		`timed? out`,
		`timeout`,
		`terminated`,
		`websocket.?closed`,
		`websocket.?error`,
		`ended without`,
		`stream ended before message_stop`,
		`stream ended before a terminal response event`,
		`http2 request did not get a response`,
		// Codex CLI mid-stream disconnect, relayed by proxies that wrap the Codex CLI, e.g. "stream disconnected before completion: stream closed before response.completed" (#11057).
		`stream disconnected before completion`,
		`pending stream has been canceled`,
		`retry delay`,
		`you can retry your request`,
		`try your request again`,
		`please retry your request`,
		`ResourceExhausted`,
		`subscription_sharing_usage_unavailable`,
		`subscription_sharing_user_unavailable`,
	)
)

// lazyEach, lazyAlternation and lazyRegexp compile on the first call of the function they return, not when the package initializes.
func lazyEach(patterns ...string) func() []*regexp.Regexp {
	return sync.OnceValue(func() []*regexp.Regexp { return compileEach(patterns...) })
}

func lazyAlternation(patterns ...string) func() *regexp.Regexp {
	return sync.OnceValue(func() *regexp.Regexp { return jsAlternation(patterns...) })
}

func lazyRegexp(source string) func() *regexp.Regexp {
	return sync.OnceValue(func() *regexp.Regexp { return jsRegexp(source) })
}

func compileEach(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		out[i] = jsRegexp(pattern)
	}
	return out
}

func matchesAny(patterns []*regexp.Regexp, text string) bool {
	return slices.ContainsFunc(patterns, func(pattern *regexp.Regexp) bool { return pattern.MatchString(text) })
}

// assistantError reads the fields of an assistant message that the classifiers use: its stop reason and its error message, which is "" when absent (an empty error message is
// falsy in Pi too).
func assistantError(message map[string]any) (stopReason, errorMessage string) {
	stopReason, _ = message["stopReason"].(string)
	errorMessage, _ = message["errorMessage"].(string)
	return stopReason, errorMessage
}

// IsRetryableAssistantError is pi-ai's isRetryableAssistantError: whether a failed assistant message looks like a transient provider or transport error, so the caller can restart
// the turn. It is a classifier, not a retry policy: a quota, billing or subscription-limit message is not retryable. Handle [IsContextOverflow] first.
func IsRetryableAssistantError(message map[string]any) bool {
	stopReason, errorMessage := assistantError(message)
	if stopReason != "error" || errorMessage == "" {
		return false
	}
	text := toJSUnits(errorMessage)
	if nonRetryableProviderLimitErrorPattern().MatchString(text) {
		return false
	}
	return retryableProviderErrorPattern().MatchString(text)
}

// IsContextOverflow is pi-ai's isContextOverflow: whether an assistant message is a context overflow. It is one when it failed with an overflow error message that is not a rate
// limit or throttling message, or when it failed with a Cerebras body-less 400 or 413. With a positive contextWindow (0 is none, as in Pi) it is also one when it stopped normally
// with more input (input and cacheRead tokens) than the window, or stopped at its length limit with no output and input that fills 99% of the window.
func IsContextOverflow(message map[string]any, contextWindow float64) bool {
	stopReason, errorMessage := assistantError(message)
	if stopReason == "error" && errorMessage != "" {
		text := toJSUnits(errorMessage)
		if !matchesAny(nonOverflowPatterns(), text) {
			if matchesAny(overflowPatterns(), text) {
				return true
			}
			if provider, _ := message["provider"].(string); provider == "cerebras" && cerebrasBodylessOverflowPattern().MatchString(text) {
				return true
			}
		}
	}
	if contextWindow != 0 && !math.IsNaN(contextWindow) {
		usage, _ := message["usage"].(map[string]any)
		inputTokens := jsArithmetic(usage, "input") + jsArithmetic(usage, "cacheRead")
		if stopReason == "stop" && inputTokens > contextWindow {
			return true
		}
		if stopReason == "length" && jsNumber(usage["output"]) == 0 && inputTokens >= contextWindow*0.99 {
			return true
		}
	}
	return false
}

// IsRecoverableLength is pi-ai's isRecoverableLength: whether a length stop ended below the output limit the caller or model intended (desiredMaxOutput, before any context-based
// clamping), so one compact-and-retry attempt may help.
func IsRecoverableLength(message map[string]any, desiredMaxOutput float64) bool {
	stopReason, _ := assistantError(message)
	usage, _ := message["usage"].(map[string]any)
	return stopReason == "length" && desiredMaxOutput > 0 && jsNumber(usage["output"]) < desiredMaxOutput
}

// EnvAPIKeyAuth is pi-ai's envApiKeyAuth: the standard API-key auth of a provider whose key comes from a stored credential or an environment variable. A stored credential's key
// wins (its env travels with it); otherwise the first of envVars that the auth context finds non-empty resolves, and none resolving is a nil result. Its Login prompts for the key
// with a secret prompt and returns an api_key credential. The auth has no Check, as in Pi.
func EnvAPIKeyAuth(name string, envVars []string) *APIKeyAuth {
	return &APIKeyAuth{
		Name: name,
		Login: func(interaction AuthInteraction) (*Credential, error) {
			if err := signalErr(interaction.Signal); err != nil {
				return nil, err
			}
			key, err := interaction.Prompt(map[string]any{"type": "secret", "message": "Enter " + name})
			if err != nil {
				return nil, err
			}
			if err := signalErr(interaction.Signal); err != nil {
				return nil, err
			}
			return NewCredential("type", "api_key", "key", key), nil
		},
		Resolve: func(input APIKeyAuthInput) (*AuthResult, error) {
			if err := signalErr(input.Signal); err != nil {
				return nil, err
			}
			if key, present := input.Credential.Get("key"); present && jsTruthy(key) {
				source := "stored credential"
				return &AuthResult{Auth: map[string]any{"apiKey": key}, Env: credentialEnv(input.Credential), Source: &source}, nil
			}
			for _, envVar := range envVars {
				value, err := input.Ctx.Env(envVar)
				if err != nil {
					return nil, err
				}
				if err := signalErr(input.Signal); err != nil {
					return nil, err
				}
				if value != nil && *value != "" {
					source := envVar
					return &AuthResult{Auth: map[string]any{"apiKey": *value}, Source: &source}, nil
				}
			}
			return nil, nil
		},
	}
}

func credentialEnv(credential *Credential) map[string]string {
	value, _ := credential.Get("env")
	var env map[string]string
	set := func(key string, entry any) {
		text, _ := entry.(string)
		if env == nil {
			env = map[string]string{}
		}
		env[key] = text
	}
	switch value := value.(type) {
	case *OrderedObject:
		for _, key := range value.Keys() {
			entry, _ := value.Get(key)
			set(key, entry)
		}
	case map[string]any:
		for key, entry := range value {
			set(key, entry)
		}
	}
	return env
}

// signalErr is signal.throwIfAborted(): the context's error once it is done. A nil signal never aborts.
func signalErr(signal context.Context) error {
	if signal == nil {
		return nil
	}
	return signal.Err()
}

// GetModelType is pi-ai's getModelType: the type of a model, "chat", "image" or "classifier". A model without a type is a chat model.
func GetModelType(model map[string]any) string {
	if kind, ok := model["type"].(string); ok {
		return kind
	}
	return "chat"
}

// IsModelType is pi-ai's isModelType: whether the model is of the type, a model without a type counting as a chat model.
func IsModelType(model map[string]any, modelType string) bool {
	return GetModelType(model) == modelType
}

// HasApi is pi-ai's hasApi: whether the model is a chat model of the API. A model of another type never matches, even when its api names the same API.
func HasApi(model map[string]any, api string) bool {
	modelAPI, _ := model["api"].(string)
	return IsModelType(model, "chat") && modelAPI == api
}

// ModelsAreEqual is pi-ai's modelsAreEqual: whether two models have the same type, id and provider. It is false when either is nil.
func ModelsAreEqual(a, b map[string]any) bool {
	if a == nil || b == nil {
		return false
	}
	return GetModelType(a) == GetModelType(b) && a["id"] == b["id"] && a["provider"] == b["provider"]
}
