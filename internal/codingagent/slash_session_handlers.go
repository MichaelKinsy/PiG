// Slash command handlers: text-only versions.
//
// /name <text>  : append a SessionInfoEntry so the picker shows it.
// /fork <id>    : set leaf to the given entry id; next message is a sibling.
// /clone        : snapshot path-to-leaf as a new session file.
// /resume       : list recent sessions (re-launch with --session <id>).
// /tree         : render the branch tree as ASCII.
//
// When modal selector callbacks are absent, /resume lists sessions,
// /fork requires an explicit entry ID, and /tree prints the tree.

package codingagent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	jsjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui"
)

// jsonStringifyString quotes text as JSON.stringify does: control characters escape, and U+2028, U+2029 and HTML characters stay literal.
func jsonStringifyString(text string) string {
	//portlint:allow jsonescape Canonicalize rewrites the escaped text in JSON.stringify form
	encoded, err := jsjson.Marshal(text)
	if err == nil {
		encoded, err = jsonstringify.Canonicalize(encoded)
	}
	if err != nil {
		return strconv.Quote(text)
	}
	return string(encoded)
}

func nameHandler(sc *SlashContext) error {
	if sc.CurrentSession == nil {
		sc.Append("Session info unavailable in this build.")
		return nil
	}
	args := strings.TrimSpace(sc.Args)
	if args == "" {
		if sc.GetSessionName != nil {
			if cur := sc.GetSessionName(); cur != "" {
				appendNameText(sc, "Session name: "+cur)
				return nil
			}
		}
		showWarningOrAppend(sc, "Usage: /name <name>")
		return nil
	}
	if sc.SetSessionName == nil {
		sc.Append("Session naming unavailable in this build.")
		return nil
	}
	if err := sc.SetSessionName(args); err != nil {
		return err
	}
	// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:handleNameCommand
	// The session manager stores the name with line breaks folded to spaces, so the stored name can differ from the argument.
	name := args
	if sc.GetSessionName != nil {
		if stored := sc.GetSessionName(); stored != "" {
			name = stored
		}
	}
	if name != args {
		showWarningOrAppend(sc, fmt.Sprintf("Session name was normalized from %s to %s", jsonStringifyString(args), jsonStringifyString(name)))
	}
	appendNameText(sc, "Session name set: "+name)
	// Update terminal title + status-line footer.
	if sc.OnNameChange != nil {
		sc.OnNameChange(name)
	}
	return nil
}

// appendNameText preserves literal names with the theme's dim foreground.
func appendNameText(sc *SlashContext, text string) {
	text = tui.ActiveTheme().Fg("dim", text)
	if sc.AppendText != nil {
		sc.AppendText(text)
	} else {
		sc.Append(text)
	}
}

// debugHandler backs /debug and the ctrl+shift+d hotkey: it writes a debug
// log (rendered frame + message JSONL) and confirms with the path. Mirrors
// upstream handleDebugCommand (interactive-mode.ts:5580).
func debugHandler(sc *SlashContext) error {
	if sc.WriteDebugLog == nil {
		sc.Append("Debug unavailable in this build.")
		return nil
	}
	path, err := sc.WriteDebugLog()
	if err != nil {
		return err
	}
	th := tui.ActiveTheme()
	confirmation := th.Fg("accent", "✓ Debug log written") + "\n" + th.Fg("muted", path)
	if sc.AppendBlock == nil {
		sc.Append(confirmation)
		return nil
	}
	sc.AppendBlock(confirmation)
	return nil
}

// forkHandler implements /fork. A session_before_fork cancel ends it without
// output, as upstream showUserMessageSelector does.
func forkHandler(sc *SlashContext) error {
	if sc.ForkToNewSession == nil {
		sc.Append("Fork unavailable in this build.")
		return nil
	}
	id := strings.TrimSpace(sc.Args)
	// Bare /fork: prefer interactive picker if available; otherwise
	// fall back to usage hint.
	if id == "" {
		if sc.PickUserMessage != nil {
			picked, ok := sc.PickUserMessage()
			if !ok {
				return nil
			}
			id = picked
		} else {
			sc.Append("Usage: /fork <entry-id>")
			sc.Append("Run /tree to see entry ids.")
			return nil
		}
	}
	if err := sc.ForkToNewSession(id); err != nil {
		if errors.Is(err, errSessionReplacementCancelled) {
			return nil
		}
		return err
	}
	// Mirrors upstream showStatus("Forked to new session")
	// (interactive-mode.ts:4405).
	showStatusOrAppend(sc, "Forked to new session")
	return nil
}

// cloneHandler implements /clone. A session_before_fork cancel ends it without
// output, as upstream handleCloneCommand does.
func cloneHandler(sc *SlashContext) error {
	if sc.CloneCurrent == nil {
		sc.Append("Clone unavailable in this build.")
		return nil
	}
	if sc.CurrentSession != nil && sc.CurrentSession() != nil && sc.CurrentSession().GetLeafID() == nil {
		showStatusOrAppend(sc, "Nothing to clone yet")
		return nil
	}
	_, err := sc.CloneCurrent()
	if errors.Is(err, errSessionReplacementCancelled) {
		return nil
	}
	if err != nil {
		return err
	}
	if sc.SetEditorText != nil {
		sc.SetEditorText("")
	}
	showStatusOrAppend(sc, "Cloned to new session")
	return nil
}

