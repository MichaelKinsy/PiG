package ai

import (
	"net/http"
	"testing"
	"time"
)

// openai-codex-responses.ts:140-156 getRetryAfterDelayMs reads retry-after-ms and retry-after with Number(headers.get(name)). Headers.get joins repeated lines with ", " and returns the raw bytes as a ByteString (one code unit per byte), and Number trims JavaScript whitespace and reads 0x/0o/0b integers but not Go's hexadecimal floats. The expected values are Node 24's Number(new Headers(...).get(name)) for each header.
func TestCodexRetryDelayReadsHeadersAsNumberDoes(t *testing.T) {
	const backoff = 2 * time.Second // attempt 1: no usable header, 2^attempt seconds
	for _, tc := range []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{"hex integer ms", http.Header{"Retry-After-Ms": {"0x10"}}, 16 * time.Millisecond},
		{"exponent ms", http.Header{"Retry-After-Ms": {"1e3"}}, time.Second},
		{"Go hex float is NaN", http.Header{"Retry-After-Ms": {"0x1p3"}}, backoff},
		{"Latin-1 no-break space is trimmed", http.Header{"Retry-After-Ms": {"\xa0250"}}, 250 * time.Millisecond},
		{"UTF-8 byte order mark is three Latin-1 characters", http.Header{"Retry-After-Ms": {"\xef\xbb\xbf250"}}, backoff},
		{"repeated lines join with a comma", http.Header{"Retry-After-Ms": {"100", "200"}}, backoff},
		{"empty ms is zero", http.Header{"Retry-After-Ms": {""}}, 0},
		{"infinite ms falls through to retry-after", http.Header{"Retry-After-Ms": {"Infinity"}, "Retry-After": {"3"}}, 3 * time.Second},
		{"hex integer seconds", http.Header{"Retry-After": {"0x3"}}, 3 * time.Second},
		{"repeated retry-after lines join with a comma", http.Header{"Retry-After": {"100", "200"}}, backoff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := codexRetryDelay(tc.header, 1, 60000)
			if err != nil || got != tc.want {
				t.Fatalf("delay = %v, err = %v; want %v", got, err, tc.want)
			}
		})
	}
}
