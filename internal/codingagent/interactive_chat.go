package codingagent

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) appendToChat(comp tui.Component) {
	m.chatContainer.Add(comp)
}

// markdownThemeWithSettings is getMarkdownThemeWithSettings (interactive-mode.ts:1378): the active theme's markdown theme with the
// codeBlockIndent setting, read when the component is built.
func (m *InteractiveMode) markdownThemeWithSettings() *tui.MarkdownTheme {
	theme := tui.GetMarkdownTheme()
	var indent string
	if m.opts.SettingsManager != nil {
		indent = m.opts.SettingsManager.GetCodeBlockIndent()
	} else {
		indent = m.opts.Settings.GetCodeBlockIndent()
	}
	theme.CodeBlockIndent = &indent
	return &theme
}

func (m *InteractiveMode) newUserMessageBlock(text string) *tui.UserMessageComponent {
	block := tui.NewUserMessageComponent(text, m.markdownThemeWithSettings(), m.outputPad, nil)
	block.SetMarkdownTransform(func(markdown string, width int) string {
		return markdowntransform.CreateMarkdownTransform(extension.MarkdownMessageUser, false, m.markdownTransformers())(markdown, width)
	})
	block.SetMarkdownTransformState(func() string {
		state := m.mermaidRenderingMode()
		if theme := tui.ActiveTheme(); theme != nil {
			state += " " + theme.Name
		}
		return state
	})
	block.SetAsyncMarkdownTransform(m.asyncMarkdownTransform(nil, extension.MarkdownMessageUser, nil))
	m.userBlocks = append(m.userBlocks, block)
	m.markdownBlocks = append(m.markdownBlocks, block)
	return block
}

func (m *InteractiveMode) newAssistantMessageBlock() *tui.AssistantMessageComponent {
	block := tui.NewAssistantMessageComponent(nil, m.hideThinking, m.markdownThemeWithSettings(), "", nil, nil)
	if m.hiddenThinkingLabel != "" {
		block.SetHiddenThinkingLabel(m.hiddenThinkingLabel)
	}
	block.SetOutputPad(m.outputPad)
	block.SetMarkdownTransform(m.assistantMarkdownTransform(block, extension.MarkdownMessageAssistant))
	block.SetThinkingMarkdownTransform(m.assistantMarkdownTransform(block, extension.MarkdownMessageAssistantThinking))
	block.SetMarkdownTransformState(m.assistantMarkdownTransformState(block))
	block.SetAsyncMarkdownTransforms(m.asyncMarkdownTransform(block, extension.MarkdownMessageAssistant, block.Invalidate), m.asyncMarkdownTransform(block, extension.MarkdownMessageAssistantThinking, block.Invalidate))
	m.markdownBlocks = append(m.markdownBlocks, block)
	return block
}

func (m *InteractiveMode) disposeMarkdownBlocks() {
	for _, block := range m.markdownBlocks {
		block.Dispose()
	}
	m.markdownBlocks = nil
}

// Ports packages/coding-agent/src/modes/interactive/components/markdown-transform.ts.
// asyncMarkdownTransform snapshots UI-owned inputs before starting the complete chain off-loop. New content remains unpublished until every transformer has answered.
func (m *InteractiveMode) asyncMarkdownTransform(block *tui.AssistantMessageComponent, messageType extension.MarkdownMessageType, invalidate func()) *tui.AsyncMarkdownTransform {
	if m.backgroundCtx == nil || m.newRunner == nil || len(m.newRunner.GetMarkdownTransformers()) == 0 {
		return nil
	}
	return &tui.AsyncMarkdownTransform{
		Context: m.backgroundCtx,
		Start:   m.backgroundTasks.Go,
		Queue:   &m.markdownQueue,
		Invalidate: func() {
			if invalidate != nil {
				invalidate()
			}
			m.requestRender()
		},
		Prepare: func(markdown string, width int) func(context.Context) string {
			mode, theme := m.mermaidRenderingMode(), tui.ActiveTheme()
			transformers := []extension.MarkdownTransformer{createMermaidMarkdownTransformer(func() string { return mode }, theme)}
			transformers = append(transformers, m.newRunner.GetMarkdownTransformers()...)
			streaming := block != nil && m.evCurrentBlock == block && m.hasActiveAgentTurn()
			// Logical replacement revokes publication, not admitted chain execution. The captured Mode context owns invocation cancellation; subprocess inactivity and disconnect remain independent exit conditions.
			lifetime := m.backgroundCtx
			return func(context.Context) string {
				return markdowntransform.ApplyMarkdownTransformers(markdown, extension.MarkdownTransformContext{
					Context: lifetime, MessageType: messageType, IsStreaming: streaming, AvailableWidth: width,
				}, transformers)
			}
		},
	}
}

