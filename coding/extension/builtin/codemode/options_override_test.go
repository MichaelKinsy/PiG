package codemode

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func overrideLoadout() extension.ToolLoadout {
	echo := extension.AgentTool{Name: "echo", Description: "Echo the input"}
	direct := func(string) extension.ToolExposure { return extension.ToolExposureDirect }
	return extension.ToolLoadout{
		Declared:            []extension.AgentTool{echo},
		Callable:            []extension.AgentTool{echo},
		Registered:          []extension.AgentTool{echo},
		GetExposure:         direct,
		GetNamespace:        func(string) *extension.ToolNamespace { return nil },
		GetPromptGuidelines: func(string) []string { return nil },
	}
}

func settings(codemode map[string]any) func() extension.Settings {
	return func() extension.Settings { return extension.Settings{"codemode": codemode} }
}

// index.ts createCodemodeExtension: `getMode: () => options.mode ?? readMode(pi)` and
// `getInlineBudget: () => options.inlineBudget ?? readInlineBudget(pi)`: an option overrides the codemode.mode and
// codemode.inlineBudget settings, and an unset option reads them on every use.
func TestExtensionOptionsOverrideTheCodemodeSettings(t *testing.T) {
	hidden := func(options Options) []string { return prepareLoadout(overrideLoadout(), options).HiddenDeclarations }
	scriptCall := func(options Options) bool {
		return strings.Contains(prepareLoadout(overrideLoadout(), options).Descriptions["echo"], "Codemode: `tools.echo")
	}

	// Mode "only" hides the declarations of active direct tools and leaves their descriptions alone.
	if got := hidden(Options{Mode: ModeOnly}); len(got) != 1 || got[0] != "echo" {
		t.Errorf("Mode only: hidden = %v, want [echo]", got)
	}
	if scriptCall(Options{Mode: ModeOnly}) {
		t.Error("Mode only: the declared tool's description names its script call")
	}
	// The option wins over the setting in both directions.
	if got := hidden(Options{Mode: ModeOn, GetSettings: settings(map[string]any{"mode": "only"})}); len(got) != 0 || !scriptCall(Options{Mode: ModeOn, GetSettings: settings(map[string]any{"mode": "only"})}) {
		t.Errorf("Mode on over a setting of only: hidden = %v", got)
	}
	if got := hidden(Options{GetSettings: settings(map[string]any{"mode": "only"})}); len(got) != 1 {
		t.Errorf("setting only without an option: hidden = %v, want [echo]", got)
	}

	// An InlineBudget of zero lists no tool section, whatever the setting says; an unset one reads the setting.
	zero, large := 0, 100000
	description := func(options Options) string { return prepareLoadout(overrideLoadout(), options).Descriptions[ToolName] }
	// Direct tools are not listed in mode on, so list them with mode only.
	if got := description(Options{Mode: ModeOnly, InlineBudget: &zero, GetSettings: settings(map[string]any{"inlineBudget": float64(100000)})}); strings.Contains(got, "### `echo`") {
		t.Errorf("InlineBudget 0 over a setting of 100000 still lists echo:\n%s", got)
	}
	if got := description(Options{Mode: ModeOnly, InlineBudget: &large, GetSettings: settings(map[string]any{"inlineBudget": float64(0)})}); !strings.Contains(got, "### `echo`") {
		t.Errorf("InlineBudget 100000 over a setting of 0 does not list echo:\n%s", got)
	}
	if got := description(Options{Mode: ModeOnly, GetSettings: settings(map[string]any{"inlineBudget": float64(0)})}); strings.Contains(got, "### `echo`") {
		t.Errorf("setting 0 without an option still lists echo:\n%s", got)
	}
}

// readInlineBudget accepts any finite number >= 0: a budget above the int range stays a large budget.
func TestInlineBudgetSettingAboveTheIntRangeStaysLarge(t *testing.T) {
	got := Options{GetSettings: settings(map[string]any{"inlineBudget": 1e300})}.inlineBudget()
	if got == nil || *got < 1_000_000 {
		t.Fatalf("inlineBudget = %v, want a large budget", got)
	}
}
