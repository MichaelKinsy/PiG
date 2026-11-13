package coding

import (
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/ai"
)

// The CLI renders the system prompt before it starts the Session. A Session whose own rendering of its sections is the same text keeps the caller's string: the prompt holds the project's context files, so a second copy of it was 65 KB of the live heap at an interactive start's first frame in this repository.
func TestSessionKeepsTheCallersSystemPromptStringWhenTheTextIsEqual(t *testing.T) {
	sections := ai.OrderedSections{{Name: "preamble", Value: new(strings.Repeat("instructions ", 64))}}
	known := strings.Clone(ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: sections}}))
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SystemPrompt: known, SystemPromptSections: sections, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	got := *sess.baseSystemPrompt.Load()
	if got != known {
		t.Fatalf("base prompt %q, want the text of its sections %q", got, known)
	}
	if unsafe.StringData(got) != unsafe.StringData(known) { //nolint:gosec // G103: the test compares the data address of strings that must share
		t.Fatal("the Session holds a second copy of the caller's equal system prompt")
	}
}

// A caller's prompt that differs from the sections' text is not the Session's base prompt.
func TestSessionBuildsItsBaseSystemPromptFromSectionsWhenTheCallersTextDiffers(t *testing.T) {
	sections := ai.OrderedSections{{Name: "preamble", Value: new("from sections")}}
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), SystemPrompt: "from the caller", SystemPromptSections: sections, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := sess.Close(); err != nil {
			t.Error(err)
		}
	}()
	want := ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: sections}})
	if got := *sess.baseSystemPrompt.Load(); got != want {
		t.Fatalf("base prompt %q, want %q", got, want)
	}
}