// resumeHandler implements /resume. With the interactive picker, a resumed
// session reports "Resumed session", while a cancelled picker or a
// session_before_switch cancel reports nothing, as upstream
// handleResumeSession and the selector's onCancel do.
func resumeHandler(sc *SlashContext) error {
	// Prefer interactive picker; fall back to text listing.
	if sc.PickSession != nil && sc.LoadSessionPath != nil {
		path, ok := sc.PickSession()
		if !ok {
			return nil
		}
		if err := sc.LoadSessionPath(path); errors.Is(err, errSessionReplacementCancelled) {
			return nil
		} else if err != nil {
			if sc.FatalRuntimeError != nil {
				return sc.FatalRuntimeError("Failed to resume session", err)
			}
			return err
		}
		status := statusResumed
		if sc.ResumeStatus != nil {
			status = sc.ResumeStatus()
		}
		showStatusOrAppend(sc, status)
		return nil
	}
	if sc.ListSessions == nil {
		sc.Append("Session listing unavailable in this build.")
		return nil
	}
	infos, err := sc.ListSessions()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		sc.Append("No sessions found in this project's session directory.")
		return nil
	}
	var b strings.Builder
	b.WriteString("**Recent sessions in this directory** (relaunch with `--session <id>` or `--continue` to pick the latest):\n\n")
	for i, info := range infos {
		if i >= 20 {
			fmt.Fprintf(&b, "\n  …and %d more.\n", len(infos)-20)
			break
		}
		name := info.Name
		if name == "" && info.FirstMessage != noMessagesText {
			name = truncate(info.FirstMessage, 200)
		}
		if name == "" {
			name = "(no name)"
		}
		fmt.Fprintf(&b, "  - `%s`  · %d msg · %s · %s\n", info.ID, info.MessageCount, info.Modified.Format("2006-01-02 15:04"), name)
	}
	sc.Append(b.String())
	return nil
}

func treeHandler(sc *SlashContext) error {
	return treeHandlerWithInitial(sc, "")
}

