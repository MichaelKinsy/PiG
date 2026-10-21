package turn

import (
	"strconv"
	"testing"
)

// TestClassifyOracle replays pi-ai utils/retry.ts and utils/overflow.ts (Pi bd3c9b82, oracle/gen_classify.mjs):
// isRetryableAssistantError, isContextOverflow and retryDelayMs over messages built from the providers' error texts.
func TestClassifyOracle(t *testing.T) {
	var retries, overflows, delays int
	for n, f := range corpusLines(t, "classify.hex") {
		switch f[0] {
		case "C":
			message := parseObject(t, unhex(t, f[1]))
			if got, want := IsRetryableAssistantError(message), f[2] == "1"; got != want {
				t.Fatalf("line %d isRetryable(%s) = %v, want %v", n, unhex(t, f[1]), got, want)
			}
			if got, want := IsContextOverflow(message), f[3] == "1"; got != want {
				t.Fatalf("line %d isContextOverflow(%s) = %v, want %v", n, unhex(t, f[1]), got, want)
			}
			if f[2] == "1" {
				retries++
			}
			if f[3] == "1" {
				overflows++
			}
		case "D":
			base, _ := strconv.ParseFloat(f[1], 64)
			var cap *float64
			if f[2] != "-" {
				c, _ := strconv.ParseFloat(f[2], 64)
				cap = &c
			}
			attempt, _ := strconv.ParseFloat(f[3], 64)
			want, _ := strconv.ParseFloat(f[4], 64)
			if got := RetryDelayMs(base, cap, attempt); got != want {
				t.Fatalf("line %d retryDelayMs(%v, %v, %v) = %v, want %v", n, base, f[2], attempt, got, want)
			}
			delays++
		}
	}
	if retries < 100 || overflows < 100 || delays < 50 {
		t.Fatalf("corpus covers %d retryable, %d overflow, %d delay cases", retries, overflows, delays)
	}
}

// Each classification compiles only the patterns it reaches, so an unsupported pattern (compileRx panics) is no longer found
// at package initialization: compile every one here.
func TestEveryClassifierPatternCompiles(t *testing.T) {
	for _, set := range [][]string{nonRetryablePatterns, retryablePatterns, overflowPatterns, nonOverflowPatterns, {cerebrasBodylessPattern}} {
		for _, pattern := range set {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("pattern %q: %v", pattern, r)
					}
				}()
				compileRx(pattern)
			}()
		}
	}
}

// Pi 73f8de80: the generation task classifies an HTTP 413 as context overflow whatever the provider or body, so it compacts
// (generation_classify.go) instead of retrying or failing on a raw API error. Only a failed response counts, and a 413 named
// only in the body or as a prefix of another status does not.
func TestHTTP413IsContextOverflowForEveryProvider(t *testing.T) {
	for _, tc := range []struct {
		stop, provider, text string
		want                 bool
	}{
		{"error", "github-copilot", `github-copilot API error (413): {"message":"failed to parse request","code":""}`, true},
		{"error", "custom-provider", "custom-provider API error (413): Payload Too Large", true},
		{"error", "openai", `413 {"error":{"message":"failed to parse request","code":""}}`, true},
		{"error", "opencode-go", "413 status code (no body)", true},
		{"error", "x", "413", true},
		{"error", "github-copilot", `github-copilot API error (400): {"message":"failed to parse request","code":""}`, false},
		{"error", "x", `github-copilot API error (400): {"message":"failed to parse request: invalid value 413"}`, false},
		{"error", "x", "custom-provider API error (400): upstream mentioned OpenAI API error (413): invalid input", false},
		{"error", "x", "custom-provider API error (4130): invalid input", false},
		{"error", "x", "4130 failed to parse request", false},
		{"aborted", "x", `OpenAI API error (413): {"message":"failed to parse request"}`, false},
		{"stop", "x", `OpenAI API error (413): {"message":"failed to parse request"}`, false},
	} {
		message := jobj("role", "assistant", "stopReason", tc.stop, "provider", tc.provider, "errorMessage", tc.text)
		if got := IsContextOverflow(message); got != tc.want {
			t.Errorf("IsContextOverflow(%s %q) = %v, want %v", tc.stop, tc.text, got, tc.want)
		}
	}
}
