package codemode

// pi: packages/coding-agent/src/core/settings-defaults.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// SETTINGS_DEFAULTS.codemode is what the codemode extension uses when no setting is given: mode `on` (index.ts readMode, tool.ts getMode ?? default) and the
// inline budget Pi exports as DEFAULT_CODEMODE_INLINE_BUDGET. PiG's Options with no settings and no overrides must resolve the same mode, and its default
// budget must be Pi's.
func TestCodemodeDefaultsAreThePinnedPiSettingsDefaults(t *testing.T) {
	var pi struct {
		Mode         string  `json:"mode"`
		InlineBudget float64 `json:"inlineBudget"`
		ToolBudget   float64 `json:"toolBudget"`
	}
	pioracle.Run(t, `
const { SETTINGS_DEFAULTS } = await load("pi-coding-agent/core/settings-defaults.js");
const { DEFAULT_CODEMODE_INLINE_BUDGET } = await load("pi-coding-agent/extensions/codemode/tool.js");
emit({ mode: SETTINGS_DEFAULTS.codemode.mode, inlineBudget: SETTINGS_DEFAULTS.codemode.inlineBudget, toolBudget: DEFAULT_CODEMODE_INLINE_BUDGET });`, nil, &pi)
	if got := (Options{}).mode(); got != pi.Mode {
		t.Errorf("default mode %q, Pi %q", got, pi.Mode)
	}
	if ModeOn != pi.Mode {
		t.Errorf("ModeOn = %q, Pi default %q", ModeOn, pi.Mode)
	}
	if float64(DefaultInlineBudget) != pi.InlineBudget || pi.InlineBudget != pi.ToolBudget {
		t.Errorf("DefaultInlineBudget = %d, Pi SETTINGS_DEFAULTS %v and DEFAULT_CODEMODE_INLINE_BUDGET %v", DefaultInlineBudget, pi.InlineBudget, pi.ToolBudget)
	}
	if (Options{}).inlineBudget() != nil {
		t.Error("an Options without a budget or settings must leave the budget to DefaultInlineBudget")
	}
}
