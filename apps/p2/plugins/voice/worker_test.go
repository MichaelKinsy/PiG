package voice

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
)

func TestMain(m *testing.M) { p2test.Main(m) }

func tagOf(tag string) *string { return &tag }

// Ports test/p2/voice.test.ts "voice prompt language".
func TestVoicePromptLanguage(t *testing.T) {
	t.Run("asks for answers in the voice's language, unless it is English or unset", func(t *testing.T) {
		for _, unset := range []*string{nil, tagOf("en"), tagOf("en-GB")} {
			if got := VoicePromptFor(unset); got != VoicePrompt {
				name := "unset"
				if unset != nil {
					name = *unset
				}
				t.Errorf("%s: %q", name, got)
			}
		}
		german := VoicePromptFor(tagOf("de"))
		if !strings.Contains(german, "Speak German: reply in German") {
			t.Errorf("de: %q", german)
		}
		if !strings.Contains(VoicePromptFor(tagOf("pt-BR")), "Speak Brazilian Portuguese") {
			t.Errorf("pt-BR: %q", VoicePromptFor(tagOf("pt-BR")))
		}
		if !strings.HasPrefix(german, VoicePrompt) {
			t.Error("the language follows the spoken style")
		}
	})
}
