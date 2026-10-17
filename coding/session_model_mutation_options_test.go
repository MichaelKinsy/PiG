package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestModelMutationPersistOptions(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		t.Run(map[bool]string{false: "set", true: "cycle"}[cycle], func(t *testing.T) {
			svcs := newTestServices(t)
			if err := svcs.SettingsManager().SetDefaultThinkingLevel("medium"); err != nil {
				t.Fatal(err)
			}
			if err := svcs.SettingsManager().SetModelThinkingLevel("fake", "next", "low"); err != nil {
				t.Fatal(err)
			}
			model := fakeModel()
			model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
			sess, err := NewSession(svcs, SessionOptions{Model: model})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sess.Close() })
			next := *model
			next.ID = "next"
			if cycle {
				err = sess.setModel(&next, extension.ModelSelectSourceCycle, ModelMutationOptions{Persist: true})
			} else {
				err = sess.SetModel(&next, ModelMutationOptions{Persist: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			saved := svcs.SettingsManager().Get()
			if saved.DefaultModel != "next" || saved.DefaultProvider != "fake" || saved.DefaultThinkingLevel != "medium" {
				t.Fatalf("persisted defaults = %s/%s:%s", saved.DefaultProvider, saved.DefaultModel, saved.DefaultThinkingLevel)
			}
			if got := sess.ThinkingLevel(); got != ai.ThinkingLow {
				t.Fatalf("effective thinking = %s", got)
			}
		})
	}
}

// Pi saves the requested level even if clamping makes the effective level unchanged.
func TestThinkingPersistSavesRequestedLevelEvenWithoutEffectiveChange(t *testing.T) {
	for _, maxThinking := range []ai.ModelThinkingLevel{ai.ThinkingHigh, ""} {
		t.Run(string(maxThinking), func(t *testing.T) {
			svcs := newTestServices(t)
			model := fakeModel()
			model.Capabilities.MaxThinking = maxThinking.ReasoningOption()
			sess, err := NewSession(svcs, SessionOptions{Model: model})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sess.Close() })
			if err := sess.SetThinkingLevel(ai.ThinkingMax); err != nil {
				t.Fatal(err)
			}
			before := len(sess.Inner().GetEntries())
			if err := sess.SetThinkingLevel(ai.ThinkingMax, ModelMutationOptions{Persist: true}); err != nil {
				t.Fatal(err)
			}
			if got := svcs.SettingsManager().GetDefaultThinkingLevel(); got != "max" {
				t.Errorf("saved default = %s, want requested max", got)
			}
			if got := sess.ThinkingLevel(); got != ai.ClampThinkingLevel(model, ai.ThinkingMax) {
				t.Errorf("effective level = %s", got)
			}
			if got := len(sess.Inner().GetEntries()); got != before {
				t.Errorf("unchanged effective level added transcript entries: %d -> %d", before, got)
			}
		})
	}
}
