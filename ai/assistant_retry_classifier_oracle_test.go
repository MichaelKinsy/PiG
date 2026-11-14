package ai

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's isRetryableAssistantError (utils/retry.js) run against the vendored Pi module: both case-insensitive, non-unicode JavaScript RegExps over error text with every separator spelling `.?` accepts (JS `.` excludes \n, \r, U+2028 and U+2029), ASCII-only case folding and UTF-16 units. Known gap, not in the corpus: a supplementary character between the words, which a non-unicode `.?` cannot span (two units) and Go's `.` spans (one code point); lazyregexp.NewJSIgnoreCase documents it.
func TestIsRetryableAssistantErrorMatchesPiOracle(t *testing.T) {
	var messages []string
	phrases := []string{"rate.limit", "timed out", "service unavailable", "provider returned error", "websocket closed", "internal error", "connection refused", "insufficient_quota", "ResourceExhausted", "billing", "quota exceeded", "ENOTFOUND", "retry delay", "ended without", "socket hang up", "connection closed", "Connection closed.", "CONNECTION CLOSED", "connection was closed", "socket connection was closed"}
	separators := []string{" ", "_", "-", "", "\n", "\r", "\u2028", "\u2029", "\t", "\u0085", "\u00a0", "ab", "é", "\v", "\f"}
	cases := []string{"rate%slimit", "service%sunavailable", "provider%sreturned%serror", "websocket%sclosed", "internal%serror", "connection%srefused", "connection%sclosed"}
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
		`{"type":"error","error":{"type":"api_error","message":"Connection closed."}}`, "Connection closed by proxy server before the response completed", `{"type":"error","error":{"type":"api_error","message":"Connection closed. insufficient_quota"}}`, "connection\u2028closed", "connectionclosed", "Connection closed 429 billing",
		// Pi 4699b5b26 (#12128): bare Node socket error codes, their guards on both sides, and what the commit leaves alone.
		"read ECONNRESET", "connect ETIMEDOUT 140.82.113.3:443", "write EPIPE", "connect ECONNREFUSED 127.0.0.1:443", "ECONNABORTED", "Connection reset by peer", "Invalid value EXECONNRESET", "Unknown field ETIMEDOUTX", "Invalid key ERR_EPIPE_ID",
		`400 {"type":"error","error":{"type":"invalid_request_error","message":"Invalid value EXECONNRESET"}}`, `400 {"error":{"message":"the tool reported ECONNRESET while fetching","type":"invalid_request_error"}}`, "400 Invalid value EXECONNRESET",
		"connect EHOSTUNREACH 140.82.113.3:443", "connect ENETUNREACH 140.82.113.3:443", "UND_ERR_SOCKET", "ECONNRESET_", "_ECONNRESET", "0EPIPE", "EPIPE0", "ETIMEDOUTS", "XECONNREFUSED", "ECONNABORTEDLY", "write: broken pipe", "software caused connection abort",
		"read econnreset", "(EPIPE)", "code=ETIMEDOUT;", `{"code":"ECONNABORTED"}`, "Error: ECONNREFUSED.", "read tcp 100.64.0.1:50817->140.82.112.22:443: read: connection reset by peer", "CONNECTION RESET BY PEER", "connection reset\nby peer",
		// The texts of a connection the server ends before a response, as they were and are reported: `connection.?error`, `fetch failed` and `other side closed` retry each.
		"Connection error.", "Connection error.: read ECONNRESET", "Connection error.: write ECONNRESET", "Connection error.: write EPIPE", "Connection error.: other side closed (UND_ERR_SOCKET)", "fetch failed", "fetch failed: read ECONNRESET", "fetch failed: write EPIPE", "fetch failed: other side closed (UND_ERR_SOCKET)",
		"\u212aECONNRESET", "EPIPE\u017f", "ECONNRESET\u212a", "\u017fEPIPE", "EPIPE billing", "ECONNRESET\n", "a\nETIMEDOUT", "éECONNRESET", "EPIPEé", "ECONNRESET ECONNRESETX", "ECONNRESETX ECONNRESET", "EPIPEEPIPE", "E PIPE",
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
