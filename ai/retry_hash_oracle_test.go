package ai

// pi: packages/ai/src/utils/retry.ts
// pi: packages/ai/src/utils/hash.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type retryHashProbe struct {
	Op           string  `json:"op"`
	StopReason   string  `json:"stopReason"`
	ErrorMessage *string `json:"errorMessage"`
	Base         int     `json:"base"`
	Cap          *int    `json:"cap"`
	Attempt      int     `json:"attempt"`
	Text         string  `json:"text"`
}

// retryHashProbes perturb provider error texts that name each retryable and each non-retryable limit condition (letter case including the Kelvin
// sign, the separators the patterns spell with '.?', newlines and U+2028 in them, status codes inside longer numbers), vary retry delays across
// attempts, bases and caps up to and past 2^53, and hash strings with surrogate pairs, lone-surrogate-free non-ASCII text and long inputs.
func retryHashProbes() []retryHashProbe {
	random := rand.New(rand.NewSource(20260937))
	retryable := []string{"overloaded", "server_busy", "servers are currently busy", "rate limit", "rate-limit", "ratelimit", "Too Many Requests", "HTTP 429", "status 503", "error 5000", "service unavailable",
		"service_unavailable", "server error", "internal  error", "provider returned error", "network error", "connection refused", "connection-lost", "other side closed", "fetch failed", "getaddrinfo ENOTFOUND",
		"EAI_AGAIN", "upstream connect error", "socket hang up", "timed out", "time out", "timeout", "terminated", "websocket closed", "stream ended before message_stop", "retry delay 5s", "please retry your request",
		"ResourceExhausted", "subscription_sharing_usage_unavailable", "ended without a terminal event"}
	blocked := []string{"GoUsageLimitError", "FreeUsageLimitError", "Monthly usage limit reached", "available balance", "insufficient_quota", "out of budget", "quota exceeded", "billing", "subscription_sharing_usage_limit_exceeded"}
	separators := []string{" ", "-", "_", "", "\n", "\u2028", "\u2029", "\r", "\t", "\u00a0", "ab"}
	var probes []retryHashProbe
	for range 2500 {
		text := retryable[random.Intn(len(retryable))]
		if random.Intn(3) == 0 {
			text += " " + blocked[random.Intn(len(blocked))]
		}
		switch random.Intn(5) {
		case 0:
			text = strings.ToUpper(text)
		case 1:
			text = strings.NewReplacer(" ", separators[random.Intn(len(separators))], "-", separators[random.Intn(len(separators))], "_", separators[random.Intn(len(separators))]).Replace(text)
		case 2:
			text = strings.ReplaceAll(text, "k", "\u212a")
		case 3:
			text = "x" + text + "y"
		}
		probe := retryHashProbe{Op: "retryable", StopReason: []string{"error", "error", "error", "stop", "aborted"}[random.Intn(5)]}
		if random.Intn(10) != 0 {
			probe.ErrorMessage = &text
		}
		probes = append(probes, probe)
	}
	for _, text := range blocked {
		for _, wrapper := range []string{"%s", "x %s y", "429 %s"} {
			text := strings.Replace(wrapper, "%s", text, 1)
			probes = append(probes, retryHashProbe{Op: "retryable", StopReason: "error", ErrorMessage: &text})
		}
	}
	for range 800 {
		probe := retryHashProbe{Op: "delay", Base: []int{0, 1, 100, 1000, 2000, 1 << 40, 1 << 52, 1<<53 - 1, -5}[random.Intn(9)], Attempt: random.Intn(80) - 3}
		if random.Intn(2) == 0 {
			probe.Cap = new([]int{0, 1, 500, 60000, 1 << 40, 1<<53 - 1, -1}[random.Intn(7)])
		}
		probes = append(probes, probe)
	}
	words := []string{"a", "toolu_01ABC", "call_123|fc_456", "é", "日本語", "😀", "👨\u200d👩\u200d👧", "x", "", "\x00", "~", "𝐀"}
	for range 600 {
		var text string
		for range random.Intn(30) {
			text += words[random.Intn(len(words))]
		}
		probes = append(probes, retryHashProbe{Op: "hash", Text: text})
	}
	return probes
}

// retry.ts and hash.ts run from the installed pi-ai in Node against the same probes: isRetryableAssistantError (the retryable and non-retryable
// patterns with JavaScript /i and '.?' semantics), retryDelayMs (the doubling, the 2^53 saturation and the cap) and shortHash (UTF-16 code units
// through Math.imul) must match Pi's.
func TestRetryAndHashMatchPiOnSeededProbes(t *testing.T) {
	probes := retryHashProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/retry_hash.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	retried, differing := 0, 0
	for i, probe := range probes {
		var got any
		switch probe.Op {
		case "retryable":
			message := AssistantMessage{StopReason: StopReason(probe.StopReason)}
			if probe.ErrorMessage != nil {
				message.ErrorMessage = *probe.ErrorMessage
			}
			got = IsRetryableAssistantError(message)
			if got == true {
				retried++
			}
		case "delay":
			got = float64(RetryDelayMs(RetryPolicy{BaseDelayMs: probe.Base, MaxAgentDelayMs: probe.Cap}, probe.Attempt))
		default:
			got = ShortHash(probe.Text)
		}
		if got != expected[i] {
			if differing++; differing <= 10 {
				t.Errorf("%+v differs from Pi: Pig %v, Pi %v", probe, got, expected[i])
			}
		}
	}
	if differing > 10 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if retried < 500 || retried > 2000 {
		t.Errorf("probes are one-sided: Pi retries %d", retried)
	}
}
