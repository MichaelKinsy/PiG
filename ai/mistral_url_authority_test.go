//go:build !pig_strip_mistral_conversations

package ai

import "testing"

func TestPairReviewMistralURLAuthority(t *testing.T) {
	for _, tc := range []struct {
		base, want string
		invalid    bool
	}{
		{"https://%65xample.test/base", "https://example.test/base/v1/chat/completions", false},
		{"https://example.test:00443/base", "https://example.test/base/v1/chat/completions", false},
		{"https:/example.test/base", "https://example.test/base/v1/chat/completions", false},
		{"https:example.test/base", "https://example.test/base/v1/chat/completions", false},
		{"https:////example.test/base", "https://example.test/base/v1/chat/completions", false},
		{"https://example.test:65536/base", "", true},
	} {
		got, err := mistralChatCompletionsURL(tc.base)
		if (err != nil) != tc.invalid || !tc.invalid && got != tc.want {
			t.Errorf("base=%q: got %q,%v want %q invalid=%v", tc.base, got, err, tc.want, tc.invalid)
		}
	}
}