// updateAssistantMessageBlock applies the authoritative content snapshot and terminal state on both live events and session redraws. Tool calls are invisible boundaries between thinking runs.
func updateAssistantMessageBlock(block *tui.AssistantMessageComponent, message *agent.AssistantMessage) {
	message = message.Observe()
	view := tui.AssistantMessage{StopReason: string(message.StopReason), ErrorMessage: message.ErrorMessage}
	for _, content := range message.Content {
		switch c := content.(type) {
		case ai.TextContent:
			view.Content = append(view.Content, tui.AssistantContentBlock{Type: "text", Text: c.Text})
		case ai.ThinkingContent:
			view.Content = append(view.Content, tui.AssistantContentBlock{Type: "thinking", Thinking: c.Thinking})
		case ai.ToolCall:
			view.Content = append(view.Content, tui.AssistantContentBlock{Type: "toolCall"})
		}
	}
	block.UpdateContent(view)
}

// assistantMarkdownTransform builds the display-only transform for assistant text or thinking with its distinct messageType and this block's live streaming state, mode, and theme.
//
// The block is "streaming" while it is the current assistant block and the
// agent turn is active; once the turn ends or a later block becomes current it
// freezes to non-streaming (Mermaid then shows warnings / renders in final
// mode). Mode and theme are read per render so live setting/theme changes apply.
//
// None of those three inputs is a function of (markdown, width), so
// assistantMarkdownTransformState reports them into the render cache key.
// Without it the cache serves the render made under the previous state: with
// mermaidRendering "final" a diagram is skipped while streaming and then never
// drawn, because the turn ending changes neither the text nor the width.
//
// The built-in Mermaid transformer runs first, then the transformers
// extensions registered, in load order (upstream getMarkdownTransformers).
func (m *InteractiveMode) assistantMarkdownTransform(block *tui.AssistantMessageComponent, messageType extension.MarkdownMessageType) func(string, int) string {
	return func(markdown string, width int) string {
		streaming := m.evCurrentBlock == block && m.hasActiveAgentTurn()
		return markdowntransform.CreateMarkdownTransform(messageType, streaming, m.markdownTransformers())(markdown, width)
	}
}

// markdownTransformers is upstream InteractiveMode.getMarkdownTransformers:
// the Mermaid transformer, then each extension's.
func (m *InteractiveMode) markdownTransformers() []extension.MarkdownTransformer {
	transformers := []extension.MarkdownTransformer{
		createMermaidMarkdownTransformer(m.mermaidRenderingMode, tui.ActiveTheme()),
	}
	if m.newRunner != nil {
		transformers = append(transformers, m.newRunner.GetMarkdownTransformers()...)
	}
	return transformers
}

// assistantMarkdownTransformState fingerprints the live state
// assistantMarkdownTransform reads, for the Markdown render cache key.
func (m *InteractiveMode) assistantMarkdownTransformState(block *tui.AssistantMessageComponent) func() string {
	return func() string {
		streaming := m.evCurrentBlock == block && m.hasActiveAgentTurn()
		state := m.mermaidRenderingMode()
		if streaming {
			state += " streaming"
		}
		if theme := tui.ActiveTheme(); theme != nil {
			state += " " + theme.Name
		}
		return state
	}
}

// mermaidRenderingMode returns the active Mermaid rendering mode
// ("off"/"final"/"streaming"), defaulting to "streaming" when no settings
// manager is present. Mirrors settings-manager.ts getMermaidRenderingMode.
func (m *InteractiveMode) mermaidRenderingMode() string {
	if m.opts.SettingsManager != nil {
		return string(m.opts.SettingsManager.GetMermaidRenderingMode())
	}
	return "streaming"
}

