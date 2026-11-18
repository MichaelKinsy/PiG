package codingagent

// pi: packages/coding-agent/src/modes/interactive/theme/theme-controller.ts

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// restoreStartupTheme restores the process-global theme state a startup
// prompt changes.
func restoreStartupTheme(t testing.TB) {
	t.Helper()
	previousRegistry := tui.ActiveThemeRegistry()
	previousTheme, previousCaps := tui.ActiveTheme(), tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetThemeRegistry(previousRegistry)
		tui.SetTerminalColors(tui.TerminalColors{})
		tui.SetTerminalColorScheme("")
		// Rebuild the previous theme in its own color mode, which can differ from the capabilities' (see pinHeaderTerminal).
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: previousTheme.GetColorMode() == tui.TerminalColorModeTrueColor})
		tui.SetThemeByName(previousTheme.Name)
		tui.SetCapabilities(previousCaps)
	})
}

// processThemeState is the process-wide state the tests of the package draw with: the active theme (its name, its color mode and two of its tokens), the terminal capabilities and the registered themes.
func processThemeState() string {
	theme := tui.ActiveTheme()
	return fmt.Sprintf("theme %s in %s (toolOutput %q, warning %q), capabilities %+v, registered themes %v", theme.Name, theme.GetColorMode(), theme.ToolOutput, theme.Warning, tui.GetCapabilities(), tui.ActiveThemeRegistry().Names())
}

// requireRestoresTheProcessTheme runs the test run as a subtest and fails unless it ends with the active theme, the terminal capabilities and the registered themes it began with, or a later test that draws with them depends on the order the tests run in (go test -shuffle). The test begins in the state a process on CI begins in: the dark theme built for truecolor, which the lazily created default theme is, under capabilities without truecolor.
func requireRestoresTheProcessTheme(t *testing.T, name string, run func(*testing.T)) {
	t.Helper()
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	tui.SetCapabilities(tui.TerminalCapabilities{})
	before := processThemeState()
	t.Run("test", run)
	if after := processThemeState(); after != before {
		t.Errorf("%s ended with %s; it began with %s; call restoreStartupTheme(t) first in it, or in the helper that selects its theme", name, after, before)
	}
}

// Each of these tests selects a theme, changes the terminal capabilities or registers themes: itself, through a helper it shares with others, or through a /reload, which applies the theme of the settings. Each has to end with what it began with.
//
// Not run a second time here, since each takes from one to twenty seconds of Pi's answers: the eighteen comparisons with Pi among the tests that left another state. Seven of them restore through runTreeContracts and runSessionContracts, which TestTreeListHandleInputContractsMatchPi and TestSessionListHandleInputContractsMatchPi hold here; the other eleven call restoreStartupTheme first, as the tests below do: TestExtensionErrorStackMatchesPi, TestFirstTimeSetupBindingOverlapsMatchPi, TestFirstTimeSetupMatchesPi, TestScopedModelsOverlappingBindingsMatchPi, TestScopedModelsSelectorMatchesPi, TestSessionSelectorInputMatchesPi, TestSettingsSelectorMatchesPi, TestSettingsSubmenusMatchPi, TestThinkingSelectorInputMatchesPi, TestTreeSelectorInputMatchesPi and TestTrustSelectorInputMatchesPi.
// Nor TestReloadEndsTheCountdownsOfTheOpenExtensionDialogs, which counts the countdown goroutines of the whole process: TestReloadDisposesEveryExtensionDialog holds newCustomRig, through which both restore.
func TestThemeTestsRestoreTheProcessTheme(t *testing.T) {
	for name, run := range themeRestoringTests {
		t.Run(name, func(t *testing.T) { requireRestoresTheProcessTheme(t, name, run) })
	}
}