// trustHandler shows current and saved trust decisions, persists the selection before closing and leaves activation to the next startup.
func trustHandler(sc *SlashContext) error {
	if sc.ShowTrustSelector == nil {
		sc.Append("Project trust selector unavailable.")
		return nil
	}
	var cwd string
	if sc.CurrentSession != nil {
		if session := sc.CurrentSession(); session != nil {
			cwd = session.CWD()
		}
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	store := NewProjectTrustStore(sc.AgentDir)
	saved, err := store.GetEntry(cwd)
	if err != nil {
		return err
	}
	trusted := sc.SettingsManager != nil && sc.SettingsManager.IsProjectTrusted()
	var saveErr error
	selection, ok := sc.ShowTrustSelector(TrustSelectorOptions{
		Cwd: cwd, SavedDecision: saved, ProjectTrusted: trusted,
		OnSelect: func(selection TrustSelection) { saveErr = store.SetMany(selection.Updates) },
	})
	if saveErr != nil {
		return saveErr
	}
	if !ok {
		return nil
	}
	state := "untrusted"
	if selection.Trusted {
		state = "trusted"
	}
	showStatusOrAppend(sc, "Saved trust decision: "+state+". Restart "+AppName+" for this to take effect.")
	return nil
}

// treeHandlerWithInitial is the full /tree handler, supporting an optional
// initialSelectedID for the re-open-at-same-row recursion in the summarize flow.
// Mirrors upstream showTreeSelector(initialSelectedId?) (interactive-mode.ts:4065).
func treeHandlerWithInitial(sc *SlashContext, initialSelectedID string) error {
	// Bare /tree: prefer interactive picker if available so the user
	// can scroll through and (eventually) hit Enter to navigate there.
	// Mirrors upstream `showTreeSelector`
	// (.upstream/current/packages/coding-agent/src/modes/interactive/
	// interactive-mode.ts:4065-4194).
	if sc.PickTreeEntry == nil {
		if sc.RenderTree == nil {
			sc.Append("Tree rendering unavailable in this build.")
			return nil
		}
		txt := sc.RenderTree()
		if txt == "" {
			sc.Append("(empty tree)")
			return nil
		}
		sc.Append("**Session tree**\n\n```\n" + txt + "\n```")
		return nil
	}

	// Empty-tree pre-check (interactive-mode.ts:4068-4072): upstream
	// flashes "No entries in session" instead of opening an empty
	// overlay.
	if sc.CurrentSession != nil {
		if s := sc.CurrentSession(); s != nil && len(s.GetEntries()) == 0 {
			showStatusOrAppend(sc, "No entries in session")
			return nil
		}
	}

	_ = initialSelectedID // passed to PickTreeEntry below for initial cursor position

	id, ok := sc.PickTreeEntry(initialSelectedID)
	if !ok {
		// Upstream cancel path (`onCancel`) is silent.
		return nil
	}

	// Current-leaf no-op check.
	if sc.CurrentSession != nil {
		if s := sc.CurrentSession(); s != nil {
			if leaf := s.GetLeafID(); leaf != nil && *leaf == id {
				showStatusOrAppend(sc, "Already at this point")
				return nil
			}
		}
	}

	// If NavigateTreeFull is wired, use the full summarize flow.
	// Mirrors upstream interactive-mode.ts:4090-4194 (post-selection handler).
	if sc.NavigateTreeFull != nil && sc.ShowExtensionSelector != nil {
		return treeNavigateWithSummarize(sc, id)
	}

	// Fallback: simple fork (no summarize dialog).
	if sc.ForkAtEntry != nil {
		if err := sc.ForkAtEntry(id); err != nil {
			if errors.Is(err, errSessionReplacementCancelled) {
				return nil
			}
			return err
		}
		showStatusOrAppend(sc, "Navigated to selected point")
		flushCompactionQueue(sc)
		return nil
	}
	sc.Append(fmt.Sprintf("Selected entry: %s", id))
	return nil
}

// treeNavigateWithSummarize implements the full 3-option "Summarize branch?"
// selection loop and navigates to the target.
// Mirrors upstream interactive-mode.ts:5570-5680.
func treeNavigateWithSummarize(sc *SlashContext, entryID string) error {
	const (
		optNoSummary       = "No summary"
		optSummarize       = "Summarize"
		optCustomSummarize = "Summarize with custom prompt"
	)

	wantsSummary := false
	customInstructions := ""

	// The summarize prompt is skipped when branchSummary.skipPrompt is set (interactive-mode.ts:5590); navigation then continues with
	// no summary through the same result handling as a chosen "No summary".
	skipPrompt := sc.SettingsManager != nil && sc.SettingsManager.GetBranchSummarySettings().SkipPrompt

	// Loop until user makes a complete choice or cancels (Esc) to re-open tree.
	// Mirrors upstream while(true) loop (interactive-mode.ts:5591-5615).
	for !skipPrompt {
		choice, ok := sc.ShowExtensionSelector("Summarize branch?", []string{
			optNoSummary, optSummarize, optCustomSummarize,
		}, "")
		if !ok {
			// Esc from selector: re-open tree at same row.
			// Mirrors upstream interactive-mode.ts:5601.
			return treeHandlerWithInitial(sc, entryID)
		}
		wantsSummary = choice != optNoSummary

		if choice == optCustomSummarize {
			instructions, ok := sc.ShowExtensionEditor("Custom summarization instructions", "", "")
			if !ok {
				// Esc from editor: loop back to selector.
				// Mirrors upstream interactive-mode.ts:5609.
				continue
			}
			customInstructions = instructions
		}
		break
	}

	result, err := sc.NavigateTreeFull(context.Background(), entryID, wantsSummary, customInstructions)
	if err != nil {
		return err
	}

	if result.Aborted {
		// Summarization was aborted: re-open tree at same row.
		// Mirrors upstream interactive-mode.ts:4148-4153.
		showStatusOrAppend(sc, "Branch summarization cancelled")
		return treeHandlerWithInitial(sc, entryID)
	}
	if result.Cancelled {
		// Navigation cancelled (e.g. extension hook cancelled it).
		showStatusOrAppend(sc, "Navigation cancelled")
		return nil
	}

	// Success: set editor text if the navigation target was a user message.
	if result.EditorText != "" && sc.SetEditorText != nil {
		sc.SetEditorText(result.EditorText)
	}
	showStatusOrAppend(sc, "Navigated to selected point")
	flushCompactionQueue(sc)
	return nil
}

// flushCompactionQueue delivers any messages queued during a compaction once
// navigation has settled on a new leaf.
func flushCompactionQueue(sc *SlashContext) {
	if sc.FlushCompactionQueue != nil {
		sc.FlushCompactionQueue()
	}
}

// renderTreeASCII walks a SessionTreeNode and produces an indented
// textual representation. Each node is one line; columns: id (8 chars),
// timestamp HH:MM:SS, role, first 60 chars of text. Branch glyphs
// (`├─`, `└─`, `│`) follow the standard tree style.
func renderTreeASCII(root *SessionTreeNode) string {
	if root == nil || len(root.Children) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range root.Children {
		last := i == len(root.Children)-1
		writeTreeNode(&b, c, "", last)
	}
	return b.String()
}

func writeTreeNode(b *strings.Builder, n *SessionTreeNode, prefix string, last bool) {
	connector := "├─ "
	childPrefix := prefix + "│  "
	if last {
		connector = "└─ "
		childPrefix = prefix + "   "
	}
	id := n.Entry.Base().ID
	if len(id) > 8 {
		id = id[:8]
	}
	role := "?"
	text := ""
	if me, ok := n.Entry.(MessageEntry); ok {
		role = me.Message.Role()
		text = extractMessageText(me)
	} else if n.Entry.Base().Type != "message" {
		role = n.Entry.Base().Type
	}
	if len(text) > 60 {
		text = text[:60] + "…"
	}
	text = strings.ReplaceAll(text, "\n", " ")
	ts := n.Entry.Base().Timestamp
	if len(ts) >= 19 {
		ts = ts[11:19]
	}
	label := ""
	if n.Label != "" {
		label = " [" + n.Label + "]"
	}
	fmt.Fprintf(b, "%s%s%s · %s · %s%s · %s\n", prefix, connector, id, ts, role, label, text)
	for i, c := range n.Children {
		isLast := i == len(n.Children)-1
		writeTreeNode(b, c, childPrefix, isLast)
	}
}

// showStatusOrAppend mirrors upstream showStatus and falls back to chat
// append in headless / test contexts where the interactive status sink is
// unwired.
func showStatusOrAppend(sc *SlashContext, msg string) {
	if sc.ShowStatus != nil {
		sc.ShowStatus(msg)
		return
	}
	sc.Append(msg)
}

func showWarningOrAppend(sc *SlashContext, msg string) {
	if sc.ShowWarning != nil {
		sc.ShowWarning(msg)
		return
	}
	sc.Append("Warning: " + msg)
}

// changelogHandler shows the full inline changelog, independently of the startup collapse setting.
func changelogHandler(sc *SlashContext) error {
	if sc.ShowChangelog != nil {
		sc.ShowChangelog()
		return nil
	}
	entries := ParseChangelog(bundledChangelog())
	sc.Append(FormatChangelogForChat(entries))
	return nil
}

// compactHandler delegates every /compact request to the Session, including empty or small Sessions. Completion and errors arrive through compaction events.
func compactHandler(sc *SlashContext) error {
	if sc.CompactSession == nil {
		sc.Append("Compaction unavailable in this build.")
		return nil
	}

	// Extract optional custom instructions from args (text after "/compact ").
	// Mirrors upstream /compact <customInstructions> handling.
	customInstructions := strings.TrimSpace(sc.Args)

	// Fire-and-forget: errors surface via CompactionEndEvent{ErrorMessage}.
	// Mirrors upstream compact() call which is not awaited at the /compact
	// dispatch site (interactive-mode.ts).
	_ = sc.CompactSession(customInstructions) // errors intentionally ignored: surfaced via events
	return nil
}

// ─── /settings handler ───────────────────────────────────────────

// settingStripped reports whether the /settings row id configures a built-in
// this process strips: skills, Mermaid, the changelog (whose detected updates
// also send the install telemetry ping) and /tree; the double-escape row goes
// when only "none" is left.
// pig additive (D92): the row of a stripped built-in is not offered.
func settingStripped(id string) bool {
	switch id {
	case "skill-commands":
		return pigstrip.Has(pigstrip.ListFeatures, pigstrip.Skills)
	case "mermaid-rendering":
		return pigstrip.Has(pigstrip.ListFeatures, pigstrip.Mermaid)
	case "collapse-changelog", "install-telemetry":
		return pigstrip.Has(pigstrip.ListFeatures, pigstrip.Changelog)
	case "double-escape-action":
		return len(doubleEscapeActions()) <= 1
	case "tree-filter-mode":
		return pigstrip.Has(pigstrip.ListCommands, "/tree")
	}
	return false
}

// doubleEscapeActions are the double-escape actions whose command the process
// has, then "none".
// pig additive (D92): a stripped /tree or /fork is not offered.
func doubleEscapeActions() []string {
	return slices.DeleteFunc([]string{"tree", "fork", "none"}, func(action string) bool {
		return action != "none" && pigstrip.Has(pigstrip.ListCommands, "/"+action)
	})
}

type httpIdleTimeoutChoice struct {
	label     string
	timeoutMs int
}

// upstream: coding-agent/src/core/http-dispatcher.ts:HTTP_IDLE_TIMEOUT_CHOICES
var httpIdleTimeoutChoices = []httpIdleTimeoutChoice{
	{label: "30 sec", timeoutMs: 30_000},
	{label: "1 min", timeoutMs: 60_000},
	{label: "2 min", timeoutMs: 120_000},
	{label: "5 min", timeoutMs: 300_000},
	{label: "disabled", timeoutMs: 0},
}

func formatHTTPIdleTimeoutMs(timeoutMs int) string {
	for _, choice := range httpIdleTimeoutChoices {
		if choice.timeoutMs == timeoutMs {
			return choice.label
		}
	}
	return strconv.FormatFloat(float64(timeoutMs)/1000, 'f', -1, 64) + " sec"
}

func parseHTTPIdleTimeoutLabel(value string) (int, bool) {
	for _, choice := range httpIdleTimeoutChoices {
		if choice.label == value {
			return choice.timeoutMs, true
		}
	}
	return 0, false
}

func warningBoolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// settingsConfig is upstream's showSettingsSelector config (interactive-mode.ts:4872-4918), read from the settings and the running UI.
func settingsConfig(sc *SlashContext) (SettingsConfig, error) {
	sm := sc.SettingsManager
	settings := sm.Get()
	httpIdleTimeoutMs, err := sm.GetHttpIdleTimeoutMs()
	if err != nil {
		return SettingsConfig{}, err
	}
	defaultModel := "not set"
	if provider, model := sm.GetDefaultProvider(), sm.GetDefaultModel(); provider != "" && model != "" {
		defaultModel = provider + "/" + model
	}
	var available []*ai.Model
	var current *ai.Model
	if sc.SettingsModels != nil {
		available, current = sc.SettingsModels()
	}
	thinkingLevel := sm.GetDefaultThinkingLevel()
	if thinkingLevel == "" {
		thinkingLevel = DefaultThinkingLevel
	}
	var thinkingLevels []string
	if sc.AvailableThinkingLevels != nil {
		thinkingLevels = sc.AvailableThinkingLevels()
	}
	currentTheme := tui.SystemThemeName
	if sc.ThemeSelection != nil {
		if selection := sc.ThemeSelection(); selection != "" {
			currentTheme = selection
		}
	} else if setting := sm.GetThemeSetting(); setting != nil && *setting != "" {
		currentTheme = *setting
	}
	tuiMode := sm.GetTuiMode()
	if sc.CurrentTuiMode != nil {
		tuiMode = tui.TuiMode(sc.CurrentTuiMode())
	}
	return SettingsConfig{
		AutoCompact:                sm.GetCompactionEnabled(),
		DefaultModel:               defaultModel,
		CurrentModel:               current,
		AvailableDefaultModels:     available,
		ShowImages:                 sm.GetShowImages(),
		ImageWidthCells:            sm.GetImageWidthCells(),
		AutoResizeImages:           sm.GetImageAutoResize(),
		BlockImages:                sm.GetBlockImages(),
		EnableSkillCommands:        sm.GetEnableSkillCommands(),
		SteeringMode:               sm.GetSteeringMode(),
		FollowUpMode:               sm.GetFollowUpMode(),
		Transport:                  sm.GetTransport(),
		HttpIdleTimeoutMs:          httpIdleTimeoutMs,
		CacheWarmingMode:           sm.GetCacheWarmingMode(),
		ThinkingLevel:              string(thinkingLevel),
		AvailableThinkingLevels:    thinkingLevels,
		ModelThinkingLevels:        maps.Clone(settings.ModelThinkingLevels),
		CurrentTheme:               currentTheme,
		TerminalTheme:              tui.GetTerminalTheme(),
		AvailableThemes:            tui.ActiveThemeRegistry().Names(),
		HideThinkingBlock:          sm.GetHideThinkingBlock(),
		MermaidRenderingMode:       sm.GetMermaidRenderingMode(),
		ShowCacheMissNotices:       sm.GetShowCacheMissNotices(),
		CollapseChangelog:          sm.GetCollapseChangelog(),
		EnableInstallTelemetry:     sm.GetEnableInstallTelemetry(),
		DoubleEscapeAction:         sm.GetDoubleEscapeAction(),
		TreeFilterMode:             sm.GetTreeFilterMode(),
		ShowHardwareCursor:         sm.GetShowHardwareCursor(),
		EditorPaddingX:             sm.GetEditorPaddingX(),
		OutputPad:                  sm.GetOutputPad(),
		AutocompleteMaxVisible:     sm.GetAutocompleteMaxVisible(),
		QuietStartup:               sm.GetQuietStartup(),
		DefaultProjectTrust:        string(sm.GetDefaultProjectTrust()),
		ClearOnShrink:              sm.GetClearOnShrink(),
		ShowTerminalProgress:       sm.GetShowTerminalProgress(),
		TuiMode:                    string(tuiMode),
		FullscreenExitOutput:       sm.GetFullscreenExitOutput(),
		FullscreenScrollbar:        sm.GetFullscreenScrollbar(),
		FullscreenCopyOnSelect:     sm.GetFullscreenCopyOnSelect(),
		FullscreenWheelScrollLines: sm.GetFullscreenWheelScrollLines(),
		Warnings:                   sm.GetWarnings(),
		MaskSecretInput:            settings.GetMaskSecretInput(),
	}, nil
}

// settingsHandler implements /settings (upstream showSettingsSelector, interactive-mode.ts:4869): it opens the settings selector, whose callbacks save each change through the SettingsManager setter and then apply its live effect.
func settingsHandler(sc *SlashContext) error {
	if sc.SettingsManager == nil {
		sc.Append("Settings manager unavailable.")
		return nil
	}
	config, err := settingsConfig(sc)
	if err != nil {
		return err
	}
	if sc.ShowSettingsSelector == nil {
		// Headless / test: just dump current values.
		var b strings.Builder
		b.WriteString("**Settings** (read-only in this mode)\n\n")
		for _, item := range NewSettingsSelectorComponent(config, SettingsCallbacks{}).GetSettingsList().Items() {
			fmt.Fprintf(&b, "  %s: %s\n", item.Label, item.CurrentValue)
		}
		sc.Append(b.String())
		return nil
	}
	return settingsHandlerTUI(sc, config)
}

// settingsHandlerTUI presents the selector for config; its callbacks save and apply each change.
func settingsHandlerTUI(sc *SlashContext, config SettingsConfig) error {
	sc.ShowSettingsSelector(func(done func()) *SettingsSelectorComponent {
		var selector *SettingsSelectorComponent
		// applied reports a failed save and shows the row's saved value again; otherwise it applies the change's live effect. value is the row's value as the live effect reads it.
		applied := func(id, value string, err error) bool {
			if err != nil {
				showStatusOrAppend(sc, fmt.Sprintf("Failed to save settings: %v", err))
				if fresh, freshErr := settingsConfig(sc); freshErr == nil {
					for _, item := range NewSettingsSelectorComponent(fresh, SettingsCallbacks{}).GetSettingsList().Items() {
						if item.ID == id {
							selector.GetSettingsList().UpdateValue(id, item.CurrentValue)
						}
					}
				}
				return false
			}
			if sc.OnSettingApplied != nil {
				sc.OnSettingApplied(id, value)
			}
			return true
		}
		sm := sc.SettingsManager
		selector = NewSettingsSelectorComponent(config, SettingsCallbacks{
			OnAutoCompactChange: func(enabled bool) {
				applied("autocompact", boolSettingValue(enabled), sm.SetCompactionEnabled(enabled))
			},
			OnShowImagesChange: func(enabled bool) {
				applied("show-images", boolSettingValue(enabled), sm.SetShowImages(enabled))
			},
			OnImageWidthCellsChange: func(width int) {
				applied("image-width-cells", strconv.Itoa(width), sm.SetImageWidthCells(width))
			},
			OnAutoResizeImagesChange: func(enabled bool) {
				applied("auto-resize-images", boolSettingValue(enabled), sm.SetImageAutoResize(enabled))
			},
			OnBlockImagesChange: func(blocked bool) {
				applied("block-images", boolSettingValue(blocked), sm.SetBlockImages(blocked))
			},
			OnEnableSkillCommandsChange: func(enabled bool) {
				applied("skill-commands", boolSettingValue(enabled), sm.SetEnableSkillCommands(enabled))
			},
			OnSteeringModeChange: func(mode string) {
				applied("steering-mode", mode, sm.SetSteeringMode(mode))
			},
			OnFollowUpModeChange: func(mode string) {
				applied("follow-up-mode", mode, sm.SetFollowUpMode(mode))
			},
			OnTransportChange: func(transport string) {
				applied("transport", transport, sm.SetTransport(transport))
			},
			OnHttpIdleTimeoutMsChange: func(timeoutMs int) {
				if applied("http-idle-timeout", strconv.Itoa(timeoutMs), sm.SetHttpIdleTimeoutMs(float64(timeoutMs))) {
					showStatusOrAppend(sc, "HTTP idle timeout: "+formatHTTPIdleTimeoutMs(timeoutMs))
				}
			},
			OnCacheWarmingModeChange: func(mode CacheWarmingMode) {
				if applied("cache-warming-mode", string(mode), sm.SetCacheWarmingMode(mode)) {
					showStatusOrAppend(sc, "Cache warming: "+string(mode))
				}
			},
			OnModelThinkingLevelChange: func(provider, modelID, level string) {
				if applied("model-thinking", "", sm.SetModelThinkingLevel(provider, modelID, ai.ThinkingLevel(level))) && sc.ApplyModelThinkingLevel != nil {
					sc.ApplyModelThinkingLevel(provider, modelID, level)
				}
			},
			OnModelThinkingLevelRemove: func(provider, modelID string) {
				if applied("model-thinking", "", sm.RemoveModelThinkingLevel(provider, modelID)) && sc.ApplyModelThinkingLevel != nil {
					sc.ApplyModelThinkingLevel(provider, modelID, "")
				}
			},
			OnThemeChange: func(theme string) {
				applied("theme", theme, sm.SetTheme(theme))
			},
			OnThemePreview: func(theme string) {
				if sc.PreviewTheme != nil {
					sc.PreviewTheme(theme)
				}
			},
			OnHideThinkingBlockChange: func(hidden bool) {
				applied("hide-thinking", boolSettingValue(hidden), sm.SetHideThinkingBlock(hidden))
			},
			OnMermaidRenderingModeChange: func(mode MermaidRenderingMode) {
				applied("mermaid-rendering", string(mode), sm.SetMermaidRenderingMode(mode))
			},
			OnShowCacheMissNoticesChange: func(shown bool) {
				applied("cache-miss-notices", boolSettingValue(shown), sm.SetShowCacheMissNotices(shown))
			},
			OnCollapseChangelogChange: func(collapsed bool) {
				applied("collapse-changelog", boolSettingValue(collapsed), sm.SetCollapseChangelog(collapsed))
			},
			OnEnableInstallTelemetryChange: func(enabled bool) {
				applied("install-telemetry", boolSettingValue(enabled), sm.SetEnableInstallTelemetry(enabled))
			},
			OnQuietStartupChange: func(quiet QuietStartup) {
				applied("quiet-startup", quiet.String(), sm.SetQuietStartup(quiet))
			},
			OnDefaultProjectTrustChange: func(defaultProjectTrust string) {
				applied("default-project-trust", defaultProjectTrust, sm.SetDefaultProjectTrust(DefaultProjectTrust(defaultProjectTrust)))
			},
			OnDoubleEscapeActionChange: func(action string) {
				applied("double-escape-action", action, sm.SetDoubleEscapeAction(action))
			},
			OnTreeFilterModeChange: func(mode string) {
				applied("tree-filter-mode", mode, sm.SetTreeFilterMode(mode))
			},
			OnMaskSecretInputChange: func(enabled bool) {
				applied("mask-secret-input", boolSettingValue(enabled), sm.SetMaskSecretInput(enabled))
			},
			OnShowHardwareCursorChange: func(enabled bool) {
				applied("show-hardware-cursor", boolSettingValue(enabled), sm.SetShowHardwareCursor(enabled))
			},
			OnEditorPaddingXChange: func(padding int) {
				applied("editor-padding", strconv.Itoa(padding), sm.SetEditorPaddingX(padding))
			},
			OnOutputPadChange: func(padding OutputPad) {
				applied("output-padding", strconv.Itoa(int(padding)), sm.SetOutputPad(padding))
			},
			OnAutocompleteMaxVisibleChange: func(maxVisible int) {
				applied("autocomplete-max-visible", strconv.Itoa(maxVisible), sm.SetAutocompleteMaxVisible(maxVisible))
			},
			OnClearOnShrinkChange: func(enabled bool) {
				applied("clear-on-shrink", boolSettingValue(enabled), sm.SetClearOnShrink(enabled))
			},
			OnShowTerminalProgressChange: func(enabled bool) {
				applied("terminal-progress", boolSettingValue(enabled), sm.SetShowTerminalProgress(enabled))
			},
			OnTuiModeChange: func(mode string) {
				// Pi's onTuiModeChange saves the mode only after the renderer switches; a refused switch keeps the running mode and the saved default.
				if sc.SwitchTuiMode != nil && !sc.SwitchTuiMode(mode) {
					if sc.CurrentTuiMode != nil {
						selector.GetSettingsList().UpdateValue("tui-mode", sc.CurrentTuiMode())
					}
					return
				}
				if applied("tui-mode", mode, sm.SetTuiMode(tui.TuiMode(mode))) && sc.CurrentTuiMode != nil {
					selector.GetSettingsList().UpdateValue("tui-mode", sc.CurrentTuiMode())
				}
			},
			OnFullscreenExitOutputChange: func(output FullscreenExitOutput) {
				applied("fullscreen-exit-output", string(output), sm.SetFullscreenExitOutput(output))
			},
			OnFullscreenScrollbarChange: func(mode string) {
				applied("fullscreen-scrollbar", mode, sm.SetFullscreenScrollbar(mode))
			},
			OnFullscreenCopyOnSelectChange: func(enabled bool) {
				applied("fullscreen-copy-on-select", boolSettingValue(enabled), sm.SetFullscreenCopyOnSelect(enabled))
			},
			OnFullscreenWheelScrollLinesChange: func(lines WheelScrollLines) {
				applied("fullscreen-wheel-scroll-lines", wheelScrollLinesLabel(lines), sm.SetFullscreenWheelScrollLines(lines))
			},
			OnWarningsChange: func(warnings WarningSettings) {
				applied("warnings", "configure", sm.SetWarnings(warnings))
			},
			OnCancel: done,
		})
		return selector
	})
	return nil
}

func reloadHandler(sc *SlashContext) error {
	if sc.Reload == nil {
		// Headless context: just reload settings if we have a manager.
		if sc.SettingsManager != nil {
			sc.SettingsManager.Reload()
		}
		if sc.Append != nil {
			sc.Append("Settings and keybindings reloaded.")
		}
		return nil
	}
	if err := sc.Reload(); err != nil {
		if errors.Is(err, errReloadBlocked) {
			return nil
		}
		// interactive-mode.ts:6250-6254 shows "Reload failed: <error.message>".
		return fmt.Errorf("Reload failed: %w", err)
	}

	// Build a diagnostic summary. Mirrors upstream showLoadedResources
	// with showDiagnosticsWhenQuiet:true (interactive-mode.ts:4518).
	explain := reloadExplainRequested(sc.Args)
	var summary strings.Builder
	summary.WriteString("Reloaded keybindings, extensions, skills, prompts, themes, and context files")
	if sc.ReloadSavedProjectTrust != nil && sc.ReloadSavedProjectTrust() {
		summary.WriteString("; saved project trust")
	}
	if sc.ReloadDiagnostics != nil {
		// Upstream's status names the reloaded resource kinds without counts;
		// /reload --explain reports the counts.
		diag := sc.ReloadDiagnostics()
		// Surface conflict diagnostics inline so the user sees command /
		// shortcut clashes after reload without a separate command.
		// Mirrors upstream's `[Extension issues]` block from
		// showLoadedResources (interactive-mode.ts:1392-1404).
		if len(diag.Diagnostics) > 0 {
			summary.WriteString("\nExtension issues:")
			for _, d := range diag.Diagnostics {
				summary.WriteString("\n  • " + d)
			}
		}
		if explain {
			summary.WriteString("\nReload explanation:")
			fmt.Fprintf(&summary, "\n  • context files: %d", diag.ContextFiles)
			fmt.Fprintf(&summary, "\n  • skills: %d", diag.Skills)
			fmt.Fprintf(&summary, "\n  • prompts: %d", diag.Prompts)
			fmt.Fprintf(&summary, "\n  • extensions: %d", diag.Extensions)
			fmt.Fprintf(&summary, "\n  • themes: %d", diag.Themes)
			if diag.ReloadDuration > 0 {
				fmt.Fprintf(&summary, "\n  • subprocess reload wall: %s", diag.ReloadDuration.Round(time.Millisecond))
			}
			if len(diag.Cells) > 0 {
				summary.WriteString("\n  • placement:")
				for _, c := range diag.Cells {
					renderCellExplain(&summary, c)
				}
			}
			summary.WriteString("\n  • each extension reloads on its own: replacements start and register beside the old processes; an extension that fails to load is dropped and listed under Extension issues")
		}
	} else if explain {
		summary.WriteString("\nReload explanation unavailable: no reload diagnostics provider")
	}
	showStatusOrAppend(sc, summary.String())
	return nil
}

func reloadExplainRequested(args string) bool {
	for field := range strings.FieldsSeq(args) {
		if field == "--explain" || field == "explain" {
			return true
		}
	}
	return false
}

// renderCellExplain writes a single placement decision line. Output is
// intentionally human-readable and machine-parseable (one line per cell
// followed by indented details).
func renderCellExplain(out *strings.Builder, c ReloadCellDiag) {
	name := strings.Join(c.Extensions, "+")
	if name == "" {
		name = c.Key
	}
	cacheTag := ""
	switch {
	case c.Quarantined:
		cacheTag = " [quarantined→fissioned]"
	case c.Strategy != "isolated" && c.Cached:
		cacheTag = " [cache hit]"
	case c.Strategy != "isolated" && c.BuildDuration > 0:
		cacheTag = fmt.Sprintf(" [cold build %s]", c.BuildDuration.Round(time.Millisecond))
	}
	fmt.Fprintf(out, "\n      - %s/%s: %s%s", c.Strategy, shortHash(c.Hash, c.Key), name, cacheTag)
	if c.Reason != "" {
		fmt.Fprintf(out, "\n          reason: %s", c.Reason)
	}
	if c.BinaryPath != "" {
		fmt.Fprintf(out, "\n          artifact: %s", c.BinaryPath)
	}
	if c.Replaced {
		out.WriteString("\n          replaced previous cell")
	}
}

func shortHash(hash, fallback string) string {
	h := hash
	if h == "" {
		h = fallback
	}
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// exportHandler implements /export [path]. The first argument, which may be
// quoted, selects the output: a .jsonl path writes the current branch
// (upstream session.exportToJsonl), and any other path, or none, writes HTML
// (upstream session.exportToHtml). A failure returns upstream's "Failed to
// export session" error. Mirrors upstream handleExportCommand.
func exportHandler(sc *SlashContext) error {
	if sc.CurrentSession == nil {
		sc.Append("Session unavailable in this build.")
		return nil
	}
	s := sc.CurrentSession()
	if s == nil {
		sc.Append("No active session.")
		return nil
	}
	outputPath := pathCommandArgument(sc.Args)
	var filePath string
	var err error
	if strings.HasSuffix(outputPath, ".jsonl") {
		if sc.ExportToJsonl != nil {
			filePath, err = sc.ExportToJsonl(outputPath)
		} else {
			filePath, err = ExportSessionToJsonl(s, outputPath, nil)
		}
	} else {
		var tools func(name string) *extension.ToolRenderers
		if sc.ToolRenderers != nil {
			tools = sc.ToolRenderers()
		}
		var state ShareState
		if sc.ShareState != nil {
			state = sc.ShareState()
		}
		var settingsTheme string
		if sc.SettingsManager != nil {
			settingsTheme = sc.SettingsManager.GetTheme()
		}
		filePath, err = ExportSessionToHTML(s.Path(), outputPath, tools, s.CWD(), state, ExportThemeName(settingsTheme))
	}
	if err != nil {
		return fmt.Errorf("Failed to export session: %w", err)
	}
	showStatusOrAppend(sc, "Session exported to: "+filePath)
	return nil
}

// pathCommandArgument returns the path argument of /export or /import as upstream
// getPathCommandArgument does: a quoted argument runs to its closing quote and
// is absent when the quote is unclosed, and an unquoted one ends at the first
// whitespace.
func pathCommandArgument(args string) string {
	args = strings.TrimLeftFunc(args, isJSWhitespace)
	if args == "" {
		return ""
	}
	if quote := args[0]; quote == '"' || quote == '\'' {
		closing := strings.IndexByte(args[1:], quote)
		if closing < 0 {
			return ""
		}
		return args[1 : 1+closing]
	}
	if end := strings.IndexFunc(args, isJSWhitespace); end >= 0 {
		return args[:end]
	}
	return args
}

// importHandler implements /import <path.jsonl>. After a confirmation it
// imports through the Session, which copies the file into the session
// directory without replacing a stored session. A stored working directory
// that no longer exists is offered for replacement by the current one. A
// missing source is a non-fatal error; any other failure is fatal. Mirrors
// upstream handleImportCommand.
func importHandler(sc *SlashContext) error {
	inputPath := pathCommandArgument(sc.Args)
	if inputPath == "" {
		return errors.New("Usage: /import <path.jsonl>")
	}
	if sc.ShowExtensionSelector == nil || sc.ImportSession == nil {
		return errors.New("Session import is not available in this context.")
	}
	if !confirmSlash(sc, "Import session", "Replace current session with "+inputPath+"?") {
		showStatusOrAppend(sc, "Import cancelled")
		return nil
	}
	cancelled, err := sc.ImportSession(inputPath, "")
	if missing, ok := errors.AsType[*MissingSessionCwdError](err); ok {
		if !confirmSlash(sc, "Session cwd not found", FormatMissingSessionCwdPrompt(missing.Issue)) {
			showStatusOrAppend(sc, "Import cancelled")
			return nil
		}
		cancelled, err = sc.ImportSession(inputPath, missing.Issue.FallbackCwd)
	}
	if notFound, ok := errors.AsType[*SessionImportFileNotFoundError](err); ok {
		return fmt.Errorf("Failed to import session: %w", notFound)
	}
	if err != nil {
		if sc.FatalRuntimeError != nil {
			return sc.FatalRuntimeError("Failed to import session", err)
		}
		return err
	}
	if cancelled {
		showStatusOrAppend(sc, "Import cancelled")
		return nil
	}
	showStatusOrAppend(sc, "Session imported from: "+inputPath)
	return nil
}

// confirmSlash asks a yes/no question as upstream showExtensionConfirm does:
// a Yes/No selector headed by the title and message.
func confirmSlash(sc *SlashContext, title, message string) bool {
	if sc.ShowExtensionConfirm != nil {
		return sc.ShowExtensionConfirm(title, message)
	}
	choice, ok := sc.ShowExtensionSelector(title+"\n"+message, []string{"Yes", "No"}, "")
	return ok && choice == "Yes"
}

// shareHandler implements /share: export the active branch with the pi.share
// presentation entry and upload it to PiG's share gateway. The command itself
// is the explicit opt-in; shareSession displays the privacy notice before it
// starts the upload.
func shareHandler(sc *SlashContext) error {
	if sc.CurrentSession == nil {
		sc.Append("Session unavailable.")
		return nil
	}
	session := sc.CurrentSession()
	if session == nil {
		sc.Append("No active session.")
		return nil
	}
	if sc.ShareSession == nil {
		return errors.New("Session sharing is not available in this context.")
	}
	state := ShareState{}
	if sc.ShareState != nil {
		state = sc.ShareState()
	}
	status, err := sc.ShareSession(session, state, func(message string) { showStatusOrAppend(sc, message) })
	if errors.Is(err, errShareCancelled) {
		showStatusOrAppend(sc, "Share cancelled")
		return nil
	}
	if err != nil {
		return err
	}
	// Keep the pre-upload privacy status readable. Consecutive ShowStatus calls
	// coalesce, so the terminal result is appended as a distinct transcript row.
	if sc.AppendText != nil {
		sc.AppendText(status)
	} else if sc.Append != nil {
		sc.Append(status)
	}
	return nil
}

// modelThinkingClearOverrideValue is the sentinel select-item value that
// clears a per-model thinking override, matching upstream's internal
// CLEAR_OVERRIDE_VALUE ("__clear__" in settings-selector.ts). It is never a
// real thinking level, so it cannot collide with one.
const modelThinkingClearOverrideValue = "__clear__"

// thinkingDescriptions labels each level in the /settings submenu and /thinking.
var thinkingDescriptions = map[string]string{
	"off":     "No reasoning",
	"minimal": "Very brief reasoning (~1k tokens)",
	"low":     "Light reasoning (~2k tokens)",
	"medium":  "Moderate reasoning (~8k tokens)",
	"high":    "Deep reasoning (~16k tokens)",
	"xhigh":   "Extra-high reasoning (~32k tokens)",
	"max":     "Maximum reasoning",
}
