package ai

// pi: packages/ai/src/utils/overflow.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type overflowProbe struct {
	StopReason   string  `json:"stopReason"`
	ErrorMessage *string `json:"errorMessage"`
	Provider     string  `json:"provider"`
	Usage        struct {
		Input     int `json:"input"`
		CacheRead int `json:"cacheRead"`
		Output    int `json:"output"`
	} `json:"usage"`
	ContextWindow int `json:"contextWindow"`
	Desired       int `json:"desired"`
}

type overflowAnswer struct {
	Overflow    bool `json:"overflow"`
	Recoverable bool `json:"recoverable"`
}

// overflowProbes perturb each provider's documented error text: letter case (including the Kelvin sign and long s, which Go's (?i) folds and
// JavaScript's non-unicode /i does not), the spaces and separators the patterns spell with \s, U+00A0, U+FEFF and U+2028 (which \s covers in JS but
// not in Go, and which '.' does not cross in JS), newlines inside '.*', digit grouping, and an unrelated prefix, plus every usage-based boundary.
func overflowProbes() []overflowProbe {
	random := rand.New(rand.NewSource(20260935))
	bases := []string{
		"prompt is too long: 213462 tokens > 200000 maximum", "413 {\"error\":{\"type\":\"request_too_large\",\"message\":\"Request exceeds the maximum size\"}}",
		"Your input exceeds the context window of this model", "Requested token count exceeds the model's maximum context length of 131072 tokens",
		"Input length (265330) exceeds model's maximum context length (262144).", "The input token count (1196265) exceeds the maximum number of tokens allowed (1048575)",
		"This model's maximum prompt length is 131072 but the request contains 537812 tokens", "Please reduce the length of the messages or completion",
		"This endpoint's maximum context length is 8192 tokens. However, you requested about 9000 tokens", "Input length 9,000 exceeds the maximum allowed input length of 8,192 tokens.",
		"The input (9000 tokens) is longer than the model's context length (8192 tokens).", "the request exceeds the available context size, try increasing it",
		"tokens to keep from the initial prompt is greater than the context length", "prompt token count of 9000 exceeds the limit of 8192", "invalid params, context window exceeds limit",
		"Your request exceeded model token limit: 9000 (requested: 9100)", "Prompt has 9,000 tokens, but the configured context size is 8,192 tokens", "Prompt contains 9000 tokens ... too large for model with 8192 maximum context length",
		"Prompt too long", "Prompt exceeds max length", "model_context_window_exceeded", "prompt too long; exceeded max context length by 5 tokens", "Range of input length should be [1, 8192]",
		"context_length_exceeded", "Context length exceeded", "Too many tokens", "token limit exceeded", "400 (no body)", "413 status code (no body)", "400 status code(no body)", "413 (no body) trailing",
		"ThrottlingException: Too many tokens, please wait", "Throttling error: Too many tokens", "Service unavailable: prompt is too long", "rate limit: prompt is too long", "Too many requests: context_length_exceeded",
		"unrelated failure", "", "input is too long for requested model", "exceeds maximum context length", "exceeds the model maximum context length of 5 tokens", "exceeds the maximum context length (1,000)",
	}
	swaps := []string{"K", "\u212a", "s", "\u017f", "i", "\u0130", "\u0131", "İ"}
	spaces := []string{" ", "  ", "\t", "\n", "\u00a0", "\ufeff", "\u2028", "\u2029", "\u3000", "\u180e", "\u200b"}
	for _, space := range spaces {
		bases = append(bases, "exceeds maximum context length"+space+"(1,000)", "exceeds the model's maximum context length"+space+space+"(5)", "input token count 12"+space+"exceeds the maximum", "input token count"+space+"exceeds the maximum",
			"413"+space+"status code"+space+"(no body)", "400"+space+"(no body)", "413status code(no body)")
	}
	var probes []overflowProbe
	for range 3500 {
		text := bases[random.Intn(len(bases))]
		switch random.Intn(6) {
		case 0:
			text = strings.ToUpper(text)
		case 1:
			text = strings.ReplaceAll(text, " ", spaces[random.Intn(len(spaces))])
		case 2:
			from := []string{"k", "s", "i", "K", "S", "I"}[random.Intn(6)]
			text = strings.ReplaceAll(text, from, swaps[random.Intn(len(swaps))])
		case 3:
			text = strings.Replace(text, " ", spaces[random.Intn(len(spaces))], 1+random.Intn(3))
		case 4:
			text = []string{"error: ", "HTTP 400 ", "\n", "x"}[random.Intn(4)] + text
		}
		probe := overflowProbe{StopReason: []string{"error", "error", "error", "stop", "length", "toolUse", "aborted"}[random.Intn(7)], Provider: []string{"", "cerebras", "anthropic", "Cerebras"}[random.Intn(4)]}
		if random.Intn(8) != 0 {
			probe.ErrorMessage = &text
		}
		probe.Usage.Input = []int{0, 1, 99, 100, 101, 990, 1000, 1001}[random.Intn(8)]
		probe.Usage.CacheRead = []int{0, 0, 1, 10, 500}[random.Intn(5)]
		probe.Usage.Output = []int{0, 0, 1, 5, 100}[random.Intn(5)]
		probe.ContextWindow = []int{0, 0, 100, 1000, 1010}[random.Intn(5)]
		probe.Desired = []int{-1, 0, 1, 5, 6, 100, 101}[random.Intn(7)]
		probes = append(probes, probe)
	}
	return probes
}

// overflow.ts runs from the installed pi-ai in Node against the same messages: isContextOverflow (the pattern lists with JavaScript /i, \s and '.'
// semantics, the non-overflow exclusions, the cerebras bodyless pattern, the silent-overflow and length-stop usage rules) and isRecoverableLength.
func TestContextOverflowMatchesPiOnSeededMessages(t *testing.T) {
	probes := overflowProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/overflow.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []overflowAnswer
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	overflowed, differing := 0, 0
	for i, probe := range probes {
		message := AssistantMessage{StopReason: StopReason(probe.StopReason), Provider: probe.Provider}
		if probe.ErrorMessage != nil {
			message.ErrorMessage = *probe.ErrorMessage
		}
		message.Usage.Input, message.Usage.CacheRead, message.Usage.Output = probe.Usage.Input, probe.Usage.CacheRead, probe.Usage.Output
		got := overflowAnswer{IsContextOverflow(message, probe.ContextWindow), IsRecoverableLength(message, probe.Desired)}
		if expected[i].Overflow {
			overflowed++
		}
		if got != expected[i] {
			if differing++; differing <= 10 {
				msg := "<none>"
				if probe.ErrorMessage != nil {
					msg = *probe.ErrorMessage
				}
				t.Errorf("%+v message %q differs from Pi:\n  Pig %+v\n  Pi  %+v", probe, msg, got, expected[i])
			}
		}
	}
	if differing > 10 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if overflowed < 300 || overflowed > len(probes)*3/4 {
		t.Errorf("probes are one-sided: Pi reports overflow for %d of %d", overflowed, len(probes))
	}
}
