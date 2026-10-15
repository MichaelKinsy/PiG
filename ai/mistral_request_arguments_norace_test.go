//go:build !race && !pig_strip_mistral_conversations

package ai

import "testing"

// A finished call's arguments reach the Mistral body as the bytes ArgumentsJSON keeps, as on completions: a later request neither copies
// the 200 KB text nor hashes and compares it.
func TestMistralRequestSendsKeptArgumentsWithoutCopying(t *testing.T) {
	messages := largeWriteHistory()
	provider := &mistralProvider{}
	allocated := largeWriteFollowUpBytes(t, func() error {
		_, err := marshalSpliced(provider.convertMessages(messages, false), requestStrings, marshalRequestBody)
		return err
	})
	t.Logf("mistral follow-up request: %d B allocated", allocated)
	if allocated > 64<<10 {
		t.Fatalf("the Mistral request after a 200 KB write call allocates %d bytes, want at most 64 KB", allocated)
	}
}