// appendChatBlock appends a standalone text/markdown block preceded by a
// blank spacer line. Use this for error messages, status messages, and
// login flow messages: any content that is NOT an AssistantMessageComponent
// (which handles its own leading spacer). Mirrors upstream's pattern of
// inserting new Spacer(1) before standalone chat additions.
func (m *InteractiveMode) appendChatBlock(comp tui.Component) {
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(comp)
}

// addCacheMissNotice shows a formatted cache-miss notice: a spacer, then the text in the theme warning color at padding 1 (interactive-mode.ts addCacheMissNotice).
func (m *InteractiveMode) addCacheMissNotice(notice string) {
	m.appendChatBlock(themedNotice("warning", notice, 1))
}

// noticeFg and noticeBold style notice text as upstream theme.fg and
// theme.bold (chalk.bold) do: each closes only its own attribute.
func noticeFg(color, text string) string { return color + text + tui.FgClose(color) }

func noticeBold(text string) string { return "\x1b[1m" + text + tui.SGRBoldDimReset }

// binaryUpdateNoticeBody builds the heading+instruction line for the
// self-update notification, mirroring upstream showNewVersionNotification:
// bold-warning "Update Available", newline, muted instruction with the accent
// update command.
func binaryUpdateNoticeBody(t *tui.Theme, latestVersion, command string) string {
	return noticeBold(noticeFg(t.Warning, "Update Available")) +
		"\n" + noticeFg(t.Muted, fmt.Sprintf("New version %s is available. Run ", latestVersion)) + noticeFg(t.Accent, command)
}

// showNewVersionNotification appends the startup update notice: the heading block, the release note as a muted Markdown block
// between spacers, and the Changelog line, each padded by one column.
//
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:4635 (showNewVersionNotification)
func (m *InteractiveMode) showNewVersionNotification(release LatestPiRelease) {
	t := tui.ActiveTheme()
	var note string
	if release.Note != nil {
		note = strings.TrimSpace(*release.Note)
	}
	// pig divergence (D39): Pi links its fixed pi.dev changelog; a release note that is a bare URL is pig's changelog link.
	changelogURL, note := splitBinaryUpdateNotes(note)
	blocks := []tui.Component{tui.NewThemedText(func() string { return binaryUpdateNoticeBody(tui.ActiveTheme(), release.Version, AppName+" update") }, 1, 0)}
	if note != "" {
		muted := func(text string) string { return noticeFg(t.Muted, text) }
		blocks = append(blocks,
			tui.NewSpacer(1),
			tui.NewMarkdownWithOptions(note, 1, 0, m.markdownThemeWithSettings(), &tui.DefaultTextStyle{Color: muted}, nil),
			tui.NewSpacer(1),
		)
	}
	if changelogURL != "" {
		blocks = append(blocks, tui.NewThemedText(func() string {
			t := tui.ActiveTheme()
			link := noticeFg(t.Accent, changelogURL)
			if tui.GetCapabilities().Hyperlinks {
				link = tui.Hyperlink(link, changelogURL)
			}
			return noticeFg(t.Muted, "Changelog: ") + link
		}, 1, 0))
	}
	m.appendBorderedNotice(blocks...)
}

// splitBinaryUpdateNotes separates a manifest note into a changelog URL and a
// release note. PiG's release manifest carries the release page URL as its
// note; that URL is the changelog link, so it is not repeated as a note block.
// Any other text is a release note and names no changelog.
func splitBinaryUpdateNotes(notes string) (changelogURL, note string) {
	notes = strings.TrimSpace(notes)
	if u, err := url.Parse(notes); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && !strings.ContainsAny(notes, " \t\r\n") {
		return notes, ""
	}
	return "", notes
}

// packageUpdateNoticeBody builds the body text for the package-update
// notification, mirroring upstream showPackageUpdateNotification: bold-warning
// heading, muted instruction with the accent command, a muted "Packages:"
// label, then one "- <name>" line per package.
func packageUpdateNoticeBody(t *tui.Theme, packages []string) string {
	var body strings.Builder
	body.WriteString(noticeBold(noticeFg(t.Warning, "Package Updates Available")) +
		"\n" + noticeFg(t.Muted, "Package updates are available. Run ") + noticeFg(t.Accent, AppName+" update --extensions") +
		"\n" + noticeFg(t.Muted, "Packages:"))
	for _, name := range packages {
		body.WriteString("\n- " + name)
	}
	return body.String()
}

