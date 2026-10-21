package ai

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's isRetryableAssistantError (utils/retry.js) run against the vendored Pi module: both case-insensitive, non-unicode JavaScript RegExps over error text with every separator spelling `.?` accepts (JS `.` excludes \n, \r, U+2028 and U+2029), ASCII-only case folding and UTF-16 units. Known gap, not in the corpus: a supplementary character between the words, which a non-unicode `.?` cannot span (two units) and Go's `.` spans (one code point); lazyregexp.NewJSIgnoreCase documents it.
func TestIsRetryableAssistantErrorMatchesPiOracle(t *testing.T) {
	var messages []string
	phrases := []string{"rate.limit", "timed out", "service unavailable", "provider returned error", "websocket closed", "internal error", "connection refused", "insufficient_quota", "ResourceExhausted", "billing", "quota exceeded", "ENOTFOUND", "retry delay", "ended without", "socket hang up"}
	separators := []string{" ", "_", "-", "", "\n", "\r", "\u2028", "\u2029", "\t", "\u0085", "\u00a0", "ab", "é", "\v", "\f"}
	cases := []string{"rate%slimit", "service%sunavailable", "provider%sreturned%serror", "websocket%sclosed", "internal%serror", "connection%srefused"}
	for _, format := range cases {
		for _, separator := range separators {
			messages = append(messages, replaceAllSep(format, separator), "Error: "+replaceAllSep(format, separator)+" (retry later)")
		}
	}
	for _, phrase := range phrases {
		messages = append(messages, phrase, "PREFIX "+phrase+" SUFFIX", "xx"+phrase, phrase+"\n", "a\n"+phrase)
	}
	messages = append(messages,
		"", " ", "429", "HTTP 429 quota exceeded", "429 Billing issue", "BILLING", "billing\n429", "GoUsageLimitError 503", "OVERLOADED", "Overloaded", "überloaded", "OVERLOADED\u212a", "\u212aey", "RATE\u017f", "timed\u212a out", "timeout", "TIMEOUT", "timed  out",
		"5000", "429x", "x429", "_429_", "(503)", "req_011CU503xyz", "400 image exceeds 5 MB maximum: 5316784 bytes > 5242880 bytes", "stream disconnected before completion: stream closed before response.completed", "subscription_sharing_workplace_individual_usage_limit_exceeded 429",
		"ſerver error", "server\u00a0error", "SERVER_ERROR", "sErVeR.eRrOr", "5xx", "500", "5000", "x500x", "50", "reset before headers", "RESET BEFORE HEADERS", "upstream\u2028connect", "upstream\u2029connect", "upstream\nconnect", "upstream\rconnect", "upstream connect",
		"😀 429", "stream ended before message_stop", "stream ended before a terminal response event", "HTTP2 request did not get a response", "pending stream has been canceled", "you can retry your request", "TRY YOUR REQUEST AGAIN",
		"subscription_sharing_usage_limit_exceeded 503", "subscription_sharing_usage_unavailable", "Monthly usage limit reached 429", "available balance", "AVAILABLE BALANCE 500", "out of budget", "Out Of Budget",
	)
	type result struct {
		Retryable bool `json:"retryable"`
	}
	var want []result
	pioracle.Run(t, `
const mod = await load("pi-ai/utils/retry.js");
emit(input.flatMap((m) => [
	{ retryable: mod.isRetryableAssistantError({ stopReason: "error", errorMessage: m }) },
]));`, messages, &want)
	for i, message := range messages {
		got := IsRetryableAssistantError(AssistantMessage{StopReason: StopReasonError, ErrorMessage: message})
		if got != want[i].Retryable {
			t.Errorf("errorMessage=%q: Go %v, Pi %v", message, got, want[i].Retryable)
		}
	}
}

func replaceAllSep(format, separator string) string {
	out := ""
	for i := 0; i < len(format); i++ {
		if format[i] == '%' && i+1 < len(format) && format[i+1] == 's' {
			out += separator
			i++
			continue
		}
		out += string(format[i])
	}
	return out
}
