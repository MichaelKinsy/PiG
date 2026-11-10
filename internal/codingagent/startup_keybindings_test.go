package codingagent

// pi: packages/coding-agent/src/cli/startup-ui.ts

import (
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi's createStartupTui installs setKeybindings(KeybindingsManager.create()) for every startup prompt (startup-ui.ts:91), so the
// prompt answers to the keybindings in the agent directory and to nothing a caller installed before. PiG's prompts took whatever the
// process-wide keybindings held: a tui.select.confirm remapped earlier in the process left a prompt that Enter could not confirm,
// and it waited for a key that never came.
func TestStartupPromptsDoNotInheritKeybindingsInstalledBefore(t *testing.T) {
	restoreTUIKeybindings(t)
	restoreStartupTheme(t)
	NewKeybindingsManagerFromBindings(map[string][]KeyID{tui.KBSelectConfirm: {"ctrl+t"}}, "")
	opts := StartupUIOptions{AgentDir: t.TempDir()}

	selector := newStartupSelector("Pick", []string{"a", "b"}, opts)
	selector.HandleInput("\r")
	if !selector.Done() || selector.Cancelled() {
		t.Fatalf("selector: done=%v cancelled=%v after Enter; a stale tui.select.confirm binding reached the startup prompt", selector.Done(), selector.Cancelled())
	}

	NewKeybindingsManagerFromBindings(map[string][]KeyID{tui.KBSelectConfirm: {"ctrl+t"}}, "")
	input := newStartupInput("Name", "", opts)
	input.HandleInput("\r")
	if !input.Done() || input.Cancelled() {
		t.Fatalf("input: done=%v cancelled=%v after Enter; a stale tui.select.confirm binding reached the startup prompt", input.Done(), input.Cancelled())
	}
}

// startup-ui.ts:91 loads keybindings.json of the agent directory: a user's remap of tui.select.confirm applies to the startup prompts.
func TestStartupPromptsApplyTheKeybindingsOfTheAgentDirectory(t *testing.T) {
	restoreTUIKeybindings(t)
	restoreStartupTheme(t)
	agentDir := t.TempDir()
	if err := os.WriteFile(KeybindingsFile(agentDir), []byte(`{"tui.select.confirm": "ctrl+y"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := StartupUIOptions{AgentDir: agentDir}

	selector := newStartupSelector("Pick", []string{"a", "b"}, opts)
	selector.HandleInput("\r")
	if selector.Done() {
		t.Fatal("selector: Enter confirmed although keybindings.json remaps tui.select.confirm to ctrl+y")
	}
	selector.HandleInput("\x19")
	if !selector.Done() || selector.Cancelled() {
		t.Fatalf("selector: done=%v cancelled=%v after ctrl+y", selector.Done(), selector.Cancelled())
	}

	input := newStartupInput("Name", "", opts)
	input.HandleInput("\r")
	if input.Done() {
		t.Fatal("input: Enter confirmed although keybindings.json remaps tui.select.confirm to ctrl+y")
	}
	input.HandleInput("\x19")
	if !input.Done() || input.Cancelled() {
		t.Fatalf("input: done=%v cancelled=%v after ctrl+y", input.Done(), input.Cancelled())
	}
}

// configureStartupTui installs the process-wide keybindings of the agent directory, so every test that opens a startup prompt has to restore them, or a later test that reads them depends on the order the tests run in (go test -shuffle).
func TestStartupPromptTestsRestoreTheProcessKeybindings(t *testing.T) {
	for name, run := range map[string]func(*testing.T){
		"TestStartupPromptAppliesTerminalColorsAndKeepsRepliesOutOfInput": TestStartupPromptAppliesTerminalColorsAndKeepsRepliesOutOfInput,
		"TestStartupPromptAppliesColorsThatArriveAfterTheTimeout":         TestStartupPromptAppliesColorsThatArriveAfterTheTimeout,
		"TestStartupPromptWithFixedThemeQueriesAndKeepsTheTheme":          TestStartupPromptWithFixedThemeQueriesAndKeepsTheTheme,
		"TestStartupPromptExplicitEmptyThemeFallsBackToSystem":            TestStartupPromptExplicitEmptyThemeFallsBackToSystem,
		"TestStartupPromptMalformedThemeUsesSystemTheme":                  TestStartupPromptMalformedThemeUsesSystemTheme,
		"TestStartupPromptComponentsAreBuiltUnderThePendingSystemTheme":   TestStartupPromptComponentsAreBuiltUnderThePendingSystemTheme,
		"TestStartupPromptComponentsAreBuiltUnderTheCapabilityOverrides":  TestStartupPromptComponentsAreBuiltUnderTheCapabilityOverrides,
		"TestShowFirstTimeSetupSavesTheChoiceBeforeTheTUI":                TestShowFirstTimeSetupSavesTheChoiceBeforeTheTUI,
		"TestShowFirstTimeSetupSkipSavesNothing":                          TestShowFirstTimeSetupSkipSavesNothing,
		"TestFirstTimeSetupCreateYourOwnShowsTheHintAndKeepsTheSprite":    TestFirstTimeSetupCreateYourOwnShowsTheHintAndKeepsTheSprite,
		"TestShowFirstTimeSetupReportsNoSettingsWriteFailure":             TestShowFirstTimeSetupReportsNoSettingsWriteFailure,
	} {
		t.Run(name, func(t *testing.T) {
			restoreTUIKeybindings(t)
			marker := tui.NewTUIKeybindingsManager(nil)
			tui.SetTUIKeybindings(marker)
			t.Run("test", run)
			if tui.GetTUIKeybindings() != marker {
				t.Fatalf("%s left the process-wide TUI keybindings it installed; call restoreTUIKeybindings(t) in it", name)
			}
		})
	}
}
