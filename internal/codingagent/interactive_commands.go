package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func (m *InteractiveMode) dispatchSlash(ctx context.Context, line string) {
	sc := m.buildSlashContext(ctx)
	// Mirror any extension-registered commands into the registry just
	// before dispatch so a freshly loaded command resolves.
	m.syncExtensionSlashCommands()
	name, args := parseSlashLine(line)
	if canonical, ok := m.slashRegistry.Resolve(name); ok && canonical == "model" && args != "" {
		m.findExactModelMatch(ctx, args, func(spec string, found bool) {
			sc.ResolveModel = func(string) (string, bool) { return spec, found }
			m.dispatchSlashContext(sc, line)
		})
		return
	}
	m.dispatchSlashContext(sc, line)
}

func (m *InteractiveMode) dispatchSlashContext(sc *SlashContext, line string) {
	extCtx := m.extCtx
	if err := m.slashRegistry.Dispatch(sc, line, extCtx); err != nil {
		if errors.Is(err, ErrInteractiveCrashed) {
			return
		}
		if errors.Is(err, ErrUnknownSlashCommand) {
			// Upstream behavior: unknown slash commands are forwarded to the LLM
			// as regular user messages (not shown as errors).
			return
		}
		m.showError(err.Error())
	}
	m.tuiInst.Render()
}