// ShowPackageUpdateNotification adds the bordered "Package Updates Available" notice naming the packages to the chat.
//
// upstream: interactive-mode.ts:4627 (showPackageUpdateNotification)
func (m *InteractiveMode) ShowPackageUpdateNotification(packages []string) {
	m.appendBorderedNotice(tui.NewThemedText(func() string { return packageUpdateNoticeBody(tui.ActiveTheme(), packages) }, 1, 0))
}

// finishPackageUpdateCheck shows the startup package update notice. On Windows
// it then restores pig's title, because npm can overwrite the shared console
// title while it checks package versions. Mirrors upstream run(), which
// restores the title in the check's finally on win32.
func (m *InteractiveMode) finishPackageUpdateCheck(goos string, updates []string) {
	if len(updates) > 0 {
		m.ShowPackageUpdateNotification(updates)
	}
	if goos == "windows" {
		m.updateTerminalTitle()
	}
}

// updateTerminalTitle sets the title from the session name and the cwd.
// Mirrors upstream updateTerminalTitle (interactive-mode.ts).
func (m *InteractiveMode) updateTerminalTitle() {
	setTerminalTitle(tui.BuildTerminalTitle(m.currentSession().GetSessionName(), m.opts.CWD))
}

// setTerminalTitle writes the terminal title; tests replace it.
var setTerminalTitle = tui.SetTerminalTitle

// appendBorderedNotice wraps the supplied body components between two
// warning-colored DynamicBorders, mirroring upstream's
// showNewVersionNotification / showPackageUpdateNotification layout: a leading
// Spacer(1), a DynamicBorder, the body blocks, and a closing DynamicBorder, all
// in the warning color. Callers supply pre-colored components.
func (m *InteractiveMode) appendBorderedNotice(blocks ...tui.Component) {
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(tui.NewDynamicBorderToken("warning"))
	for _, b := range blocks {
		m.chatContainer.Add(b)
	}
	m.chatContainer.Add(tui.NewDynamicBorderToken("warning"))
	m.tuiInst.Render()
}

// lastAssistantMessageText is session.getLastAssistantText(), which /copy reads (interactive-mode.ts handleCopyCommand): empty when the Session has no assistant text.
func (m *InteractiveMode) lastAssistantMessageText() string {
	if m.opts.SessionHandle == nil {
		return ""
	}
	if text := m.opts.SessionHandle.LastAssistantText(); text != nil {
		return *text
	}
	return ""
}

// handleCopyCommand copies to the clipboard and confirms it. Ports upstream
// handleCopyCommand (interactive-mode.ts): with preferSelection, an active
// fullscreen selection is copied when automatic copy-on-select is off;
// otherwise the last assistant message is copied. flashConfirmation flashes
// "Copied!" in fullscreen, otherwise a status line.
func (m *InteractiveMode) handleCopyCommand(flashConfirmation, preferSelection bool) {
	if preferSelection && m.altScreen != nil && !m.altScreen.GetCopyOnSelect() && m.altScreen.HasActiveSelection() {
		m.altScreen.CopyActiveSelectionToClipboard()
		return
	}
	text := m.lastAssistantMessageText()
	if text == "" {
		m.showError("No agent messages to copy yet.")
		return
	}
	if err := m.effectiveCopyClipboard()(text); err != nil {
		m.showError(err.Error())
		return
	}
	m.confirmMessageCopied(flashConfirmation)
}

var cancelledAssistantError = lazyregexp.New(`(?i)\b(?:abort(?:ed)?|cancel(?:l?ed)?)\b`)

// maybeSuggestBugReport emits one hint per interactive lifetime, excluding retryable and cancellation errors.
func (m *InteractiveMode) maybeSuggestBugReport(message *agent.AssistantMessage) {
	if message.StopReason != ai.StopReasonError || ai.IsRetryableAssistantError(message.LLMMessage()) || cancelledAssistantError.MatchString(message.ErrorMessage) {
		return
	}
	if m.maybeShowInstallChangeWarning() {
		return
	}
	m.suggestBugReport()
}

