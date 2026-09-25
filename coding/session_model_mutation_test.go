package coding

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's agent-session-model-extension suite keeps ordinary mutations in the
// Session transcript; only an explicit persist request changes global defaults.
func TestModelMutationsAreSessionOnly(t *testing.T) {
	for _, operation := range []string{"set model", "cycle model", "thinking"} {
		t.Run(operation, func(t *testing.T) {
			svcs := newTestServices(t)
			sm := svcs.SettingsManager()
			if err := sm.SetDefaultModelAndProvider("original", "saved"); err != nil {
				t.Fatal(err)
			}
			if err := sm.SetDefaultThinkingLevel("low"); err != nil {
				t.Fatal(err)
			}
			model := fakeModel()
			model.Capabilities.MaxThinking = ai.ThinkingHigh
			sess, err := NewSession(svcs, SessionOptions{Model: model})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sess.Close() })
			path := filepath.Join(svcs.AgentDir(), "settings.json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			next := *model
			next.ID = "next"
			switch operation {
			case "set model":
				err = sess.SetModel(&next)
			case "cycle model":
				err = sess.CycleToModel(&next)
			case "thinking":
				err = sess.SetThinkingLevel(ai.ThinkingHigh)
			}
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("session-only %s rewrote global settings:\nbefore %s\nafter %s", operation, before, after)
			}
			if operation == "thinking" {
				if got := sess.ThinkingLevel(); got != ai.ThinkingHigh {
					t.Errorf("thinking = %s", got)
				}
			} else if sess.Model().ID != "next" {
				t.Errorf("model = %s", sess.Model().ID)
			}
			entries := sess.Inner().Entries()
			last := entries[len(entries)-1].Base.Type
			want := "model_change"
			if operation == "thinking" {
				want = "thinking_level_change"
			}
			if last != want {
				t.Errorf("last transcript entry = %s, want %s", last, want)
			}
		})
	}
}

func TestModelSwitchDoesNotReplaceGlobalThinkingPreference(t *testing.T) {
	svcs := newTestServices(t)
	if err := svcs.SettingsManager().SetDefaultThinkingLevel("medium"); err != nil {
		t.Fatal(err)
	}
	if err := svcs.SettingsManager().SetModelThinkingLevel("fake", "next", "low"); err != nil {
		t.Fatal(err)
	}
	model := fakeModel()
	model.Capabilities.MaxThinking = ai.ThinkingHigh
	sess, err := NewSession(svcs, SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	if err := sess.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	next := *model
	next.ID = "next"
	if err := sess.SetModel(&next); err != nil {
		t.Fatal(err)
	}
	if got := sess.ThinkingLevel(); got != ai.ThinkingLow {
		t.Errorf("per-model thinking = %s", got)
	}
	if err := sess.SetModel(model); err != nil {
		t.Fatal(err)
	}
	if got := sess.ThinkingLevel(); got != ai.ThinkingMedium {
		t.Errorf("return to model without override = %s, want global medium", got)
	}
}
