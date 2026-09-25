package ai

import (
	"strings"
	"testing"
)

func TestClampOpenAIPromptCacheKey(t *testing.T) {
	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "empty", key: "", want: ""},
		{name: "short", key: "session-123", want: "session-123"},
		{name: "exactly 64 runes", key: strings.Repeat("a", openAIPromptCacheKeyMaxLength), want: strings.Repeat("a", openAIPromptCacheKeyMaxLength)},
		{name: "truncate ascii", key: strings.Repeat("a", 70), want: strings.Repeat("a", openAIPromptCacheKeyMaxLength)},
		{name: "truncate multibyte at rune boundary", key: strings.Repeat("🙂", 70), want: strings.Repeat("🙂", openAIPromptCacheKeyMaxLength)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClampOpenAIPromptCacheKey(tt.key)
			if got != tt.want {
				t.Fatalf("ClampOpenAIPromptCacheKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