// suggestBugReport shows the hint once per interactive lifetime.
func (m *InteractiveMode) suggestBugReport() {
	if m.bugReportHintShown {
		return
	}
	m.bugReportHintShown = true
	m.chatContainer.Add(themedNotice("muted", "If this looks like a pig bug, /bug sends a report to the developers.", m.outputPad))
	m.tuiInst.RequestRender()
}

// showError appends an error line to the chat. Mirrors upstream showError:
// "Error: <message>" in the theme's error color.
func (m *InteractiveMode) showError(msg string) {
	if m.chatContainer == nil {
		return
	}
	m.appendChatBlock(themedNotice("error", "Error: "+msg, m.outputPad))
	if m.tuiInst != nil {
		m.tuiInst.RequestRender()
	}
	// pig additive (D95): a failure may be a cell or runtime file another pig pruned, so check where errors are shown.
	m.maybeShowInstallChangeWarning()
}

// confirmMessageCopied surfaces the copy confirmation. In fullscreen with
// flashConfirmation set it flashes "Copied!" on the alt-screen renderer;
// otherwise it appends the status line. Mirrors the
// `flashConfirmation && ui instanceof TuiAltScreen` branch upstream.
func (m *InteractiveMode) confirmMessageCopied(flashConfirmation bool) {
	if flashConfirmation && m.altScreen != nil {
		// Go has no optional method argument, so pass upstream's default.
		m.altScreen.Flash("Copied!", 1000)
		return
	}
	m.showStatus("Copied last agent message to clipboard")
}

// showManagedToolStatus mirrors upstream showManagedToolStatus: a spacer
// before the first report, then each report as a padded line, warnings
// prefixed and colored as warnings, info dimmed.
func (m *InteractiveMode) showManagedToolStatus(status tools.ToolStatus) {
	if m.chatContainer == nil {
		return
	}
	if !m.managedToolStatusStarted {
		m.chatContainer.Add(tui.NewSpacer(1))
		m.managedToolStatusStarted = true
	}
	message, token := status.Message, "dim"
	if status.Type == "warning" {
		message, token = "Warning: "+status.Message, "warning"
	}
	m.chatContainer.Add(themedNotice(token, message, 1))
	m.lastStatusSpacer = nil
	m.lastStatusText = nil
	if m.tuiInst != nil {
		m.tuiInst.Render()
	}
}

func (m *InteractiveMode) showStatus(msg string) {
	if m.chatContainer == nil {
		return
	}
	secondLast, last := m.chatContainer.LastTwoChildren()
	if last != nil && secondLast != nil && last == m.lastStatusText && secondLast == m.lastStatusSpacer {
		m.lastStatusMessage = msg
		m.lastStatusText.Invalidate()
	} else {
		spacer := tui.NewSpacer(1)
		m.lastStatusMessage = msg
		text := tui.NewThemedText(func() string { return tui.ActiveTheme().Fg("dim", m.lastStatusMessage) }, 1, 0)
		m.chatContainer.Add(spacer)
		m.chatContainer.Add(text)
		m.lastStatusSpacer = spacer
		m.lastStatusText = text
	}
	// Mirror upstream's `this.ui.requestRender()` (interactive-mode.ts:2918,2927).
	// Without this, status messages added inside synchronous handlers
	// (e.g. /tree empty-session, /branch-summarize cancelled) never make
	// it to the screen because nothing else triggers a render before the
	// The handler requests a render before it returns.
	if m.tuiInst != nil {
		m.tuiInst.Render()
	}
}

func (m *InteractiveMode) setWorkingVisible(visible bool) {
	m.workingVisible = visible
	if !visible {
		m.clearStatusIndicator("working")
	} else if !m.isIdle && m.tuiInst != nil && m.statusContainer != nil && (m.activeStatusIndicator == nil || m.activeStatusIndicator.Kind != "working") {
		m.startWorkingLoader()
	}
	if m.statusLine == nil {
		return
	}
	m.statusLine.SetWorking(visible && !m.isIdle)
	if m.tuiInst != nil {
		m.tuiInst.Render()
	}
}

// themedNotice is a chat notice whose color follows theme changes: ThemedText rebuilds it after the UI invalidates the chat (interactive-mode.ts ThemedText call sites).
func themedNotice(token, message string, paddingX int) *tui.ThemedText {
	return tui.NewThemedText(func() string { return tui.ActiveTheme().Fg(token, message) }, paddingX, 0)
}
