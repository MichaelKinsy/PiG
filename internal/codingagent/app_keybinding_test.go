package codingagent

import (
	"os"
	"regexp"
	"slices"
	"testing"
)

// AppKeybinding is `keyof AppKeybindings` (keybindings.ts:14-60): exactly the app.* ids of the keybinding table, no more and no fewer.
func TestAppKeybindingConstantsAreTheAppKeybindingTable(t *testing.T) {
	constants := []AppKeybinding{
		AppInterrupt, AppClear, AppExit, AppSuspend, AppThinkingCycle, AppThinkingSave, AppModelCycleForward, AppModelCycleBackward,
		AppModelSelect, AppToolsExpand, AppThinkingToggle, AppSessionToggleNamedFilter, AppEditorExternal, AppMessageCopy, AppMessageFollowUp,
		AppMessageDequeue, AppClipboardPasteImage, AppSessionNew, AppSessionTree, AppSessionFork, AppSessionResume, AppTreeFoldOrUp,
		AppTreeUnfoldOrDown, AppTreeEditLabel, AppTreeToggleLabelTimestamp, AppSessionTogglePath, AppSessionToggleSort, AppSessionRename,
		AppSessionDelete, AppSessionDeleteNoninvasive, AppModelsSave, AppModelsEnableAll, AppModelsClearAll, AppModelsToggleProvider,
		AppModelsReorderUp, AppModelsReorderDown, AppTreeFilterDefault, AppTreeFilterNoTools, AppTreeFilterUserOnly, AppTreeFilterLabeledOnly,
		AppTreeFilterAll, AppTreeFilterCycleForward, AppTreeFilterCycleBackward,
	}
	var got []string
	for _, c := range constants {
		got = append(got, string(c))
	}
	var want []string
	for id := range appKeybindingDefinitions {
		want = append(want, id)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("AppKeybinding constants differ from the app.* keybinding table\n got  %v\n want %v", got, want)
	}
}

// Pi keybindings.ts:14-60 AppKeybinding = keyof AppKeybindings: the variants are the "app.*" keys of the keybinding table, in source order.
func TestAppKeybindingVariantsMatchPiAppKeybindings(t *testing.T) {
	raw, err := os.ReadFile("../../.upstream/current/packages/coding-agent/src/core/keybindings.ts")
	if err != nil {
		t.Fatal(err)
	}
	var want []AppKeybinding
	for _, m := range regexp.MustCompile(`(?m)^\t"(app\.[\w.]+)": \{`).FindAllSubmatch(raw, -1) {
		want = append(want, AppKeybinding(m[1]))
	}
	if len(want) == 0 || !slices.Equal(appKeybindingIDs, want) {
		t.Fatalf("AppKeybinding variants\n got  %v\n Pi   %v", appKeybindingIDs, want)
	}
}
