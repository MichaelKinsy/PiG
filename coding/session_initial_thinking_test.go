package coding

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi sdk.ts:231-255 gives an explicit thinking level precedence over model/global preferences and clamps before persistence.
func TestInitialThinkingPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name           string
		model          bool
		explicit, want ai.ModelThinkingLevel
	}{
		{"explicit off", true, ai.ThinkingOff, ai.ThinkingOff},
		{"model preference", true, "", ai.ThinkingLow},
		{"clamp", true, ai.ThinkingMax, ai.ThinkingHigh},
		{"unknown model", false, ai.ThinkingHigh, ai.ThinkingOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svcs := newTestServices(t)
			if err := svcs.SettingsManager().SetDefaultThinkingLevel("medium"); err != nil {
				t.Fatal(err)
			}
			if err := svcs.SettingsManager().SetModelThinkingLevel("fake", "fake-1", "low"); err != nil {
				t.Fatal(err)
			}
			var model *ai.Model
			if tc.model {
				model = fakeModel()
				model.ProviderMeta.ProviderID = "fake"
				model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
			}
			session, err := NewSession(svcs, SessionOptions{Model: model, ThinkingLevel: tc.explicit, SkipBuiltinTools: true, NoSession: true})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
			}()
			if got := session.ThinkingLevel(); got != tc.want {
				t.Fatalf("state=%s want=%s", got, tc.want)
			}
			var levels []ai.ModelThinkingLevel
			for _, entry := range session.Entries() {
				if entry.Base().Type != "thinking_level_change" {
					continue
				}
				var value struct {
					ThinkingLevel ai.ModelThinkingLevel `json:"thinkingLevel"`
				}
				if err := json.Unmarshal(entry.Raw(), &value); err != nil {
					t.Fatal(err)
				}
				levels = append(levels, value.ThinkingLevel)
			}
			if len(levels) != 1 || levels[0] != tc.want {
				t.Fatalf("initial levels=%v want=%s", levels, tc.want)
			}
		})
	}
}
