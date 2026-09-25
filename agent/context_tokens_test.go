package agent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestCalculateContextTokens(t *testing.T) {
	cases := []struct {
		name string
		u    ai.Usage
		want int
	}{
		{
			name: "all_fields",
			u:    ai.Usage{Input: 10, Output: 5, CacheRead: 20, CacheWrite: 3},
			want: 38,
		},
		{
			name: "zero",
			u:    ai.Usage{},
			want: 0,
		},
		{
			name: "input_only",
			u:    ai.Usage{Input: 50},
			want: 50,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CalculateContextTokens(tc.u); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestEstimateTokens(t *testing.T) {
	t.Run("user_message_400_chars", func(t *testing.T) {
		text := strings.Repeat("a", 400)
		msg := AgentMessage{
			User: &UserMessage{
				Role:    "user",
				Content: []ai.UserContentBlock{ai.TextContent{Text: text}},
			},
		}
		got := EstimateTokens(msg)
		// ceil(400/4) = 100
		if got != 100 {
			t.Errorf("got %d, want 100", got)
		}
	})

	t.Run("tool_result_8000_chars", func(t *testing.T) {
		text := strings.Repeat("x", 8000)
		msg := AgentMessage{
			ToolResult: &ToolResultMessage{
				Role:    RoleToolResult,
				Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}},
			},
		}
		got := EstimateTokens(msg)
		// ceil(8000/4) = 2000
		if got != 2000 {
			t.Errorf("got %d, want 2000", got)
		}
	})

	t.Run("compaction_summary_counts_summary_only", func(t *testing.T) {
		msg := AgentMessage{Custom: map[string]any{
			"role":    RoleCompactionSummary,
			"summary": strings.Repeat("s", 101),
		}}
		if got := EstimateTokens(msg); got != 26 {
			t.Errorf("got %d, want 26", got)
		}
	})

	t.Run("empty_message", func(t *testing.T) {
		got := EstimateTokens(AgentMessage{})
		if got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})
}

// TestEstimateContextTokens covers the two branches: usage present (usage +
// trailing estimate) and no usage (full estimate, LastUsageIndex == -1).
func TestEstimateContextTokens(t *testing.T) {
	t.Run("no_usage_full_estimate", func(t *testing.T) {
		msgs := []AgentMessage{
			{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: strings.Repeat("a", 400)}}}},
		}
		est := EstimateContextTokens(msgs)
		if est.LastUsageIndex != -1 {
			t.Fatalf("LastUsageIndex = %d, want -1", est.LastUsageIndex)
		}
		if est.Tokens != 100 || est.TrailingTokens != 100 {
			t.Fatalf("Tokens=%d TrailingTokens=%d, want 100/100", est.Tokens, est.TrailingTokens)
		}
	})

	t.Run("usage_plus_trailing", func(t *testing.T) {
		msgs := []AgentMessage{
			{Assistant: &AssistantMessage{Role: "assistant", StopReason: "stop", Usage: &ai.Usage{Input: 1000}}},
			{ToolResult: &ToolResultMessage{Role: RoleToolResult, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("x", 8000)}}}},
		}
		est := EstimateContextTokens(msgs)
		if est.LastUsageIndex != 0 {
			t.Fatalf("LastUsageIndex = %d, want 0", est.LastUsageIndex)
		}
		// usage 1000 + ceil(8000/4)=2000 trailing
		if est.UsageTokens != 1000 || est.TrailingTokens != 2000 || est.Tokens != 3000 {
			t.Fatalf("UsageTokens=%d TrailingTokens=%d Tokens=%d, want 1000/2000/3000", est.UsageTokens, est.TrailingTokens, est.Tokens)
		}
	})

	t.Run("error_and_aborted_skipped_for_usage", func(t *testing.T) {
		msgs := []AgentMessage{
			{Assistant: &AssistantMessage{Role: "assistant", StopReason: "stop", Usage: &ai.Usage{Input: 500}}},
			{Assistant: &AssistantMessage{Role: "assistant", StopReason: "error", Usage: &ai.Usage{Input: 999999}}},
			{Assistant: &AssistantMessage{Role: "assistant", StopReason: "aborted", Usage: &ai.Usage{Input: 999999}}},
		}
		est := EstimateContextTokens(msgs)
		if est.LastUsageIndex != 0 || est.UsageTokens != 500 {
			t.Fatalf("LastUsageIndex=%d UsageTokens=%d, want 0/500 (error+aborted usage ignored)", est.LastUsageIndex, est.UsageTokens)
		}
	})

	// Upstream getAssistantUsage skips all-zero usage, so a provider that
	// reports no usage falls back to estimating every message.
	t.Run("all_zero_usage_skipped", func(t *testing.T) {
		msgs := []AgentMessage{
			{User: &UserMessage{Role: "user", Content: []ai.UserContentBlock{ai.TextContent{Text: strings.Repeat("u", 40)}}}},
			{Assistant: &AssistantMessage{Role: "assistant", StopReason: "stop", Content: []ai.AssistantContentBlock{ai.TextContent{Text: strings.Repeat("a", 80)}}, Usage: &ai.Usage{}}},
		}
		est := EstimateContextTokens(msgs)
		if est.LastUsageIndex != -1 || est.UsageTokens != 0 || est.Tokens != 30 || est.TrailingTokens != 30 {
			t.Fatalf("estimate = %+v, want full 10+20 estimate with no usage index", est)
		}

		withEarlier := append([]AgentMessage{{Assistant: &AssistantMessage{Role: "assistant", StopReason: "stop", Usage: &ai.Usage{TotalTokens: 7}}}}, msgs...)
		est = EstimateContextTokens(withEarlier)
		if est.LastUsageIndex != 0 || est.UsageTokens != 7 || est.Tokens != 37 {
			t.Fatalf("estimate = %+v, want earlier non-zero usage 7 + trailing 30", est)
		}
	})
}
