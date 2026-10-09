package ai

import (
	"fmt"
	"testing"
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
		err := codexHTTPError(400, []byte(tc.body))
		if err == nil || err.Error() != tc.want {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
	if err := codexHTTPError(429, []byte(`{"error":{"message":"slow down"}}`)); err == nil || err.Error() != "You have hit your ChatGPT usage limit." {
		t.Errorf("429 without a code: %v", err)
	}
}
