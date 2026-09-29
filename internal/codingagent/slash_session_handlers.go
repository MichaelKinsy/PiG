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
	"os"
	"strconv"
	"strings"
	"time"

	pig "github.com/MichaelKinsy/PiG"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

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
	appendNameText(sc, "Session name set: "+args)
	// Update terminal title + status-line footer.
	if sc.OnNameChange != nil {
		sc.OnNameChange(args)
	}
	return nil
}

// appendNameText preserves literal names with the theme's dim foreground.
func appendNameText(sc *SlashContext, text string) {
	text = tui.ActiveTheme().FgText("dim", text)
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
	confirmation := th.FgText("accent", "✓ Debug log written") + "\n" + th.FgText("muted", path)
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
	if sc.CurrentSession != nil && sc.CurrentSession() != nil && sc.CurrentSession().LeafID() == nil {
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
		if name == "" {
			name = info.FirstMessage
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
		if s := sc.CurrentSession(); s != nil && len(s.Entries()) == 0 {
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
			if leaf := s.LeafID(); leaf != nil && *leaf == id {
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
// Mirrors upstream interactive-mode.ts:4090-4194.
func treeNavigateWithSummarize(sc *SlashContext, entryID string) error {
	// Skip the summarize prompt if the setting is enabled.
	if sc.SettingsManager != nil && sc.SettingsManager.GetBranchSummarySettings().SkipPrompt {
		// Navigate directly without summary.
		result, err := sc.NavigateTreeFull(context.Background(), entryID, false, "")
		if err != nil {
			showStatusOrAppend(sc, fmt.Sprintf("Navigation error: %v", err))
			return nil
		}
		if result.EditorText != "" && sc.SetEditorText != nil {
			sc.SetEditorText(result.EditorText)
		}
		if !result.Cancelled && !result.Aborted {
			showStatusOrAppend(sc, "Navigated to selected point")
			flushCompactionQueue(sc)
		}
		return nil
	}

	const (
		optNoSummary       = "No summary"
		optSummarize       = "Summarize"
		optCustomSummarize = "Summarize with custom prompt"
	)

	wantsSummary := false
	customInstructions := ""

	// Loop until user makes a complete choice or cancels (Esc) to re-open tree.
	// Mirrors upstream while(true) loop (interactive-mode.ts:4097-4120).
	for {
		choice, ok := sc.ShowExtensionSelector("Summarize branch?", []string{
			optNoSummary, optSummarize, optCustomSummarize,
		}, "")
		if !ok {
			// Esc from selector: re-open tree at same row.
			// Mirrors upstream interactive-mode.ts:4106.
			return treeHandlerWithInitial(sc, entryID)
		}
		wantsSummary = choice != optNoSummary

		if choice == optCustomSummarize {
			instructions, ok := sc.ShowExtensionEditor("Custom summarization instructions", "", "")
			if !ok {
				// Esc from editor: loop back to selector.
				// Mirrors upstream interactive-mode.ts:4113.
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
	id := n.Entry.Base.ID
	if len(id) > 8 {
		id = id[:8]
	}
	role := "?"
	text := ""
	if me, ok := n.Entry.AsMessage(); ok {
		role = me.Message.Role()
		text = extractMessageText(me)
	} else if n.Entry.Base.Type != "message" {
		role = n.Entry.Base.Type
	}
	if len(text) > 60 {
		text = text[:60] + "…"
	}
	text = strings.ReplaceAll(text, "\n", " ")
	ts := n.Entry.Base.Timestamp
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
	entries := ParseChangelog(pig.Changelog)
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

// settingItem describes one toggle-able setting for the /settings selector.
type settingItem struct {
	id    string
	label string
	desc  string
	// get returns the current display value.
	get func(s Settings) string
	// values is the ordered list of valid values to cycle through.
	values []string
	// apply writes the chosen value into the settings struct.
	apply func(s *Settings, val string)
	// gated, when non-nil, returns false to hide this item from /settings
	// when the current terminal lacks the relevant capability. Mirrors
	// upstream settings-selector.ts capability gating via getCapabilities().
	gated func() bool
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

// settingsItemsVisible returns settingsItems filtered by capability gates.
// Mirrors upstream settings-selector.ts which only inserts show-images /
// image-width-cells when getCapabilities().images is truthy.
func settingsItemsVisible() []settingItem {
	all := settingsItems()
	out := make([]settingItem, 0, len(all))
	for _, item := range all {
		if item.gated != nil && !item.gated() {
			continue
		}
		out = append(out, item)
	}
	return out
}

// settingsItems returns the list of toggle-able settings.
// Mirrors upstream SettingsSelectorComponent items (settings-selector.ts:163-253).
func settingsItems() []settingItem {
	boolStr := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	return []settingItem{
		{
			id: "autocompact", label: "Auto-compact",
			desc:   "Automatically compact context when it gets too large",
			values: []string{"true", "false"},
			get: func(s Settings) string {
				if s.Compaction != nil && s.Compaction.Enabled != nil {
					return boolStr(*s.Compaction.Enabled)
				}
				return "true"
			},
			apply: func(s *Settings, v string) {
				if s.Compaction == nil {
					s.Compaction = &CompactionSettingsJSON{}
				}
				b := v == "true"
				s.Compaction.Enabled = &b
			},
		},
		{
			id: "show-images", label: "Show images",
			desc:   "Render images inline in terminal",
			values: []string{"true", "false"},
			get:    func(s Settings) string { return boolStr(s.GetShowImages()) },
			apply: func(s *Settings, v string) {
				b := v == "true"
				s.ShowImages = &b
			},
			gated: func() bool { return tui.GetCapabilities().Images != "" },
		},
		{
			id: "image-width-cells", label: "Image width",
			desc:   "Preferred inline image width in terminal cells",
			values: []string{"60", "80", "120"},
			get: func(s Settings) string {
				return fmt.Sprintf("%d", s.GetImageWidthCells())
			},
			apply: func(s *Settings, v string) {
				n, _ := strconv.Atoi(v)
				if n > 0 {
					s.ImageWidthCells = n
				}
			},
			gated: func() bool { return tui.GetCapabilities().Images != "" },
		},
		{
			id: "auto-resize-images", label: "Auto-resize images",
			desc:   "Resize large images to 2000x2000 max for better model compatibility",
			values: []string{"true", "false"},
			get:    func(s Settings) string { return boolStr(s.GetImageAutoResize()) },
			apply:  func(s *Settings, v string) { b := v == "true"; s.ImageAutoResize = &b },
		},
		{
			id: "block-images", label: "Block images",
			desc:   "Prevent images from being sent to LLM providers",
			values: []string{"true", "false"},
			get:    func(s Settings) string { return boolStr(s.BlockImages) },
			apply: func(s *Settings, v string) {
				s.BlockImages = v == "true"
				s.blockImagesSet = true
			},
		},
		{
			id: "skill-commands", label: "Skill commands",
			desc:   "Register skills as /skill:name commands",
			values: []string{"true", "false"},
			get: func(s Settings) string {
				if s.EnableSkillCommands != nil && !*s.EnableSkillCommands {
					return "false"
				}
				return "true"
			},
			apply: func(s *Settings, v string) {
				enabled := v == "true"
				s.EnableSkillCommands = &enabled
			},
		},
		{
			id: "show-hardware-cursor", label: "Show hardware cursor",
			desc:   "Show the terminal cursor while still positioning it for IME support",
			values: []string{"true", "false"},
			get:    func(s Settings) string { return boolStr(s.GetShowHardwareCursor()) },
			apply:  func(s *Settings, v string) { b := v == "true"; s.ShowHardwareCursor = &b },
		},
		{
			id: "editor-padding", label: "Editor padding",
			desc:   "Horizontal padding for input editor (0-3)",
			values: []string{"0", "1", "2", "3"},
			get: func(s Settings) string {
				return fmt.Sprintf("%d", s.GetEditorPaddingX())
			},
			apply: func(s *Settings, v string) {
				n, _ := strconv.Atoi(v)
				p := max(0, min(3, n))
				s.EditorPaddingX = &p
			},
		},
		{
			id: "output-padding", label: "Output padding",
			desc:   "Horizontal padding for user messages, assistant messages, and thinking",
			values: []string{"0", "1"},
			get: func(s Settings) string {
				return fmt.Sprintf("%d", s.GetOutputPad())
			},
			apply: func(s *Settings, v string) {
				n, _ := strconv.Atoi(v)
				p := max(0, min(1, n))
				s.OutputPad = &p
			},
		},
		{
			id: "autocomplete-max-visible", label: "Autocomplete max items",
			desc:   "Max visible items in autocomplete dropdown (3-20)",
			values: []string{"3", "5", "7", "10", "15", "20"},
			get: func(s Settings) string {
				return fmt.Sprintf("%d", s.GetAutocompleteMaxVisible())
			},
			apply: func(s *Settings, v string) {
				n, _ := strconv.Atoi(v)
				m := max(3, min(20, n))
				s.AutocompleteMaxVisible = &m
			},
		},
		{
			id: "clear-on-shrink", label: "Clear on shrink",
			desc:   "Clear empty rows when content shrinks (may cause flicker)",
			values: []string{"true", "false"},
			get:    func(s Settings) string { return boolStr(s.GetClearOnShrink()) },
			apply:  func(s *Settings, v string) { b := v == "true"; s.ClearOnShrink = &b },
		},
		{
			id: "terminal-progress", label: "Terminal progress",
			desc:   "Show OSC 9;4 progress indicators in the terminal tab bar",
			values: []string{"true", "false"},
			get:    func(s Settings) string { return boolStr(s.GetShowTerminalProgress()) },
			apply:  func(s *Settings, v string) { b := v == "true"; s.ShowTerminalProgress = &b },
			// Upstream settings-selector.ts:438 inserts this item unconditionally
			// (no capability gate). The capability check still applies at write
			// time inside the TUI: the menu just always lets the user toggle
			// the persisted setting.
		},
		{
			id: "steering-mode", label: "Steering mode",
			desc:   "Enter while streaming queues steering messages. 'one-at-a-time': deliver one, wait for response. 'all': deliver all at once.",
			values: []string{"one-at-a-time", "all"},
			get: func(s Settings) string {
				if s.SteeringMode == "" {
					return "one-at-a-time"
				}
				return s.SteeringMode
			},
			apply: func(s *Settings, v string) { s.SteeringMode = v },
		},
		{
			id: "follow-up-mode", label: "Follow-up mode",
			desc:   fmt.Sprintf("%s queues follow-up messages until agent stops. 'one-at-a-time': deliver one, wait for response. 'all': deliver all at once.", tui.KeyDisplayText("alt+enter")),
			values: []string{"one-at-a-time", "all"},
			get: func(s Settings) string {
				if s.FollowUpMode == "" {
					return "one-at-a-time"
				}
				return s.FollowUpMode
			},
			apply: func(s *Settings, v string) { s.FollowUpMode = v },
		},
		{
			id: "transport", label: "Transport",
			desc:   "Preferred transport for providers that support multiple transports",
			values: []string{"sse", "websocket", "websocket-cached", "auto"},
			get: func(s Settings) string {
				if s.Transport == "" {
					return "auto"
				}
				return s.Transport
			},
			apply: func(s *Settings, v string) { s.Transport = v },
		},
		{
			id: "http-idle-timeout", label: "HTTP idle timeout",
			desc:   "Maximum idle gap while waiting for HTTP headers or body chunks. Disable for local models that pause longer than five minutes.",
			values: []string{"30 sec", "1 min", "2 min", "5 min", "disabled"},
			get: func(s Settings) string {
				timeoutMs, err := (&SettingsManager{merged: s}).GetHttpIdleTimeoutMs()
				if err != nil {
					return err.Error()
				}
				return formatHTTPIdleTimeoutMs(timeoutMs)
			},
			apply: func(s *Settings, v string) {
				timeoutMs, ok := parseHTTPIdleTimeoutLabel(v)
				if !ok {
					return
				}
				s.HTTPIdleTimeoutMs = &timeoutMs
				s.httpIdleTimeoutInvalid = nil
			},
		},
		{
			id: "cache-warming-mode", label: "Cache warming",
			desc:   "off; streaming while the agent runs; idle also between runs while continuation stays profitable",
			values: []string{"off", "streaming", "idle"},
			get: func(s Settings) string {
				return string((&SettingsManager{global: s}).GetCacheWarmingMode())
			},
			apply: func(s *Settings, v string) { s.CacheWarming = CacheWarmingMode(v) },
		},
		{
			id: "hide-thinking", label: "Hide thinking",
			desc:   "Hide thinking blocks in assistant responses",
			values: []string{"true", "false"},
			get:    func(s Settings) string { return boolStr(s.HideThinkingBlock) },
			apply: func(s *Settings, v string) {
				s.HideThinkingBlock = v == "true"
				s.hideThinkingBlockSet = true
			},
		},
		{
			id: "mermaid-rendering", label: "Mermaid diagrams",
			desc:   "Render Mermaid code blocks as Unicode diagrams",
			values: []string{"off", "final", "streaming"},
			get: func(s Settings) string {
				return (&SettingsManager{merged: s}).GetMermaidRenderingMode()
			},
			apply: func(s *Settings, v string) {
				if s.Markdown == nil {
					s.Markdown = &MarkdownSettings{}
				}
				s.Markdown.Mermaid = v
			},
		},
		{
			id: "cache-miss-notices", label: "Cache miss notices",
			desc:   "Show transcript notices for cache costs and provider recovery diagnostics",
			values: []string{"true", "false"},
			get:    func(s Settings) string { return boolStr(s.ShowCacheMissNotices) },
			apply: func(s *Settings, v string) {
				s.ShowCacheMissNotices = v == "true"
				s.showCacheMissNoticesSet = true
			},
		},
		{
			id: "collapse-changelog", label: "Collapse changelog",
			desc:   "Show condensed changelog after updates",
			values: []string{"true", "false"},
			get:    func(s Settings) string { return boolStr(s.CollapseChangelog) },
			apply: func(s *Settings, v string) {
				s.CollapseChangelog = v == "true"
				s.collapseChangelogSet = true
			},
		},
		{
			id: "quiet-startup", label: "Quiet startup",
			desc:   "Disable verbose printing at startup",
			values: []string{"true", "false"},
			apply: func(s *Settings, v string) {
				s.QuietStartup = v == "true"
				s.quietStartupSet = true
			},
			get: func(s Settings) string { return boolStr(s.QuietStartup) },
		},
		{
			id: "install-telemetry", label: "Install telemetry",
			desc:   "Send an anonymous version/update ping after changelog-detected updates",
			values: []string{"true", "false"},
			get: func(s Settings) string {
				if s.EnableInstallTelemetry == nil {
					return "true"
				}
				return boolStr(*s.EnableInstallTelemetry)
			},
			apply: func(s *Settings, v string) {
				enabled := v == "true"
				s.EnableInstallTelemetry = &enabled
			},
		},
		{
			id: "default-project-trust", label: "Default project trust",
			desc:   "Fallback behavior when no extension or saved trust decision decides project trust",
			values: []string{"Ask", "Always trust", "Never trust"},
			get: func(s Settings) string {
				switch s.DefaultProjectTrust {
				case "always":
					return "Always trust"
				case "never":
					return "Never trust"
				default:
					return "Ask"
				}
			},
			apply: func(s *Settings, v string) {
				switch v {
				case "Always trust":
					s.DefaultProjectTrust = "always"
				case "Never trust":
					s.DefaultProjectTrust = "never"
				default:
					s.DefaultProjectTrust = "ask"
				}
			},
		},
		{
			id: "double-escape-action", label: "Double-escape action",
			desc:   "Action when pressing Escape twice with empty editor",
			values: []string{"tree", "fork", "none"},
			get: func(s Settings) string {
				if s.DoubleEscapeAction == "" {
					return "tree"
				}
				return s.DoubleEscapeAction
			},
			apply: func(s *Settings, v string) { s.DoubleEscapeAction = v },
		},
		{
			id: "tree-filter-mode", label: "Tree filter mode",
			desc:   "Default filter when opening /tree",
			values: []string{"default", "no-tools", "user-only", "labeled-only", "all"},
			get: func(s Settings) string {
				if s.TreeFilterMode == "" {
					return "default"
				}
				return s.TreeFilterMode
			},
			apply: func(s *Settings, v string) { s.TreeFilterMode = v },
		},
		// pig divergence (D80): configurable input privacy leaves the upstream setting order intact.
		{
			id: "mask-secret-input", label: "Mask secret input",
			desc:   "PiG default: hide secret input with a count and last four characters. Differs from Pi; false restores Pi's plain-text behavior.",
			values: []string{"true", "false"},
			get:    func(s Settings) string { return boolStr(s.GetMaskSecretInput()) },
			apply:  func(s *Settings, v string) { enabled := v == "true"; s.MaskSecretInput = &enabled },
		},
		{
			id: "warnings", label: "Warnings",
			desc:   "Enable or disable individual warnings",
			values: []string{"configure"},
			get:    func(Settings) string { return "configure" },
			apply: func(s *Settings, _ string) {
				if s.Warnings == nil {
					s.Warnings = &WarningSettings{}
				}
			},
		},
		{
			// Mirrors upstream settings-selector.ts's "model-thinking" item
			// (id "model-thinking", label "Default thinking level per
			// model"). Upstream has no separate global "Thinking level"
			// settings-list entry: the global default is set via /thinking
			// (app.thinking.cycle / app.thinking.save), already ported in
			// thinking_selector.go. This item instead opens a per-model
			// override submenu, handled specially in settingsHandlerTUI.
			id: "model-thinking", label: "Default thinking level per model",
			desc: fmt.Sprintf("Override the default thinking level for specific models. %s cycles in-session.",
				tui.ActionKeyDisplayText("app.thinking.cycle")),
			values: nil,
			get: func(s Settings) string {
				if len(s.ModelThinkingLevels) == 0 {
					return "none"
				}
				return fmt.Sprintf("%d configured", len(s.ModelThinkingLevels))
			},
			apply: func(*Settings, string) {}, // no-op: applied via the submenu directly
		},
		{
			id: "tui-mode", label: "TUI mode",
			desc:   "Interface layout; fullscreen mode is experimental",
			values: []string{"regular", "fullscreen"},
			get: func(s Settings) string {
				return (&SettingsManager{merged: s}).GetTuiMode()
			},
			apply: func(s *Settings, v string) { s.TuiMode = v },
		},
		{
			id: "fullscreen-exit-output", label: "Fullscreen exit output",
			desc:   "Print the transcript or only a session resume hint when exiting fullscreen mode",
			values: []string{"transcript", "resume-hint"},
			get: func(s Settings) string {
				return (&SettingsManager{merged: s}).GetFullscreenExitOutput()
			},
			apply: func(s *Settings, v string) { s.FullscreenExitOutput = v },
		},
		{
			id: "fullscreen-scrollbar", label: "Fullscreen scrollbar",
			desc:   "Scrollbar behavior in fullscreen mode; has no effect in regular mode",
			values: []string{"auto", "always", "hidden"},
			get: func(s Settings) string {
				return (&SettingsManager{merged: s}).GetFullscreenScrollbar()
			},
			apply: func(s *Settings, v string) { s.FullscreenScrollbar = v },
		},
		{
			id: "fullscreen-copy-on-select", label: "Fullscreen copy on select",
			desc:   "Automatically copy selected text in fullscreen mode; disable to copy selections with Ctrl+X",
			values: []string{"true", "false"},
			get: func(s Settings) string {
				return boolStr((&SettingsManager{merged: s}).GetFullscreenCopyOnSelect())
			},
			apply: func(s *Settings, v string) {
				enabled := v == "true"
				s.FullscreenCopyOnSelect = &enabled
			},
		},
		{
			id: "theme", label: "Theme",
			desc:   "Color theme for the interface",
			values: []string{"auto", "dark", "light"},
			get: func(s Settings) string {
				if s.Theme == "" {
					return tui.ActiveTheme().Name
				}
				return s.Theme
			},
			apply: func(s *Settings, v string) {
				if v == "auto" {
					s.Theme = ""
				} else {
					s.Theme = v
				}
			},
		},
	}
}

// settingsHandler implements /settings: shows a selector of toggle-able
// settings, cycling values on each selection. Mirrors upstream
// SettingsSelectorComponent (settings-selector.ts) in line-renderer form.
func settingsHandler(sc *SlashContext) error {
	if sc.SettingsManager == nil {
		sc.Append("Settings manager unavailable.")
		return nil
	}

	// Prefer the dedicated SettingsList overlay (upstream parity).
	if sc.ShowSettingsList != nil {
		return settingsHandlerTUI(sc)
	}

	if sc.ShowExtensionSelector == nil {
		// Headless / test: just dump current values.
		s := sc.SettingsManager.Get()
		var b strings.Builder
		b.WriteString("**Settings** (read-only in this mode)\n\n")
		for _, item := range settingsItemsVisible() {
			fmt.Fprintf(&b, "  %s: %s\n", item.label, item.get(s))
		}
		sc.Append(b.String())
		return nil
	}

	// Fallback: generic ShowExtensionSelector (shouldn't reach here
	// in interactive mode but kept for safety).
	items := settingsItemsVisible()
	for {
		s := sc.SettingsManager.Get()
		options := make([]string, len(items))
		for i, item := range items {
			options[i] = fmt.Sprintf("%s  [%s]", item.label, item.get(s))
		}
		choice, ok := sc.ShowExtensionSelector("Settings (Enter to toggle, Esc to close)", options, "")
		if !ok {
			break
		}
		var selected *settingItem
		for i := range items {
			if options[i] == choice {
				selected = &items[i]
				break
			}
		}
		if selected == nil {
			continue
		}
		current := selected.get(s)
		nextVal := cycleValue(current, selected.values)
		if err := sc.SettingsManager.UpdateGlobal(func(gs *Settings) {
			selected.apply(gs, nextVal)
		}); err != nil {
			showStatusOrAppend(sc, fmt.Sprintf("Failed to save settings: %v", err))
			continue
		}
		showStatusOrAppend(sc, fmt.Sprintf("%s: %s", selected.label, nextVal))
		if sc.OnSettingApplied != nil {
			sc.OnSettingApplied(selected.id, nextVal)
		}
	}
	return nil
}

// settingsHandlerTUI uses the dedicated two-column SettingsList component
// that matches upstream's SettingsSelectorComponent layout. The list stays
// open until Esc and applies each change in place without a status line, as
// upstream's selector callbacks do; only a failed save is reported.
func settingsHandlerTUI(sc *SlashContext) error {
	items := settingsItemsVisible()
	s := sc.SettingsManager.Get()
	tuiItems := make([]tui.SettingItem, len(items))
	for i, item := range items {
		tuiItems[i] = tui.SettingItem{
			ID:           item.id,
			Label:        item.label,
			Description:  item.desc,
			CurrentValue: item.get(s),
			Values:       item.values,
		}
		if item.id == "tui-mode" && sc.CurrentTuiMode != nil {
			tuiItems[i].CurrentValue = sc.CurrentTuiMode()
		}
		if item.id == "model-thinking" {
			tuiItems[i].Submenu = sc.ModelThinkingSubmenu
		}
	}

	// The Warnings row opens a nested settings list whose changes save
	// immediately, as upstream WarningSettingsSubmenu does.
	configureWarnings := func() {
		warnings := sc.SettingsManager.GetWarnings()
		warningItems := []tui.SettingItem{{
			ID:           "anthropic-extra-usage",
			Label:        "Anthropic extra usage",
			Description:  "Warn when Anthropic subscription auth may use paid extra usage",
			CurrentValue: warningBoolString(warnings.AnthropicExtraUsage),
			Values:       []string{"true", "false"},
		}}
		onWarningChange := func(id, value string) string {
			if id != "anthropic-extra-usage" {
				return value
			}
			next := warnings
			next.AnthropicExtraUsage = value == "true"
			if err := sc.SettingsManager.SetWarnings(next); err != nil {
				showStatusOrAppend(sc, fmt.Sprintf("Failed to save settings: %v", err))
			} else {
				warnings = next
				if sc.OnSettingApplied != nil {
					sc.OnSettingApplied("warnings", "configure")
				}
			}
			return warningBoolString(warnings.AnthropicExtraUsage)
		}
		if sc.ShowSettingsSubmenu != nil {
			sc.ShowSettingsSubmenu(warningItems, onWarningChange)
			return
		}
		sc.ShowSettingsList(warningItems, onWarningChange)
	}

	// onChange saves one change and returns the value its row shows
	// afterwards: the new value, as upstream's SettingsList keeps it, or the
	// saved value when the change was cancelled or could not be saved.
	onChange := func(changedID, changedValue string) string {
		var selected *settingItem
		for i := range items {
			if items[i].id == changedID {
				selected = &items[i]
				break
			}
		}
		if selected == nil {
			return changedValue
		}
		saved := func() string {
			if changedID == "tui-mode" && sc.CurrentTuiMode != nil {
				return sc.CurrentTuiMode()
			}
			return selected.get(sc.SettingsManager.Get())
		}

		if changedID == "warnings" {
			configureWarnings()
			return saved()
		}
		if changedID == "theme" && sc.ShowThemeSelector != nil {
			chosen, ok := sc.ShowThemeSelector(saved())
			if !ok || chosen == "" {
				return saved()
			}
			changedValue = chosen
		}

		appliedValue := changedValue
		if changedID == "http-idle-timeout" {
			timeoutMs, ok := parseHTTPIdleTimeoutLabel(changedValue)
			if !ok {
				showStatusOrAppend(sc, fmt.Sprintf("Failed to save settings: invalid HTTP idle timeout %q", changedValue))
				return saved()
			}
			appliedValue = strconv.Itoa(timeoutMs)
		}

		// Pi's onTuiModeChange saves the mode only after the renderer switches; a refused switch keeps the running mode and the saved default.
		if changedID == "tui-mode" && sc.SwitchTuiMode != nil && !sc.SwitchTuiMode(changedValue) {
			return saved()
		}

		if err := sc.SettingsManager.UpdateGlobal(func(gs *Settings) {
			selected.apply(gs, changedValue)
		}); err != nil {
			showStatusOrAppend(sc, fmt.Sprintf("Failed to save settings: %v", err))
			return saved()
		}
		if sc.OnSettingApplied != nil {
			sc.OnSettingApplied(selected.id, appliedValue)
		}
		if changedID == "tui-mode" && sc.CurrentTuiMode != nil {
			return sc.CurrentTuiMode()
		}
		return changedValue
	}
	sc.ShowSettingsList(tuiItems, onChange)
	return nil
}

// cycleValue returns the next value in the cycle after current.
// If current is not in values, returns values[0].
func cycleValue(current string, values []string) string {
	for i, v := range values {
		if v == current {
			return values[(i+1)%len(values)]
		}
	}
	if len(values) > 0 {
		return values[0]
	}
	return current
}

// reloadHandler implements /reload. Reloads settings, prompt templates,
// context files (AGENTS.md/CLAUDE.md), and re-fires session_start for
// extensions. Mirrors upstream handleReloadCommand (interactive-mode.ts:4453).
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
		// interactive-mode.ts:6250-6254 shows "Reload failed: <error.message>".
		return fmt.Errorf("Reload failed: %w", err)
	}

	// Build a diagnostic summary. Mirrors upstream showLoadedResources
	// with showDiagnosticsWhenQuiet:true (interactive-mode.ts:4518).
	explain := reloadExplainRequested(sc.Args)
	var summary strings.Builder
	summary.WriteString("Reloaded keybindings, extensions, skills, prompts, themes, and context files")
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
		filePath, err = ExportSessionToJsonl(s, outputPath, nil)
	} else {
		var tools []extension.RegisteredTool
		if sc.RegisteredTools != nil {
			tools = sc.RegisteredTools()
		}
		filePath, err = ExportSessionToHTML(s.Path(), outputPath, tools, s.CWD())
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