// themeRestoringTests is the list above by name. A test whose file a strip build tag excludes is added from an init in a file of the same build
// constraint (startup_ui_theme_export_html_test.go, startup_ui_theme_mermaid_test.go), so a build that strips the code still runs every test it links.
var themeRestoringTests = map[string]func(*testing.T){
	"TestBuildChatViewportWiresLiveThemedScrollbar":                         TestBuildChatViewportWiresLiveThemedScrollbar,
	"TestBuiltInHeaderKeptLinesMatchAFirstDraw":                             TestBuiltInHeaderKeptLinesMatchAFirstDraw,
	"TestBuiltInHeaderRedrawsWhenAnInputChanges":                            TestBuiltInHeaderRedrawsWhenAnInputChanges,
	"TestCompactionBillingRenderProbe":                                      TestCompactionBillingRenderProbe,
	"TestDefaultResourceLoaderThemeColorModeUpstream":                       TestDefaultResourceLoaderThemeColorModeUpstream,
	"TestExtensionThemeLookupUsesPortablePaletteAndAbsence":                 TestExtensionThemeLookupUsesPortablePaletteAndAbsence,
	"TestExtensionUIResetClearsTheStatuses":                                 TestExtensionUIResetClearsTheStatuses,
	"TestExtensionUIResetForgetsTheEditorComponent":                         TestExtensionUIResetForgetsTheEditorComponent,
	"TestExtensionUIResetRemovesTheOverlays":                                TestExtensionUIResetRemovesTheOverlays,
	"TestExtensionUIResetRemovesTheTerminalInputListeners":                  TestExtensionUIResetRemovesTheTerminalInputListeners,
	"TestExtensionUIResetRestoresTheFooterAndTheHeader":                     TestExtensionUIResetRestoresTheFooterAndTheHeader,
	"TestExtensionUIResetRestoresTheWorkingStatus":                          TestExtensionUIResetRestoresTheWorkingStatus,
	"TestExtensionUIResetWritesTheSessionTitle":                             TestExtensionUIResetWritesTheSessionTitle,
	"TestFrontendFramesCarryTheThemeAcrossSwitches":                         TestFrontendFramesCarryTheThemeAcrossSwitches,
	"TestInteractiveManagedToolStatusContiguousGroupUpstream":               TestInteractiveManagedToolStatusContiguousGroupUpstream,
	"TestInteractiveStatusCoalescingUpstream":                               TestInteractiveStatusCoalescingUpstream,
	"TestInteractiveThemeWatcherAppliesOnOwnerLoop":                         TestInteractiveThemeWatcherAppliesOnOwnerLoop,
	"TestLoginHeaderRendererCachesDefensivelyAndTracksTheme":                TestLoginHeaderRendererCachesDefensivelyAndTracksTheme,
	"TestManagedToolRenderProbe":                                            TestManagedToolRenderProbe,
	"TestNewInteractiveModeAppliesTerminalCapabilitySettings":               TestNewInteractiveModeAppliesTerminalCapabilitySettings,
	"TestNewToolCardIsBuiltWithTheRegisteredDefinition":                     TestNewToolCardIsBuiltWithTheRegisteredDefinition,
	"TestNewVersionNotificationBytesMatchPi":                                TestNewVersionNotificationBytesMatchPi,
	"TestPackageUpdateNotificationBytesMatchPi":                             TestPackageUpdateNotificationBytesMatchPi,
	"TestRadiusLoginMenuRendersLikePi":                                      TestRadiusLoginMenuRendersLikePi,
	"TestReadCardKeepsTheTextOnceAndRenderersGetStructuredContent":          TestReadCardKeepsTheTextOnceAndRenderersGetStructuredContent,
	"TestReapplyToolPresentationDrawsCardsWithALaterResolution":             TestReapplyToolPresentationDrawsCardsWithALaterResolution,
	"TestReapplyToolPresentationReturnsACardWithoutRenderersToThePlainCard": TestReapplyToolPresentationReturnsACardWithoutRenderersToThePlainCard,
	"TestRegression10285RendersCallsToMCPToolsThatAreNotRegistered":         TestRegression10285RendersCallsToMCPToolsThatAreNotRegistered,
	"TestReloadClearsTheExtensionWidgets":                                   TestReloadClearsTheExtensionWidgets,
	"TestReloadDisposesEveryExtensionDialog":                                TestReloadDisposesEveryExtensionDialog,
	"TestReloadResourceResolutionPumpsOwner":                                TestReloadResourceResolutionPumpsOwner,
	"TestSelectStartupSessionCancelsAndJoinsBlockedLoaders":                 TestSelectStartupSessionCancelsAndJoinsBlockedLoaders,
	"TestSelectStartupSessionResolvesNullOnCancel":                          TestSelectStartupSessionResolvesNullOnCancel,
	"TestSelectStartupSessionResolvesTheSelectedPath":                       TestSelectStartupSessionResolvesTheSelectedPath,
	"TestSelectStartupSessionShowsNoRenameHint":                             TestSelectStartupSessionShowsNoRenameHint,
	"TestSelectStartupSessionUsesTheUsersKeybindings":                       TestSelectStartupSessionUsesTheUsersKeybindings,
	"TestSessionCommandBlockFollowsThemeChanges":                            TestSessionCommandBlockFollowsThemeChanges,
	"TestSessionListHandleInputContractsMatchPi":                            TestSessionListHandleInputContractsMatchPi,
	"TestSessionSelectorComponentHandleInputContractsMatchPi":               TestSessionSelectorComponentHandleInputContractsMatchPi,
	"TestSessionSelectorDeleteOutcomeReportsInTheHeader":                    TestSessionSelectorDeleteOutcomeReportsInTheHeader,
	"TestSessionSelectorKeybindingSourcesMatchPi":                           TestSessionSelectorKeybindingSourcesMatchPi,
	"TestSessionSelectorSharedKeysFollowRebinding":                          TestSessionSelectorSharedKeysFollowRebinding,
	"TestSessionSelectorStatusExpiryWakesIdleOwners":                        TestSessionSelectorStatusExpiryWakesIdleOwners,
	"TestSettingsSelectorInAContainerRedrawsAfterInput":                     TestSettingsSelectorInAContainerRedrawsAfterInput,
	"TestSettingsSelectorNeedsRedrawReportsTheListWithoutClearingIt":        TestSettingsSelectorNeedsRedrawReportsTheListWithoutClearingIt,
	"TestSettingsSelectorOmitsShareDestination":                             TestSettingsSelectorOmitsShareDestination,
	"TestSettingsSelectorReportsEveryCyclingRowThroughItsCallback":          TestSettingsSelectorReportsEveryCyclingRowThroughItsCallback,
	"TestSettingsSelectorUpstream":                                          TestSettingsSelectorUpstream,
	"TestStartupPromptAppliesTerminalCapabilitySettings":                    TestStartupPromptAppliesTerminalCapabilitySettings,
	"TestStartupPromptComponentsAreBuiltUnderTheCapabilityOverrides":        TestStartupPromptComponentsAreBuiltUnderTheCapabilityOverrides,
	"TestThinkingSelectorBoundariesMatchPi":                                 TestThinkingSelectorBoundariesMatchPi,
	"TestTreeContractReplayLeavesTheEmptyTreeSelfCancelOutOfTheTrace":       TestTreeContractReplayLeavesTheEmptyTreeSelfCancelOutOfTheTrace,
	"TestTreeLabelInputAndRoutingContractsMatchPi":                          TestTreeLabelInputAndRoutingContractsMatchPi,
	"TestTreeListHandleInputContractsMatchPi":                               TestTreeListHandleInputContractsMatchPi,
	"TestTreeListRemappedKeysMatchPi":                                       TestTreeListRemappedKeysMatchPi,
	"TestTreeSavedLabelCarriesTheSaveTimeAndRemovalClearsIt":                TestTreeSavedLabelCarriesTheSaveTimeAndRemovalClearsIt,
	"TestUpstreamExtendResourcesKeepPackageMetadata":                        TestUpstreamExtendResourcesKeepPackageMetadata,
}

