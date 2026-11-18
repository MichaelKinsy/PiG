package ai

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// openAIStreamTrace keeps bounded evidence when a completions stream ends without finish_reason.
// Evidence is attached to the failed assistant message, not sent to the model.
// pig additive (D237): retain bounded, redacted metadata without changing error text.

const (
	openAIStreamTraceRecords  = 3
	openAIStreamTraceMaxBytes = 2048
)

type openAIStreamTrace struct {
	status    int
	headers   map[string]string
	started   time.Time
	last      []string // ring of the last records, oldest first
	records   int
	bytes     int
	lastAt    time.Time
	firstByte time.Time
}

// openAIStreamTraceHeader reports whether a response header may be kept: identifiers and timing only, never credentials.
func openAIStreamTraceHeader(name string) bool {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "auth"), strings.Contains(name, "cookie"), strings.Contains(name, "token"), strings.Contains(name, "key"), strings.Contains(name, "secret"):
		return false
	case strings.Contains(name, "request-id"), strings.Contains(name, "trace"), strings.HasPrefix(name, "x-github-"), strings.HasPrefix(name, "openai-"):
		return true
	}
	switch name {
	case "content-type", "date", "server", "via", "cf-ray", "x-envoy-upstream-service-time", "retry-after", "x-ms-region", "azureml-model-session":
		return true
	}
	return false
}

func newOpenAIStreamTrace(resp *http.Response) *openAIStreamTrace {
	trace := &openAIStreamTrace{started: time.Now(), headers: map[string]string{}}
	if resp != nil {
		trace.status = resp.StatusCode
		for name, values := range resp.Header {
			if openAIStreamTraceHeader(name) && len(values) > 0 {
				trace.headers[strings.ToLower(name)] = redactStreamTrace(values[0], 256)
			}
		}
	}
	return trace
}

func (trace *openAIStreamTrace) record(data string) {
	if trace == nil {
		return
	}
	now := time.Now()
	if trace.records == 0 {
		trace.firstByte = now
	}
	trace.records++
	trace.bytes += len(data)
	trace.lastAt = now
	if len(trace.last) == openAIStreamTraceRecords {
		trace.last = append(trace.last[:0], trace.last[1:]...)
	}
	trace.last = append(trace.last, redactStreamTrace(data, openAIStreamTraceMaxBytes))
}

// details is the diagnostic payload; the kept records are trimmed from the oldest so they total at most 2 KiB.
func (trace *openAIStreamTrace) details(sawDone bool) JsonObject {
	now := time.Now()
	headers := make([]string, 0, len(trace.headers))
	for name := range trace.headers {
		headers = append(headers, name)
	}
	sort.Strings(headers)
	headerObject := JsonObject{}
	for _, name := range headers {
		headerObject[name] = trace.headers[name]
	}
	last := append([]string(nil), trace.last...)
	total := 0
	for i := len(last) - 1; i >= 0; i-- {
		if total+len(last[i]) > openAIStreamTraceMaxBytes {
			keep := openAIStreamTraceMaxBytes - total
			if keep < 0 {
				keep = 0
			}
			last[i] = truncateUTF8(last[i], keep)
			last = last[i:]
			break
		}
		total += len(last[i])
	}
	records := make([]any, len(last))
	for i, record := range last {
		records[i] = record
	}
	details := JsonObject{
		"httpStatus":    trace.status,
		"headers":       headerObject,
		"doneReceived":  sawDone,
		"records":       trace.records,
		"bytes":         trace.bytes,
		"durationMs":    now.Sub(trace.started).Milliseconds(),
		"lastRecords":   records,
		"msSinceRecord": nil,
	}
	if trace.records > 0 {
		details["msSinceRecord"] = now.Sub(trace.lastAt).Milliseconds()
		details["msToFirstRecord"] = trace.firstByte.Sub(trace.started).Milliseconds()
	}
	return details
}

var streamTraceSecret = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]{8,}|\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16})\b|("(?:[a-z_]*(?:token|secret|password|api_?key|authorization)[a-z_]*)"\s*:\s*")[^"]*`)

// redactStreamTrace masks credential-looking values and bounds text to limit bytes.
func redactStreamTrace(text string, limit int) string {
	text = streamTraceSecret.ReplaceAllStringFunc(text, func(match string) string {
		sub := streamTraceSecret.FindStringSubmatch(match)
		switch {
		case sub[1] != "":
			return sub[1] + "[redacted]"
		case sub[3] != "":
			return sub[3] + "[redacted]"
		}
		return "[redacted]"
	})
	if len(text) > limit {
		const marker = "…[truncated]"
		if limit >= len(marker) {
			text = truncateUTF8(text, limit-len(marker)) + marker
		} else {
			text = truncateUTF8(text, limit)
		}
	}
	return text
}

func truncateUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && limit < len(text) && text[limit]&0xC0 == 0x80 {
		limit--
	}
	return text[:limit]
}