// writeDebugLog dumps the current frame and message history to a debug log
// file under the agent dir and returns its path. Mirrors upstream
// handleDebugCommand (interactive-mode.ts:5580) and getDebugLogPath
// (config.ts:452). The rendered-lines section reflects pig's line renderer
// (D1), so its content differs from pi's full-screen TUI by construction.
func (m *InteractiveMode) writeDebugLog() (string, error) {
	width, height := m.tuiInst.Width(), m.tuiInst.Height()
	lines := m.tuiInst.RenderSnapshot(width)

	var b strings.Builder
	fmt.Fprintf(&b, "Debug output at %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Terminal: %dx%d\n", width, height)
	fmt.Fprintf(&b, "Total lines: %d\n\n", len(lines))
	b.WriteString("=== All rendered lines with visible widths ===\n")
	for i, line := range lines {
		esc, _ := json.Marshal(line)
		fmt.Fprintf(&b, "[%d] (w=%d) %s\n", i, widthx.VisibleWidth(line), esc)
	}
	b.WriteString("\n=== Agent messages (JSONL) ===\n")
	for _, msg := range m.agent.Messages() {
		j, err := json.Marshal(msg)
		if err != nil {
			return "", fmt.Errorf("marshal message: %w", err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	path := filepath.Join(m.opts.AgentDir, AppName+"-debug.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// buildAutocompleteProvider constructs slash-command and @-file autocomplete in Pi's builtin, template, extension, then skill order. Equal fuzzy scores preserve that order.
// Mirrors createBaseAutocompleteProvider (interactive-mode.ts:747-776).
func (m *InteractiveMode) buildAutocompleteProvider() tui.AutocompleteProvider {
	builtins := BuiltinSlashCommands()
	cmds := make([]tui.SlashCommand, 0, len(builtins)+len(m.promptTemplates))
	for _, b := range builtins {
		if b.Hidden {
			continue
		}
		sc := tui.SlashCommand{Name: b.Name, Description: b.Description, ArgumentHint: b.ArgumentHint}
		if b.Name == "login" {
			sc.GetArgumentCompletions = m.loginArgCompletions
		}
		if b.Name == "model" {
			sc.GetArgumentCompletions = m.modelArgCompletions
		}
		if b.Name == "thinking" {
			sc.GetArgumentCompletions = m.thinkingArgCompletions
		}
		cmds = append(cmds, sc)
	}

	// Add prompt templates as slash commands in autocomplete.
	// Mirrors upstream createBaseAutocompleteProvider templateCommands
	// (interactive-mode.ts:445-452).
	for _, pt := range m.promptTemplates {
		desc := pt.Description
		if pt.Scope != "" {
			desc = "[" + pt.Scope + "] " + desc
		}
		sc := tui.SlashCommand{
			Name:         pt.Name,
			Description:  desc,
			ArgumentHint: pt.ArgumentHint,
		}
		cmds = append(cmds, sc)
	}

	if m.newRunner != nil {
		for _, rc := range m.newRunner.Commands() {
			cmds = append(cmds, m.extensionCommandSlashEntry(rc))
		}
	}

	// Add skill commands as /skill:name entries (when enabled in settings).
	// Mirrors upstream skillCommandList gate (interactive-mode.ts:758-770).
	skillCommandsEnabled := m.opts.SettingsManager == nil || m.opts.SettingsManager.GetEnableSkillCommands()
	if skillCommandsEnabled {
		for _, s := range m.opts.Skills {
			cmds = append(cmds, tui.SlashCommand{
				Name:        "skill:" + s.Name,
				Description: s.Description,
			})
		}
	}

	// Detect fd for fuzzy file search. Mirrors upstream ensureTool("fd")
	// (interactive-mode.ts:527). The pre-warm at startup already populated
	// <agentDir>/bin/fd when needed; LookupToolPath prefers that over PATH.
	fdPath := tools.LookupToolPath("fd", filepath.Join(m.opts.AgentDir, "bin"))

	cwd, _ := os.Getwd()
	prov := tui.NewCombinedProvider(cmds, cwd, fdPath)
	// Defer the fd subprocess off the keystroke path (parity with upstream's
	// async getFuzzyFileSuggestions); only meaningful when fd is present.
	prov.SetAsyncFileSearch(true)
	return prov
}

// modelArgCompletions fuzzy-filters the available runtime snapshot using getModelSearchText.
func (m *InteractiveMode) modelArgCompletions(prefix string) []tui.AutocompleteItem {
	items := m.availableModelItems()
	filtered := tui.FuzzyFilter(items, prefix, func(item tui.ModelSelectorItem) string {
		return tui.GetModelSearchText(tui.ModelSearchItem{ID: item.ID, Provider: item.Provider, Name: item.Name})
	})
	out := make([]tui.AutocompleteItem, 0, len(filtered))
	for _, item := range filtered {
		out = append(out, tui.AutocompleteItem{Value: item.FQ(), Label: item.ID, Description: item.Provider})
	}
	return out
}

// thinkingArgCompletions offers the current model's thinking levels after
// "/thinking ", as upstream's thinking command does
// (interactive-mode.ts:713-726).
func (m *InteractiveMode) thinkingArgCompletions(prefix string) []tui.AutocompleteItem {
	levels := tui.FuzzyFilter(levelsForModel(m.opts.Model), prefix, func(level string) string { return level })
	out := make([]tui.AutocompleteItem, 0, len(levels))
	for _, level := range levels {
		out = append(out, tui.AutocompleteItem{Value: level, Label: level})
	}
	return out
}

func (m *InteractiveMode) resolveAvailableModel(input string) (string, bool) {
	items := m.availableModelItems()
	if len(m.scopedModelIDs) > 0 {
		items = m.scopedModelItems(items)
	}
	return resolveModelFromItems(input, items)
}

func resolveModelFromItems(input string, items []tui.ModelSelectorItem) (string, bool) {
	needle := strings.TrimSpace(strings.ToLower(input))
	if needle == "" {
		return "", false
	}
	bareID := ""
	for _, mm := range items {
		fq := mm.FQ()
		if strings.EqualFold(fq, needle) {
			return fq, true
		}
		if strings.EqualFold(mm.ID, needle) {
			if bareID != "" {
				return "", false
			}
			bareID = fq
		}
	}
	if bareID != "" {
		return bareID, true
	}
	return "", false
}

// buildSlashContext assembles the SlashContext for one dispatch.
// Accessors capture references to interactive state so handlers see
// live values.
func (m *InteractiveMode) buildSlashContext(ctx context.Context) *SlashContext {
	var sc *SlashContext
	sc = &SlashContext{
		Append:      func(s string) { m.appendToChat(tui.NewMarkdown(s)) },
		AppendText:  func(s string) { m.appendChatBlock(tui.NewPaddedText(s, 1, 0, nil)) },
		AppendBlock: func(s string) { m.appendChatBlock(tui.NewPaddedText(s, 1, 1, nil)) },
		Clear: func() {
			m.disposeArminComponents()
			m.disposeMarkdownBlocks()
			m.chatContainer.Clear()
		},
		// Snapshot prompt templates so /help can list them.
		PromptTemplates: m.promptTemplates,
		ShowStatus:      m.showStatus,
		ShowWarning:     m.showWarning,
		Quit:            m.requestShutdown,
		Reset: func() {
			m.invalidatePostLoginSelection()
			// Emit session_before_switch for extensions. Mirrors upstream
			// emitBeforeSwitch("new") before teardown.
			if cancelled, err := m.beforeSessionSwitch(ctx, "new", ""); err != nil || cancelled {
				return
			}
			if err := m.settleActiveRun(); err != nil {
				return
			}
			emitSessionShutdown(m.newRunner, "new")
			if m.agent != nil {
				m.agent.SetMessages(nil)
			}
			m.restoreBuiltInHeader()
			if err := m.emitSessionStartWithUI(ctx, "new"); err != nil {
				m.failInputLoop(err)
			}
		},
		NewSession: func() error {
			m.invalidatePostLoginSelection()
			m.clearStatusIndicator("")
			if err := m.replaceSessionFromCommand(ctx, "new", ""); err != nil {
				if errors.Is(err, errSessionReplacementCancelled) {
					return nil
				}
				return err
			}
			// Upstream handleClearCommand (interactive-mode.ts:6664-6665).
			m.chatContainer.Add(tui.NewSpacer(1))
			m.chatContainer.Add(tui.NewPaddedText(tui.ActiveTheme().FgText("accent", "✓ New session started"), 1, 1, nil))
			return nil
		},
		FatalRuntimeError: func(prefix string, err error) error {
			return m.handleFatalRuntimeError(prefix, err)
		},
		ModelName: func() string {
			if m.opts.Model != nil {
				return m.opts.Model.DisplayName
			}
			return ""
		},
		ToolNames: func() []string {
			names := []string{}
			// Include the coding-tool baseline (read/write/bash/edit/grep/find/ls)
			// unless --no-builtin-tools hid them.
			if !m.opts.NoBuiltinTools {
				for _, t := range tools.CreateCodingTools(m.opts.CWD, m.opts.Settings, filepath.Join(m.opts.AgentDir, "bin")) {
					names = append(names, t.Name())
				}
			}
			// Include extension-provided tools from the new runner.
			if m.newRunner != nil {
				for _, t := range m.newRunner.Tools() {
					names = append(names, t.Definition.Name)
				}
			}
			return names
		},
		RegisteredTools: func() []extension.RegisteredTool {
			if m.newRunner == nil {
				return nil
			}
			return m.newRunner.Tools()
		},
		// Pi's /export, /share and /bug read session.state (agent-session.ts:3921, session-share.ts:38): the transcript's system prompt and the active tools.
		ShareState: func() ShareState {
			if m.agent == nil {
				return NewShareState("", nil)
			}
			return NewShareState(AgentStateSystemPrompt(m.agent.MessagesSnapshot()), m.agent.Tools())
		},
		ShareSession: func(session *Session, state ShareState, showStatus func(string)) (string, error) {
			return m.shareSessionWithLoader(ctx, session, state, showStatus)
		},
		SkillNames: func() []string { return nil },
		SessionInfo: func() (string, string, int) {
			if m.currentSession() == nil {
				return "", "", 0
			}
			n := 0
			if m.agent != nil {
				n = len(m.agent.Messages())
			}
			return m.currentSession().ID(), m.currentSession().CWD(), n
		},
		LastAssistant: func() string { return m.lastAssistantText },
		CopyClipboard: copyToClipboard,
		CostSummary: func() string {
			if m.agent == nil {
				return "No agent activity yet."
			}
			snap := m.agent.Timings().Snapshot()
			// Fill token usage from the session's stored usage totals.
			totals := m.footerUsageTotals()
			snap.TotalIn = totals.input
			snap.TotalOut = totals.output
			snap.CacheRead = totals.cacheRead
			snap.CacheWrite = totals.cacheWrite
			snap.Cost = totals.cost
			return formatCostSummary(snap, m.opts.Model)
		},
		ShowHotkeys:   m.handleHotkeysCommand,
		ShowChangelog: m.handleChangelogCommand,

		// Session ops.
		CurrentSession: func() *Session { return m.currentSession() },
		CacheWarmingStatus: func() *CacheWarmingStatus {
			if m.opts.SessionHandle == nil {
				return nil
			}
			return m.opts.SessionHandle.CacheWarmingStatus()
		},
		GetSessionName: func() string {
			if m.currentSession() == nil {
				return ""
			}
			return m.currentSession().GetSessionName()
		},
		SetSessionName: func(name string) error {
			if m.currentSession() == nil {
				return fmt.Errorf("no active session")
			}
			if m.opts.SessionHandle != nil {
				return m.opts.SessionHandle.SetSessionName(name)
			}
			_, current, err := m.currentSession().AppendSessionInfoName(name)
			if err != nil {
				return err
			}
			emitSessionInfoChanged(m.newRunner, current)
			return nil
		},
		// Update terminal title + status-line name
		// whenever /name sets a new session name.
		OnNameChange: func(name string) {
			tui.SetTerminalTitle(tui.BuildTerminalTitle(name, m.opts.CWD))
			m.statusLine.SetName(name)
		},
		FlushCompactionQueue: func() {
			// Navigation runs on the input loop, so the flush and any turn it
			// starts stay single-threaded with editor state.
			m.flushCompactionQueue(m.runCtx, true)
		},
		ForkAtEntry: func(entryID string) error {
			if m.currentSession() == nil {
				return fmt.Errorf("no active session")
			}
			if cancelled, err := m.beforeSessionFork(ctx, entryID, "at"); err != nil {
				return err
			} else if cancelled {
				return errSessionReplacementCancelled
			}
			if err := m.settleActiveRun(); err != nil {
				return err
			}
			// Fork is silent. Upstream reserves branch-summary components for
			// summarized branch navigation; see
			// .upstream/current/packages/coding-agent/src/modes/interactive/
			// interactive-mode.ts:4070-4180: the BranchSummary chip is a
			// COMPACTION artifact (only emitted when the user opts to
			// summarize a branch in the /tree flow), not a fork marker.
			// Fork itself is silent in upstream; status text is set by the
			// slash handler, not by chip emission here.
			if err := forkAndRebuild(m.currentSession(), m.agent, entryID); err != nil {
				return err
			}
			// Rebuild visible chat so it matches the LLM
			// context (forkAndRebuild moves the leaf + updates agent messages
			// but chatContainer still shows the abandoned-branch messages).
			// Mirrors upstream rebuildChatFromMessages called at
			// interactive-mode.ts:3346,3766,4516.
			m.rebuildChatFromSession()
			return nil
		},
		// ForkToNewSession implements /fork: branch the selected user message
		// into a NEW session file (up to its parent), switch to it, and
		// prefill the editor with the message text. Mirrors upstream
		// showUserMessageSelector → runtimeHost.fork(entryId)
		// (interactive-mode.ts:4381-4419, position "before").
		ForkToNewSession: func(userMsgEntryID string) error {
			m.invalidatePostLoginSelection()
			sess := m.currentSession()
			if sess == nil {
				return fmt.Errorf("no active session")
			}
			return m.replaceSessionFromCommand(ctx, "fork", userMsgEntryID)
		},
		// Wire CompactSession so /compact fires the real
		// coding.Session.Compact() which emits CompactionStart/End events
		// that processAgentEvents handles for spinner + chat rebuild.
		CompactSession: func(customInstructions string) error {
			if m.opts.SessionHandle == nil {
				return fmt.Errorf("no active session")
			}
			// Run in a goroutine so /compact returns immediately;
			// CompactionStart/End events drive the TUI state machine.
			sessHandle := m.opts.SessionHandle
			go func() {
				// coding.Session.Compact is accessed via the Inner() icodingagent.Session
				// pointer, but Compact() lives on the *coding.Session wrapper.
				// We access it through the InteractiveSessionHandle's underlying
				// *coding.Session by casting: both types implement the same
				// interface from this call site's perspective. Since only
				// coding.Session implements InteractiveSessionHandle in production,
				// this type assertion is safe. If it fails (test double), we no-op.
				type compactor interface {
					Compact(ctx context.Context, customInstructions string) error
				}
				if c, ok := sessHandle.(compactor); ok {
					_ = c.Compact(ctx, customInstructions)
				}
			}()
			return nil
		},
		ShowTrustSelector: m.runTrustSelector,
		// Three-option summarize-branch selector dialog.
		// Mirrors upstream showExtensionSelector (interactive-mode.ts:1935-1969):
		// a bordered editor-slot overlay (no filter input, fixed title + hint),
		// not the floating FilterableList overlay this used to be.
		ShowExtensionSelector: func(title string, options []string, description string) (string, bool) {
			sel := tui.NewExtensionSelector(title, options)
			sel.SetDescription(description)
			idx, ok := m.runEditorSlotExtensionSelector(sel)
			if !ok || idx < 0 || idx >= len(options) {
				return "", false
			}
			return options[idx], true
		},
		// Faithful ExtensionEditorComponent in the editor
		// slot. Mirrors upstream showExtensionEditor
		// (interactive-mode.ts:2065-2090) which creates an
		// ExtensionEditorComponent (border + spacer + Editor + spacer +
		// border + hint) and swaps it into the editorContainer.
		ShowExtensionEditor: func(title, description, prefill string) (string, bool) {
			ed := tui.NewExtensionEditorComponent(title, prefill)
			ed.SetDescription(description)
			return m.runEditorSlotExtensionEditor(ed)
		},
		BugReportInputs:       m.bugReportInputs,
		BugReportProviderName: m.bugReportProviderName,
		SummarizeForBugReport: m.summarizeForBugReport,
		UpstreamVersion:       func() string { return m.opts.AppVersion },
		CurrentTuiMode:        func() string { return m.opts.TuiMode },
		SwitchTuiMode: func(mode string) bool {
			if m.switchTuiMode(mode, true) {
				return true
			}
			m.showStatus("Close active overlays before changing TUI mode")
			return false
		},
		ShowSettingsList: func(items []tui.SettingItem, onChange func(id, value string) string) {
			sl := tui.NewSettingsList(items)
			m.runModalSettingsList(sl, onChange)
		},
		ShowSettingsSubmenu: func(items []tui.SettingItem, onChange func(id, value string) string) {
			sl := tui.NewSettingsListWithOptions(items, min(len(items), 10), false)
			m.runModalSettingsList(sl, onChange)
		},
		ShowSelectList: func(title, description string, items []tui.SelectItem, currentValue string) (string, bool) {
			sel := tui.NewSelectSubmenu(title, description, items, currentValue)
			return m.runEditorSlotSelectSubmenu(sel)
		},
		ShowThemeSelector: func(currentTheme string) (string, bool) {
			return m.runEditorSlotThemeSubmenu(currentTheme)
		},
		AvailableThinkingLevels: func() []string {
			return levelsForModel(m.opts.Model)
		},
		CurrentThinkingLevel: func() string { return m.thinkingLevel },
		SelectThinkingLevel:  func(level string) { m.selectThinkingLevel(level, false) },
		ShowThinkingSelector: m.showThinkingSelector,
		ModelThinkingSubmenu: m.modelThinkingSettingsSubmenu,
		NavigateTreeFull:     m.navigateTree,
		AbortBranchSummary: func() {
			if m.opts.SessionHandle != nil {
				m.opts.SessionHandle.AbortBranchSummary()
			}
		},
		SetEditorText: func(text string) {
			if strings.TrimSpace(m.editor.Text()) == "" {
				m.editor.SetText(text)
			}
		},
		WriteDebugLog: func() (string, error) { return m.writeDebugLog() },
		CloneCurrent: func() (string, error) {
			m.invalidatePostLoginSelection()
			if m.currentSession() == nil {
				return "", fmt.Errorf("no active session")
			}
			leaf := m.currentSession().LeafID()
			if leaf == nil {
				return "", fmt.Errorf("session is empty: nothing to clone")
			}
			if err := m.replaceSessionFromCommand(ctx, "clone", *leaf); err != nil {
				return "", err
			}
			m.editor.SetText("")
			return m.currentSession().Path(), nil
		},
		ListSessions: func() ([]SessionInfo, error) {
			sm := m.newSessionManager()
			return sm.ListSessions()
		},
		RenderTree: func() string {
			if m.currentSession() == nil {
				return ""
			}
			return renderTreeASCII(m.currentSession().Tree())
		},
		PickSession: func() (string, bool) {
			sm := m.newSessionManager()
			allLoader := func(options SessionListOptions) ([]SessionInfo, error) { return sm.ListAllSessions(options) }
			if m.opts.SessionDir != "" {
				allLoader = func(options SessionListOptions) ([]SessionInfo, error) { return sm.ListSessions(options) }
			}
			selector := newSessionSelector(
				func(options SessionListOptions) ([]SessionInfo, error) { return sm.ListCurrentSessions(options) },
				allLoader,
				func(path, name string) error {
					return m.runModalOperation(func() error { return sm.RenameSession(path, name) })
				},
				sm.deleteListedSession,
				func() string {
					if m.currentSession() == nil {
						return ""
					}
					return m.currentSession().Path()
				}(),
				m.keybindings,
			)
			return m.runEditorSlotSessionSelector(selector)
		},
		PickUserMessage: func() (string, bool) {
			if m.currentSession() == nil {
				return "", false
			}
			ids, labels := userMessageSelectorItems(m.currentSession())
			if len(ids) == 0 {
				m.showStatus("No messages to fork from")
				return "", false
			}
			list := tui.NewFilterableList("Fork from message", labels)
			idx, ok := m.runModalSelector(list, tui.OverlayOptions{Title: "Fork from message", WidthFraction: 0.85, HeightFraction: 0.7})
			if !ok || idx < 0 || idx >= len(ids) {
				return "", false
			}
			return ids[idx], true
		},
		PickTreeEntry: func(initialSelectedID string) (string, bool) {
			if m.currentSession() == nil {
				return "", false
			}
			root := m.currentSession().Tree()
			if root == nil {
				return "", false
			}
			ts := m.newSessionTreeSelect(root, initialSelectedID)
			ts.OnCopy = m.copySelectedTreeMessage
			// Wire label editing. When the user presses Shift+L,
			// the tree selector calls this callback; we persist via Session.
			if m.currentSession() != nil {
				ts.OnLabelEdit = func(entryID, label string) {
					var lp *string
					if label != "" {
						lp = &label
					}
					if err := m.currentSession().AppendLabelChange(entryID, lp); err != nil {
						m.showError(err.Error())
					}
				}
			}
			// Show selector in the editor slot (not overlay).
			// Mirrors upstream showSelector (.upstream/v0.69.0/...
			// interactive-mode.ts:3655-3666) which replaces editorContainer
			// with the component; chat stays visible above.
			id, ok := m.runEditorSlotTreeSelector(ts)
			return id, ok
		},
		AppendLabelChange: func(targetID, label string) error {
			if m.currentSession() == nil {
				return fmt.Errorf("no active session")
			}
			var lp *string
			if label != "" {
				lp = &label
			}
			return m.currentSession().AppendLabelChange(targetID, lp)
		},
		LoadSessionPath: func(path string) error {
			m.invalidatePostLoginSelection()
			m.clearStatusIndicator("")
			m.resumeStatus = ""
			return m.replaceSessionFromCommand(ctx, "resume", path)
		},
		ResumeStatus: func() string {
			status := m.resumeStatus
			m.resumeStatus = ""
			if status == "" {
				return statusResumed
			}
			return status
		},
		ImportSession: func(inputPath, cwdOverride string) (bool, error) {
			var importer sessionImporter
			if rt := m.opts.Runtime; rt != nil {
				importer = runtimeImporter{rt}
			} else if session, ok := m.opts.SessionHandle.(sessionImporter); ok {
				importer = session
			} else {
				return false, errors.New("Session import is not available in this context.")
			}
			m.invalidatePostLoginSelection()
			m.clearStatusIndicator("")
			result, err := importer.ImportFromJsonl(ctx, inputPath, cwdOverride)
			if err != nil || result.Cancelled {
				return result.Cancelled, err
			}
			m.renderCurrentSessionState()
			return false, nil
		},
		SwitchModel: func(spec string) error {
			m.invalidatePostLoginSelection()
			if m.opts.ModelBuilder == nil {
				return fmt.Errorf("model switching is not configured (no ModelBuilder)")
			}
			newModel, err := m.opts.ModelBuilder(spec)
			if err != nil {
				return err
			}
			prevModel := m.opts.Model
			if m.opts.SessionHandle != nil {
				if err := m.opts.SessionHandle.SetModel(newModel); err != nil {
					return err
				}
			} else if m.agent != nil {
				m.agent.SetModel(newModel)
			}
			m.opts.Model = newModel
			m.statusLine.SetModel(newModel)
			m.refreshThinkingLevel()
			if sc.modelSelectionPersist {
				if err := m.persistDefaultModel(newModel); err != nil {
					return err
				}
			}
			// Emit model_select for extensions.
			emitModelSelect(m.newRunner,
				modelToExtModel(newModel),
				modelToExtModel(prevModel),
				extension.ModelSelectSourceUser)
			m.tuiInst.Render()
			return nil
		},
		ResolveModel: func(input string) (string, bool) {
			return m.resolveAvailableModel(input)
		},
		PickModel: func(initialQuery string) (string, bool) {
			m.invalidatePostLoginSelection()
			spec, ok, persist := m.pickModel(ctx, initialQuery)
			sc.modelSelectionPersist = persist
			return spec, ok
		},

		// Settings UI.
		SettingsManager: m.opts.SettingsManager,

		// /scoped-models selector.
		ShowScopedModels: func() {
			m.showScopedModels()
		},

		// /reload: reload settings, templates, context files, and
		// re-fire session_start for extensions.
		// Mirrors upstream handleReloadCommand (interactive-mode.ts:6223)
		// and session.reload() (agent-session.ts:3291).
		Reload: func() error {
			if err := m.settleUserBash(); err != nil {
				return err
			}
			m.setRemoteEditor(nil)
			m.resetAutocompleteWrappers()
			dismiss := m.beginReloadBlocker()
			defer dismiss()
			m.reloadIssues = nil
			m.extensionConflicts = nil
			runner := m.newRunner
			if err := m.awaitReloadStep(ctx, func(ctx context.Context) error {
				if runner == nil {
					return nil
				}
				_, err := runner.Emit(ctx, extension.SessionShutdownEvent{Type: EventSessionShutdown, Reason: "reload"})
				return err
			}); err != nil {
				return err
			}

			// 2. Reload settings.
			if m.opts.SettingsManager != nil {
				m.opts.SettingsManager.Reload()
				m.opts.Settings = m.opts.SettingsManager.Get()
			}

			// 3. Reload keybindings.
			if m.keybindings != nil {
				if err := m.keybindings.Reload(); err != nil {
					fmt.Fprintf(os.Stderr, "keybindings reload: %v\n", err)
				}
				m.publishExtensionKeybindings()
			}

			// Resource resolution can await a temporary Git refresh. Keep the owner available until resolution completes, then publish its snapshot.
			if provider := m.opts.ReloadResourceProvider; provider != nil {
				var snapshot ReloadResourceSnapshot
				if err := m.awaitReloadStep(ctx, func(context.Context) error {
					snapshot = provider()
					return nil
				}); err != nil {
					return err
				}
				if snapshot.Err != nil {
					return snapshot.Err
				}
				m.applyReloadResourceSnapshot(snapshot)
			}

			// 5. Reload prompt templates. Their diagnostics are shown after
			//    the chat rebuild below, as upstream does.
			m.loadPromptTemplates()
			if provider := m.opts.ResourceSourceInfoProvider; m.opts.ReloadResourceProvider == nil && provider != nil {
				var infos map[string]ResourceSourceInfo
				if err := m.awaitReloadStep(ctx, func(context.Context) error {
					infos = provider()
					return nil
				}); err != nil {
					return err
				}
				m.resourceSourceInfo = infos
			}
			m.publishSlashCommandCatalog()

			// 6. Reload context files (AGENTS.md / CLAUDE.md).
			// Mirrors upstream resourceLoader.reload() which re-walks
			// the project tree for context files.
			if m.opts.ReloadResourceProvider == nil {
				m.opts.ContextFiles = LoadProjectContextFiles(
					m.opts.CWD, m.opts.AgentDir)
			}

			// 6b. Reload skills.
			// Mirrors upstream session.reload() → resourceLoader.reload()
			// which re-discovers skills from configured paths.
			m.reloadSkillsFromPaths()
			m.rebuildSystemPromptFromResources()

			// 6a. Reload subprocess extensions.
			// pig-specific: upstream has no subprocess extensions.
			//
			// Stage the embedded SDKs first. A reload recompiles out-of-tree
			// extensions, and it is the command reached for after rebuilding
			// pig itself, which is exactly when the staged SDK is older than
			// the running binary. Rebuilding against that stale copy reproduces
			// the bug the startup stage exists to prevent: an extension
			// carrying a defect the SDK already fixed, with nothing in the
			// session to explain it. EnsureSynced also prunes builds its
			// fingerprint proves stale, so they rebuild against the new SDK.
			if stage := m.opts.StageExtensionSDKs; stage != nil {
				if err := m.awaitReloadStep(ctx, func(context.Context) error { return stage() }); err != nil {
					if ctx.Err() != nil {
						return context.Cause(ctx)
					}
					m.reloadIssues = append(m.reloadIssues, "[extension] SDK staging failed: "+err.Error())
					fmt.Fprintf(os.Stderr, "extension SDK staging: %v\n", err)
				}
			}
			resetLogin := m.opts.SubprocessHost == nil
			if host := m.opts.SubprocessHost; host != nil {
				var reloadedExts []extension.Extension
				reloadErr := m.awaitReloadStep(ctx, func(ctx context.Context) error {
					// Pi flushes the reloaded factories' provider registrations after every factory has finished (bindCore), so no Provider callback runs while a later extension still loads. The host callbacks only queue their refresh until Reload returns.
					if m.opts.ModelRegistry != nil {
						defer m.opts.ModelRegistry.HoldRegistrationRefresh()()
					}
					var err error
					reloadedExts, err = host.Reload(ctx)
					return err
				})
				if ctx.Err() != nil {
					return context.Cause(ctx)
				}
				if reloadErr != nil {
					m.reloadIssues = append(m.reloadIssues, "[extension] reload failed: "+reloadErr.Error())
					fmt.Fprintf(os.Stderr, "extension reload: %v\n", reloadErr)
				} else {
					resetLogin = true
					if m.opts.ReloadBuiltinExtensions != nil {
						m.opts.BuiltinExtensions = m.opts.ReloadBuiltinExtensions()
					}
					orderedExts := ExtensionsInLoadOrder(reloadedExts, m.opts.BuiltinExtensions)
					// Mirrors upstream reload: tool and flag conflicts are
					// listed under Extension issues in complete load order.
					m.extensionConflicts = DetectExtensionConflicts(orderedExts)
					for _, err := range m.replaceExtensionRunner(reloadedExts) {
						m.reloadIssues = append(m.reloadIssues, "[tool] "+err.Error())
						fmt.Fprintf(os.Stderr, "tool reload: %v\n", err)
					}
				}
			} else {
				if m.opts.ReloadBuiltinExtensions != nil {
					m.opts.BuiltinExtensions = m.opts.ReloadBuiltinExtensions()
					m.extensionConflicts = DetectExtensionConflicts(m.opts.BuiltinExtensions)
					for _, err := range m.replaceExtensionRunner(nil) {
						m.reloadIssues = append(m.reloadIssues, "[tool] "+err.Error())
						fmt.Fprintf(os.Stderr, "tool reload: %v\n", err)
					}
				} else {
					for _, err := range m.refreshAgentTools() {
						m.reloadIssues = append(m.reloadIssues, "[tool] "+err.Error())
						fmt.Fprintf(os.Stderr, "tool reload: %v\n", err)
					}
				}
			}

			if resetLogin {
				m.restoreBuiltInHeader()
			}

			// Upstream rebuilds persisted chat after the replacement extension
			// runner is ready and before session_start initializes extension UI,
			// re-reading the display settings the rebuilt transcript uses
			// (restoreChatBeforeSessionStart, interactive-mode.ts:6210-6218).
			m.hideThinking = m.opts.Settings.GetHideThinkingBlock()
			m.outputPad = m.opts.Settings.GetOutputPad()
			m.rebuildChatFromSession()

			// The rebuilt chat precedes session_start notifications.
			runner = m.newRunner
			if err := m.awaitReloadStep(ctx, func(ctx context.Context) error {
				if runner == nil {
					return nil
				}
				_, err := runner.Emit(ctx, extension.SessionStartEvent{Type: EventSessionStart, Reason: "reload"})
				return err
			}); err != nil {
				return err
			}
			if err := m.extendResourcesFromExtensions(ctx, "reload"); err != nil {
				return err
			}
			m.loadThemes()

			// 8b. Mirrors upstream applyRuntimeSettings after reload:
			//     re-apply the terminal capability overrides.
			if m.opts.SettingsManager != nil {
				tui.SetCapabilityOverrides(m.opts.SettingsManager.GetTerminalCapabilityOverrides())
			}
			if err := m.initializeTerminalTheme(ctx, m.themeOutput()); err != nil {
				m.failInputLoop(err)
			}

			// 8c. Upstream rebuilds the loaded-resources listing from the
			//     reloaded resources (showLoadedResources after reload).
			m.showLoadedResources(false, true)

			// 9. Rebuild autocomplete (may have new slash commands from
			//    reloaded extensions).
			m.editor.SetAutocomplete(m.buildAutocompleteProvider())
			return nil
		},
		// ReloadDiagnostics returns resource counts after reload for the
		// /reload summary message. Mirrors upstream showLoadedResources
		// diagnostics (interactive-mode.ts:4518).
		ReloadDiagnostics: func() ReloadDiag {
			extCount := 0
			if m.newRunner != nil {
				extCount = m.newRunner.ExtensionCount()
			} else if m.opts.SubprocessHost != nil {
				extCount = m.opts.SubprocessHost.ExtensionCount()
			}
			themeCount := len(tui.ActiveThemeRegistry().Names())
			diag := ReloadDiag{
				ContextFiles: len(m.opts.ContextFiles),
				Skills:       len(m.opts.Skills),
				Prompts:      len(m.promptTemplates),
				Extensions:   extCount,
				Themes:       themeCount,
			}
			// Surface extension/tool reload failures captured during the Reload
			// closure so they render as "Extension issues" instead of vanishing to
			// a TUI-clobbered stderr.
			diag.Diagnostics = append(diag.Diagnostics, m.reloadIssues...)
			if m.opts.SubprocessHost != nil {
				if rep := m.opts.SubprocessHost.LastReloadReport(); rep != nil {
					if rep.Error != "" {
						diag.Diagnostics = append(diag.Diagnostics, "[extension] "+rep.Error)
					}
					diag.ReloadDuration = rep.Duration
					for _, c := range rep.Cells {
						if c.Quarantined {
							msg := "[extension] cell " + c.Key + " quarantined"
							if c.Reason != "" {
								msg += ": " + c.Reason
							}
							diag.Diagnostics = append(diag.Diagnostics, msg)
						}
						diag.Cells = append(diag.Cells, ReloadCellDiag{
							Key:           c.Key,
							Strategy:      string(c.Strategy),
							Language:      c.Language,
							Extensions:    append([]string(nil), c.Extensions...),
							Hash:          c.Hash,
							BinaryPath:    c.BinaryPath,
							Cached:        c.Cached,
							BuildDuration: c.BuildDuration,
							Replaced:      c.Replaced,
							Quarantined:   c.Quarantined,
							Reason:        c.Reason,
						})
					}
				}
			}
			return diag
		},
		// OnSettingApplied applies live state changes when /settings persists.
		// Mirrors upstream settings-selector.ts per-field callbacks.
		OnSettingApplied: func(id, value string) {
			switch id {
			case "skill-commands":
				m.editor.SetAutocomplete(m.buildAutocompleteProvider())
			case "transport":
				m.agent.SetTransport(ai.Transport(value))
			case "cache-miss-notices":
				m.rebuildChatFromSession()
			case "http-idle-timeout":
				if timeoutMs, err := strconv.Atoi(value); err == nil {
					_ = ai.ConfigureHTTPDispatcher(timeoutMs)
				}
			case "cache-warming-mode":
				if m.opts.SessionHandle != nil {
					_ = m.opts.SessionHandle.SetCacheWarmingMode(CacheWarmingMode(value))
				}
			case "hide-thinking":
				// Sync m.hideThinking with the new setting and update all
				// visible thinking blocks: same as Ctrl+T toggleThinkingVisibility
				// but driven by /settings instead of a keybind.
				newVal := value == "true"
				if m.hideThinking != newVal {
					// toggleThinkingVisibility already flips + persists: use
					// it only if the live state differs from the new setting.
					m.toggleThinkingVisibility()
				}
			case "autocompact":
				// Sync the session's auto-compaction flag via settings.
				// The session reads compaction settings dynamically from the
				// settings manager, so UpdateGlobal (already done in settingsHandler)
				// is sufficient. Also update the footer indicator.
				if m.statusLine != nil {
					m.statusLine.SetAutoCompactEnabled(value == "true")
				}
			case "theme":
				m.themeState.currentThemeSetting.Store(&value)
				m.applyThemeFromSettings(ctx)
			case "show-hardware-cursor":
				m.tuiInst.SetShowHardwareCursor(value == "true")
			case "tui-mode":
				// The settings list switched through SwitchTuiMode before saving, so this switch is a no-op there.
				// The fallback selector saves first; on refusal it restores the mode still in effect.
				if !m.switchTuiMode(value, true) {
					reverted := "regular"
					if m.altScreen != nil {
						reverted = "fullscreen"
					}
					if m.opts.SettingsManager != nil {
						_ = m.opts.SettingsManager.SetTuiMode(reverted)
					}
					m.showStatus("Close active overlays before changing TUI mode")
					break
				}
				// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:onTuiModeChange
				if m.activeStatusIndicator == nil {
					m.statusContainer.Clear()
				}
				m.showStatus("TUI mode: " + value)
			case "fullscreen-scrollbar":
				// Apply the scrollbar mode live to the fullscreen transcript view;
				// nil in regular mode (no-op). Mirrors upstream
				// applyFullscreenScrollbarSetting() (interactive-mode.ts:1877).
				if m.transcriptScrollView != nil {
					m.transcriptScrollView.SetScrollbar(value)
				}
			case "fullscreen-copy-on-select":
				if m.altScreen != nil {
					m.altScreen.SetCopyOnSelect(value == "true")
				}
			case "clear-on-shrink":
				enabled := value == "true"
				m.tuiInst.SetClearOnShrink(enabled)
				if !enabled && m.statusContainer != nil && m.statusContainer.IsEmpty() {
					m.statusContainer.Clear()
				}
			case "editor-padding":
				if padding, err := strconv.Atoi(value); err == nil {
					m.editor.SetPaddingX(padding)
				}
			case "output-padding":
				if padding, err := strconv.Atoi(value); err == nil {
					m.outputPad = max(0, min(1, padding))
					if m.agent.IsStreaming() {
						for _, block := range m.userBlocks {
							block.SetOutputPad(m.outputPad)
						}
						for _, block := range m.assistantBlocks {
							block.SetOutputPad(m.outputPad)
						}
						for _, component := range m.customMessageOrder {
							if padded, ok := component.(interface{ SetOutputPad(int) }); ok {
								padded.SetOutputPad(m.outputPad)
							}
						}
						m.tuiInst.RequestRender()
					} else {
						m.rebuildChatFromSession()
					}
				}
			case "autocomplete-max-visible":
				if maxVisible, err := strconv.Atoi(value); err == nil {
					m.editor.SetAutocompleteMaxVisible(maxVisible)
				}
			case "steering-mode":
				m.agent.SetSteeringMode(agent.QueueMode(value))
			case "follow-up-mode":
				m.agent.SetFollowUpMode(agent.QueueMode(value))
			case "show-images", "image-width-cells", "auto-resize-images", "block-images":
				// Update all existing tool execution components with new image settings.
				s := m.opts.SettingsManager.Get()
				show := s.GetShowImages() && !s.BlockImages
				width := s.GetImageWidthCells()
				m.toolMu.Lock()
				for _, comp := range m.toolOrder {
					comp.SetShowImages(show)
					comp.SetImageWidthCells(width)
				}
				m.toolMu.Unlock()
			}
			// Refresh merged settings so subsequent accesses see the new value.
			if m.opts.SettingsManager != nil {
				m.opts.Settings = m.opts.SettingsManager.Get()
			}
		},
		AgentDir:               m.opts.AgentDir,
		ProbeClipboardRead:     m.probeClipboardRead,
		ProbeImageFallback:     m.probeImageFallback,
		ProbeCancellableLoader: m.probeCancellableLoader,
		ProbeSelectList:        m.probeSelectList,
		ProbeOAuthShared:       m.probeOAuthShared,
		ProbeOAuthCallbackPage: m.probeOAuthCallbackPage,
		ProbeCopilotHeaders:    m.probeCopilotHeaders,
		ProbeOAuthCopilot:      m.probeOAuthCopilot,
		ProbeOAuthCopilotEnv:   m.probeOAuthCopilotEnv,
		ProbeOAuthAnthropic:    m.probeOAuthAnthropic,
		ProbeOAuthCodex:        m.probeOAuthCodex,
		LoginProviders:         func() []tui.OAuthProvider { return m.getLoginProviderOptions() },
		LogoutProviders:        m.getLogoutProviderOptions,
		SelectAuthMethod:       m.showLoginAuthTypeSelector,
		SelectAuthProvider: func(mode string, providers []tui.OAuthProvider, initialSearch string) (tui.OAuthProvider, bool) {
			sel := tui.NewOAuthSelector(mode, providers, initialSearch)
			_, ok := m.runEditorSlotOAuthSelector(sel)
			return sel.SelectedProvider(), ok
		},
		StartProviderLogin: func(provider tui.OAuthProvider) error {
			if provider.AuthType == "oauth" {
				return m.runOAuthLogin(ctx, provider.ID)
			}
			if m.loginAPIKeyProvider(provider.ID) {
				return nil
			}
			return m.runAPIKeyLogin(provider)
		},
		// /logout: remove stored credentials.
		Logout: func(provider string) error {
			return m.runOAuthLogout(ctx, provider)
		},
		ExtRunner: m.newRunner,
		Skills:    m.opts.Skills,
	}
	if m.opts.Llama != nil {
		sc.RunLlama = func() error { return m.runLlamaCommand(ctx) }
	}
	return sc
}

// buildAuthProviderName returns the user-facing provider label used by /login
// and /logout. Upstream renders provider.name in the login selector and in
// login/logout status labels, so delegate to the catalog name.
func buildAuthProviderName(provider string) string {
	return ai.ProviderDisplayName(provider)
}

// newSessionTreeSelect builds the /tree selector opened at the current leaf (or an explicit re-open target). It starts in the configured treeFilterMode, as Pi passes settingsManager.getTreeFilterMode() as initialFilterMode (interactive-mode.ts:5403, :5531).
func (m *InteractiveMode) newSessionTreeSelect(root *SessionTreeNode, initialSelectedID string) *tui.TreeSelect {
	settings := m.opts.SettingsManager
	if settings == nil {
		settings = &SettingsManager{merged: m.opts.Settings}
	}
	ts := tui.NewTreeSelectWithInitialFilter("Session tree", &treeNodeAdapter{n: root, f: newTreeRowFormatter(m.currentSession())}, settings.GetTreeFilterMode())
	ts.MaxVisibleLines = tui.TreeVisibleLines(m.tuiInst.Height())
	// Open at the current leaf instead of the bottom row, so /tree after forking lands where the user is (interactive-mode.ts:4621).
	currentLeafID := ""
	if leaf := m.currentSession().LeafID(); leaf != nil {
		currentLeafID = *leaf
	}
	ts.SetInitialCursor(currentLeafID, initialSelectedID)
	return ts
}
