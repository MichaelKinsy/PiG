package turn

import (
	"strconv"
	"testing"
)

// TestClassifyOracle replays pi-ai utils/retry.ts and utils/overflow.ts (pi-durable main da866ada, oracle/gen_classify.mjs):
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
