package ai

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// retryAfterDates are the forms of one instant that reach Date.parse. provider-retry.ts:58-60, github-copilot.ts:157-158 and
// openai-codex-responses.ts:153-158 read the header with Number.parseFloat or Number first and call Date.parse only when that gives
// NaN, so a value that starts with a digit never gets there: an ISO 8601 date is its leading year in seconds (see
// TestRetryAfterISODateIsItsLeadingYear). What does get there is every date V8 reads that starts with a letter, not only the HTTP-date
// forms. V8 reads a form that names no zone (asctime, "January 2, 2006 15:04:05") in local time, which http.ParseTime reads as UTC.
func retryAfterDates(at time.Time) map[string]string {
	at = at.Truncate(time.Second)
	utc, local := at.UTC(), at.Local()
	return map[string]string{
		"IMF-fixdate":              utc.Format(http.TimeFormat),
		"RFC 850":                  utc.Format("Monday, 02-Jan-06 15:04:05 GMT"),
		"asctime in local time":    local.Format("Mon Jan _2 15:04:05 2006"),
		"month name with GMT":      utc.Format("January 2, 2006 15:04:05 GMT"),
		"month name in local time": local.Format("January 2, 2006 15:04:05"),
		"month name with offset":   at.In(time.FixedZone("", 5*3600+1800)).Format("Jan 2 2006 15:04:05 -0700"),
		"lower case IMF-fixdate":   strings.ToLower(utc.Format(http.TimeFormat)),
	}
}

func TestProviderRetryDelayAcceptsEveryDateParseForm(t *testing.T) {
	resetProviderRetry(t)
	providerRetryJitter = func() float64 { return 0 }
	for name, value := range retryAfterDates(time.Now().Add(30 * time.Second)) {
		t.Run(name, func(t *testing.T) {
			got, err := providerRetryDelay(hdr("retry-after", value), 0, 60000, "503")
			if err != nil || got < 28*time.Second || got > 30*time.Second {
				t.Fatalf("retry-after %q: delay = %v, err = %v; want about 30s", value, got, err)
			}
		})
	}
	// provider-retry.ts:61-62,65: a date Date.parse rejects is NaN, which is not finite, so the delay is the exponential backoff.
	for _, value := range []string{"tomorrow", "Thu, 32 Jan 2026 00:00:00 GMT", "January 2026 10:99", "Thu, 01 Jan 2026 25:00:00 GMT"} {
		got, err := providerRetryDelay(hdr("retry-after", value), 2, 60000, "503")
		if err != nil || got != 2*time.Second {
			t.Fatalf("retry-after %q: delay = %v, err = %v; want exponential 2s", value, got, err)
		}
	}
}

func TestCopilotRetryDelayAcceptsEveryDateParseForm(t *testing.T) {
	for name, value := range retryAfterDates(time.Now().Add(30 * time.Second)) {
		t.Run(name, func(t *testing.T) {
			got, ok := copilotRetryDelay(value, 0)
			if !ok || got < 28000 || got > 30000 {
				t.Fatalf("retry-after %q: delay = %vms, ok = %v; want about 30000ms", value, got, ok)
			}
		})
	}
	// github-copilot.ts:159: a delay that is not finite returns the 429 response itself.
	if _, ok := copilotRetryDelay("Thu, 32 Jan 2026 00:00:00 GMT", 0); ok {
		t.Fatal("a date Date.parse rejects must not produce a retry delay")
	}
}

func TestCodexRetryDelayAcceptsEveryDateParseForm(t *testing.T) {
	for name, value := range retryAfterDates(time.Now().Add(30 * time.Second)) {
		t.Run(name, func(t *testing.T) {
			got, err := codexRetryDelay(hdr("retry-after", value), 0, 60000)
			if err != nil || got < 28*time.Second || got > 30*time.Second {
				t.Fatalf("retry-after %q: delay = %v, err = %v; want about 30s", value, got, err)
			}
		})
	}
	// openai-codex-responses.ts:153 reads the header with Number, which unlike parseFloat rejects "2026-10-01T00:00:00Z", so ISO 8601 dates reach Date.parse there.
	for name, value := range map[string]string{
		"ISO 8601 with Z":      time.Now().Add(30 * time.Second).UTC().Format("2006-01-02T15:04:05Z"),
		"ISO 8601 with millis": time.Now().Add(30 * time.Second).UTC().Format("2006-01-02T15:04:05.000Z"),
		"ISO 8601 with offset": time.Now().Add(30 * time.Second).In(time.FixedZone("", 5*3600+1800)).Format("2006-01-02T15:04:05-07:00"),
		"ISO 8601 local time":  time.Now().Add(30 * time.Second).Local().Format("2006-01-02T15:04:05"),
	} {
		got, err := codexRetryDelay(hdr("retry-after", value), 0, 60000)
		if err != nil || got < 28*time.Second || got > 30*time.Second {
			t.Fatalf("%s %q: delay = %v, err = %v; want about 30s", name, value, got, err)
		}
	}
	got, err := codexRetryDelay(hdr("retry-after", "2026-13-01T00:00:00Z"), 1, 60000)
	if err != nil || got != 2*time.Second {
		t.Fatalf("an ISO date Date.parse rejects: delay = %v, err = %v; want exponential 2s", got, err)
	}
	// The caller's 2^attempt seconds fallback.
	got, err = codexRetryDelay(hdr("retry-after", "Thu, 32 Jan 2026 00:00:00 GMT"), 2, 60000)
	if err != nil || got != 4*time.Second {
		t.Fatalf("a date Date.parse rejects: delay = %v, err = %v; want exponential 4s", got, err)
	}
	// V8 reads a two-digit year of 50 to 99 as 19xx (dateparser.cc DayComposer::Write); http.ParseTime reads 50 to 68 as 20xx. A past date is clamped to zero (Math.max(0, date - Date.now())).
	got, err = codexRetryDelay(hdr("retry-after", "Sunday, 06-Nov-55 08:49:37 GMT"), 0, 60000)
	if err != nil || got != 0 {
		t.Fatalf("RFC 850 date in 1955: delay = %v, err = %v; want 0", got, err)
	}
}

// Number.parseFloat reads the leading number of an ISO 8601 date, so provider-retry.ts:58 and github-copilot.ts:157 retry after that
// many seconds and never reach Date.parse; this is Pi's behavior for the form, not a missing date parser.
func TestRetryAfterISODateIsItsLeadingYear(t *testing.T) {
	resetProviderRetry(t)
	got, err := providerRetryDelay(hdr("retry-after", "2026-10-01T00:00:00Z"), 0, 0, "503")
	if err != nil || got != 2026*time.Second {
		t.Fatalf("delay = %v, err = %v; want 2026s", got, err)
	}
	if ms, ok := copilotRetryDelay("2026-10-01T00:00:00Z", 0); !ok || ms != 2026000 {
		t.Fatalf("copilot delay = %v, ok = %v; want 2026000ms", ms, ok)
	}
}