// terminalColorQueryPrefix begins the OSC 10 request that starts a terminal color query.
const terminalColorQueryPrefix = "\x1b]10;?"

type fakeStartupTerminal struct {
	mu      sync.Mutex
	writes  []string
	onInput func([]byte)
	replies [][]byte
}

func (f *fakeStartupTerminal) StartWithReadError(onInput func([]byte), _ func(), _ func(error)) error {
	f.mu.Lock()
	f.onInput = onInput
	f.mu.Unlock()
	return nil
}

// send delivers input once the prompt has started reading.
func (f *fakeStartupTerminal) send(t *testing.T, data string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		onInput := f.onInput
		f.mu.Unlock()
		if onInput != nil {
			onInput([]byte(data))
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("prompt never started reading input")
		}
		time.Sleep(time.Millisecond)
	}
}

func (f *fakeStartupTerminal) written() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

func (f *fakeStartupTerminal) Stop() {}

func (f *fakeStartupTerminal) Write(data string) {
	f.mu.Lock()
	f.writes = append(f.writes, data)
	replies, onInput := f.replies, f.onInput
	f.replies = nil
	f.mu.Unlock()
	go func() {
		for _, reply := range replies {
			onInput(reply)
		}
	}()
}

// startupQueryWriter forwards the terminal color query, the only control write the renderer makes here, to the fake terminal so it can reply.
type startupQueryWriter struct{ terminal *fakeStartupTerminal }

func (w startupQueryWriter) Write(data []byte) (int, error) {
	if bytes.HasPrefix(data, []byte(terminalColorQueryPrefix)) {
		w.terminal.Write(string(data))
	}
	return len(data), nil
}

// whiteTerminalReplies answers a color query for a black-on-white terminal without a palette; the DA1 reply ends the query.
func whiteTerminalReplies() [][]byte {
	return [][]byte{
		[]byte("\x1b]10;rgb:0000/0000/0000\x07"),
		[]byte("\x1b]11;rgb:ffff/ffff/ffff\x07"),
		[]byte("\x1b[?62c"),
	}
}

