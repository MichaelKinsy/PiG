package ai

import (
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

// openai-codex-responses.ts:1605-1614: a 429 or usage-limit error names the plan and, when resets_at is a non-zero time, the minutes
// until it as Math.max(0, Math.round(...)); a zero resets_at is falsy, so no "Try again" part is added.
func TestCodexHTTPErrorUsageLimitMessage(t *testing.T) {
	inMinutes := func(minutes float64) float64 {
		return float64(time.Now().Add(time.Duration(minutes * float64(time.Minute))).Unix())
	}
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"no reset time", `{"error":{"code":"usage_limit_reached","plan_type":"PRO"}}`, "You have hit your ChatGPT usage limit (pro plan)."},
		{"zero reset time", `{"error":{"code":"usage_limit_reached","resets_at":0}}`, "You have hit your ChatGPT usage limit."},
		{"future reset", fmt.Sprintf(`{"error":{"type":"rate_limit_exceeded","resets_at":%v}}`, inMinutes(30)), "You have hit your ChatGPT usage limit. Try again in ~30 min."},
		{"past reset clamps to zero", fmt.Sprintf(`{"error":{"code":"usage_not_included","resets_at":%v}}`, inMinutes(-90)), "You have hit your ChatGPT usage limit. Try again in ~0 min."},
	} {
		err := codexHTTPError(400, "Bad Request", []byte(tc.body))
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
	if err := codexHTTPError(429, "Too Many Requests", []byte(`{"error":{"message":"slow down"}}`)); err == nil || err.Error() != "You have hit your ChatGPT usage limit." {
		t.Errorf("429 without a code: %v", err)
	}
}

// parseErrorResponse's message is `raw || response.statusText || "Request failed"` (openai-codex-responses.ts:1598): an empty body names the reason
// phrase the server sent, and a response without one says "Request failed".
func TestCodexHTTPErrorEmptyBodyFallsBackToTheReasonPhrase(t *testing.T) {
	for _, tc := range []struct{ statusText, want string }{{"Bad Gateway", "Bad Gateway"}, {"status code 529", "status code 529"}, {"", "Request failed"}} {
		if err := codexHTTPError(502, tc.statusText, nil); err == nil || err.Error() != tc.want {
			t.Errorf("statusText %q: %v, want %q", tc.statusText, err, tc.want)
		}
	}
}

// The resets_at product rounds before Date.now() is subtracted, as in JavaScript (openai-codex-responses.ts:1612). At
// Date.now() = 946684800002, node -e 'console.log(Math.round((946684830.002*1000 - 946684800002)/60000))' prints 1: the
// rounded product is exactly 30 s ahead. One fused multiply-subtract keeps the product's error and rounds to 0.
func TestCodexHTTPErrorResetMinutesRoundTheProductFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		time.Sleep(time.Until(time.UnixMilli(946684800002)))
		err := codexHTTPError(429, "Too Many Requests", []byte(`{"error":{"code":"usage_limit_reached","resets_at":946684830.002}}`))
		if want := "You have hit your ChatGPT usage limit. Try again in ~1 min."; err == nil || err.Error() != want {
			t.Fatalf("%v, want %q", err, want)
		}
	})
}
