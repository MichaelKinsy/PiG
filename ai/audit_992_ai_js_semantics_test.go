package ai

// Red tests from the 0.99.1 -> 0.99.2 packages/ai audit (docs/plan/progress/audit-992-ai.md). Each case states the
// Pi 0.99.2 behavior that was measured against the installed @earendil-works/pi-ai@0.99.2 package.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// provider-retry.ts:58-63 parses a non-numeric Retry-After with Date.parse. V8 accepts dates net/http.ParseTime
// rejects ("UTC" zone, one-digit day, numeric offset, no weekday), so Pi validates the requested delay (and fails
// when it exceeds maxRetryDelayMs) where Go falls through to exponential backoff.
// Probe (node 24): Date.parse("Thu, 01 Oct 2026 10:00:00 UTC") === Date.parse("Thu, 1 Oct 2026 10:00:00 GMT")
// === Date.parse("Thu, 01 Oct 2026 10:00:00 +0000") === Date.parse("Oct 1 2026 10:00:00 GMT") === 1790848800000.
func TestAudit992RetryAfterAcceptsDateParseFormats(t *testing.T) {
	resetProviderRetry(t)
	providerRetryJitter = func() float64 { return 0 }
	future := time.Now().Add(time.Hour).UTC()
	for _, format := range []string{
		"Mon, 02 Jan 2006 15:04:05 UTC",
		"Mon, 2 Jan 2006 15:04:05 GMT",
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Jan 2 2006 15:04:05 GMT",
	} {
		value := future.Format(format)
		t.Run(value, func(t *testing.T) {
			delay, err := providerRetryDelay(hdr("retry-after", value), 0, 60000, "429")
			if err == nil || !strings.Contains(err.Error(), "retry delay (max: 60s)") {
				t.Fatalf("delay = %v, err = %v; want the server-requested ~3600s delay rejected by the 60s cap", delay, err)
			}
		})
	}
}

// constrained-sampling.ts:65-70 builds the require-mode reason with JSON.stringify(value), which keeps the authored
// key order of an object-valued keyword. Probe: format {"b":2,"a":1} with strict "require" ->
// `Tool "t" requires JSON-schema constrained sampling, but format: {"b":2,"a":1} is unsupported.`
func TestAudit992StrictRequireReasonKeepsObjectKeyOrder(t *testing.T) {
	var tool ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"t","description":"","parameters":{"type":"object","properties":{"a":{"type":"string","format":{"b":2,"a":1}}},"required":["a"]},"constrainedSampling":{"type":"json_schema","strict":"require"}}`), &tool); err != nil {
		t.Fatal(err)
	}
	_, err := resolveJSONSchemaStrictSampling(tool, true, anthropicStrictUnsupportedKeyword)
	want := `Tool "t" requires JSON-schema constrained sampling, but format: {"b":2,"a":1} is unsupported.`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}