// upstream 0.99.1 startup-ui.ts startStartupTui and theme-controller.ts requestTerminalColors: the prompt renders at once, the replies record the terminal's colors, and none reaches the prompt as input.
func TestStartupPromptAppliesTerminalColorsAndKeepsRepliesOutOfInput(t *testing.T) {
	restoreTUIKeybindings(t)
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	configureStartupTui(StartupUIOptions{})
	terminal := &fakeStartupTerminal{replies: append(whiteTerminalReplies(), []byte("h"), []byte("i"), []byte("\r"))}
	input := tui.NewExtensionInputComponent("Name", "", nil, nil)
	completed, err := runStartupComponentWith(input, StartupUIOptions{Settings: Settings{}}, false, tui.NewWithOutput(startupQueryWriter{terminal}, 80, 24), terminal)
	if err != nil || !completed {
		t.Fatalf("completed=%v err=%v", completed, err)
	}
	if got := input.Text(); got != "hi" {
		t.Fatalf("input = %q; terminal replies leaked into the prompt", got)
	}
	if got := tui.ActiveTheme().Name; got != tui.SystemThemeName {
		t.Fatalf("theme = %q; want the system theme", got)
	}
	if got := tui.GetTerminalTheme(); got != "light" {
		t.Fatalf("terminal theme = %q; want the light reported background", got)
	}
	if writes := terminal.written(); len(writes) != 1 || writes[0][:len(terminalColorQueryPrefix)] != terminalColorQueryPrefix {
		t.Fatalf("writes = %q; want one color query", writes)
	}
}

// upstream 0.99.1 theme-controller.ts requestTerminalColors passes onLateReply: colors that arrive after the timeout still apply.
func TestStartupPromptAppliesColorsThatArriveAfterTheTimeout(t *testing.T) {
	restoreTUIKeybindings(t)
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	configureStartupTui(StartupUIOptions{})
	selector := tui.NewExtensionSelectorComponent("Pick", []string{"a", "b"}, nil, nil)
	terminal := &fakeStartupTerminal{}
	done := make(chan error, 1)
	ui := tui.NewWithOutput(startupQueryWriter{terminal}, 80, 24)
	go func() {
		_, err := runStartupComponentWith(selector, StartupUIOptions{}, false, ui, terminal)
		done <- err
	}()
	time.Sleep(3 * terminalColorQueryTimeout)
	if got := tui.GetTerminalTheme(); got == "light" {
		t.Fatal("the terminal theme was light before any reply arrived")
	}
	for _, reply := range whiteTerminalReplies() {
		terminal.send(t, string(reply))
	}
	terminal.send(t, "\r")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := tui.GetTerminalTheme(); got != "light" {
		t.Fatalf("terminal theme = %q; want the late light background", got)
	}
	if selector.SelectedIndex() != 0 {
		t.Fatalf("selected %d", selector.SelectedIndex())
	}
}

// upstream 0.99.1 startup-ui.ts startStartupTui queries the terminal whatever the setting is, and re-resolves the setting with the reported appearance.
func TestStartupPromptWithFixedThemeQueriesAndKeepsTheTheme(t *testing.T) {
	restoreTUIKeybindings(t)
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	settings := Settings{Theme: "dark"}
	configureStartupTui(StartupUIOptions{Settings: settings})
	terminal := &fakeStartupTerminal{replies: append(whiteTerminalReplies(), []byte("\r"))}
	selector := tui.NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil)
	if _, err := runStartupComponentWith(selector, StartupUIOptions{Settings: settings}, false, tui.NewWithOutput(startupQueryWriter{terminal}, 80, 24), terminal); err != nil {
		t.Fatal(err)
	}
	if got := tui.ActiveTheme().Name; got != "dark" {
		t.Fatalf("theme = %q; a fixed theme does not follow the terminal", got)
	}
	if writes := terminal.written(); len(writes) != 1 {
		t.Fatalf("writes = %q; want one color query", writes)
	}
}

func TestStartupPromptFlushesALoneEscape(t *testing.T) {
	restoreStartupTheme(t)
	terminal := &fakeStartupTerminal{}
	selector := tui.NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil)
	done := make(chan error, 1)
	go func() {
		_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, tui.NewWithOutput(io.Discard, 80, 24), terminal)
		done <- err
	}()
	terminal.send(t, "\x1b")
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a lone Escape was not flushed to the prompt")
	}
	if !selector.Cancelled() {
		t.Fatal("Escape did not cancel the selector")
	}
}

// pinTrueColorCapabilities fixes the terminal capabilities at truecolor for a test that compares theme instances by
// identity. storeActiveTheme converts a theme into the capabilities' color mode and returns a copy when the mode
// differs, so on a 256-color host (TERM=screen) an instance built in truecolor is not the active theme itself. Register
// it before restoreStartupTheme so its cleanup runs after that helper's.
func pinTrueColorCapabilities(t testing.TB) {
	t.Helper()
	previous := tui.GetCapabilities()
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	t.Cleanup(func() { tui.SetCapabilities(previous) })
}
