package codingagent

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

func (m *InteractiveMode) appendToChat(comp tui.Component) {
	m.chatContainer.Add(comp)
}

func (m *InteractiveMode) newUserMessageBlock(text string) *tui.UserMessageBlock {
	block := tui.NewUserMessageBlock(text)
	block.SetOutputPad(m.outputPad)
	m.userBlocks = append(m.userBlocks, block)
	return block
}

func (m *InteractiveMode) newAssistantMessageBlock() *tui.AssistantMessageBlock {
	block := tui.NewAssistantMessageBlock(m.hideThinking)
	block.SetOutputPad(m.outputPad)
	block.SetMarkdownTransform(m.assistantMarkdownTransform(block, extension.MarkdownMessageAssistant))
	block.SetThinkingMarkdownTransform(m.assistantMarkdownTransform(block, extension.MarkdownMessageAssistantThinking))
	block.SetMarkdownTransformState(m.assistantMarkdownTransformState(block))
	return block
}

// updateAssistantMessageBlock applies the authoritative content snapshot and terminal state on both live events and session redraws. Tool calls are invisible boundaries between thinking runs.
func updateAssistantMessageBlock(block *tui.AssistantMessageBlock, message *agent.AssistantMessage) {
	segments := make([]tui.AssistantSegment, 0, len(message.Content))
	hasToolCalls := false
	for _, content := range message.Content {
		switch c := content.(type) {
		case ai.TextContent:
			segments = append(segments, tui.AssistantSegment{Text: c.Text})
		case ai.ThinkingContent:
			segments = append(segments, tui.AssistantSegment{Thinking: true, Text: c.Thinking})
		case ai.ToolCall:
			hasToolCalls = true
			segments = append(segments, tui.AssistantSegment{})
		}
	}
	block.SetContent(segments)
	block.SetHasToolCalls(hasToolCalls)
	block.SetTerminalError(string(message.StopReason), message.ErrorMessage)
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
// Extension-registered Markdown transformers are not appended here: the
// subprocess RegisterMarkdownTransformer path (protocol + SDKs) is unported, so
// only the built-in transformer runs, exactly as upstream prepends it.
func (m *InteractiveMode) assistantMarkdownTransform(block *tui.AssistantMessageBlock, messageType extension.MarkdownMessageType) func(string, int) string {
	return func(markdown string, width int) string {
		streaming := m.evCurrentBlock == block && m.hasActiveAgentTurn()
		transformers := []extension.MarkdownTransformer{
			createMermaidMarkdownTransformer(m.mermaidRenderingMode, tui.ActiveTheme()),
		}
		return createMarkdownTransform(messageType, streaming, transformers)(markdown, width)
	}
}

// assistantMarkdownTransformState fingerprints the live state
// assistantMarkdownTransform reads, for the Markdown render cache key.
func (m *InteractiveMode) assistantMarkdownTransformState(block *tui.AssistantMessageBlock) func() string {
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
		return m.opts.SettingsManager.GetMermaidRenderingMode()
	}
	return "streaming"
}

// appendChatBlock appends a standalone text/markdown block preceded by a
// blank spacer line. Use this for error messages, status messages, and
// login flow messages: any content that is NOT an AssistantMessageBlock
// (which handles its own leading spacer). Mirrors upstream's pattern of
// inserting new Spacer(1) before standalone chat additions.
func (m *InteractiveMode) appendChatBlock(comp tui.Component) {
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(comp)
}

// binaryUpdateNoticeBody builds the heading+instruction line for the
// self-update notification, mirroring upstream showNewVersionNotification:
// bold-warning "Update Available", newline, muted instruction with the accent
// update command.
func binaryUpdateNoticeBody(t *tui.Theme, latestVersion, command string) string {
	const bold, reset = "\x1b[1m", "\x1b[0m"
	return bold + t.Warning + "Update Available" + reset +
		"\n" + t.Muted + fmt.Sprintf("New version %s is available. Run ", latestVersion) + reset + t.Accent + command + reset
}

// packageUpdateNoticeBody builds the body text for the package-update
// notification, mirroring upstream showPackageUpdateNotification: bold-warning
// heading, muted instruction with the accent command, a muted "Packages:"
// label, then one "- <name>" line per package.
func packageUpdateNoticeBody(t *tui.Theme, packages []string) string {
	const bold, reset = "\x1b[1m", "\x1b[0m"
	var body strings.Builder
	body.WriteString(bold + t.Warning + "Package Updates Available" + reset +
		"\n" + t.Muted + "Package updates are available. Run " + reset + t.Accent + "pig update --extensions" + reset +
		"\n" + t.Muted + "Packages:" + reset)
	for _, name := range packages {
		body.WriteString("\n- " + name)
	}
	return body.String()
}

// finishPackageUpdateCheck shows the startup package update notice. On Windows
// it then restores pig's title, because npm can overwrite the shared console
// title while it checks package versions. Mirrors upstream run(), which
// restores the title in the check's finally on win32.
func (m *InteractiveMode) finishPackageUpdateCheck(goos string, updates []string) {
	if len(updates) > 0 {
		m.appendBorderedNotice(tui.NewText(packageUpdateNoticeBody(tui.ActiveTheme(), updates)))
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
	warning := tui.ActiveTheme().Warning
	m.chatContainer.Add(tui.NewSpacer(1))
	m.chatContainer.Add(tui.NewDynamicBorder(warning))
	for _, b := range blocks {
		m.chatContainer.Add(b)
	}
	m.chatContainer.Add(tui.NewDynamicBorder(warning))
	m.tuiInst.Render()
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
	text := m.lastAssistantText
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

// showError appends an error line to the chat. Mirrors upstream showError:
// "Error: <message>" in the theme's error color.
func (m *InteractiveMode) showError(msg string) {
	if m.chatContainer == nil {
		return
	}
	m.appendChatBlock(tui.NewPaddedText(tui.ActiveTheme().FgText("error", "Error: "+msg), m.outputPad, 0, nil))
	if m.tuiInst != nil {
		m.tuiInst.Render()
	}
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
	theme := tui.ActiveTheme()
	message, color := status.Message, theme.Dim
	if status.Type == "warning" {
		message, color = "Warning: "+status.Message, theme.Warning
	}
	m.chatContainer.Add(tui.NewPaddedText(themeFg(color, message), 1, 0, nil))
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
	status := tui.ActiveTheme().FgText("dim", msg)
	secondLast, last := m.chatContainer.LastTwoChildren()
	if last != nil && secondLast != nil && last == m.lastStatusText && secondLast == m.lastStatusSpacer {
		m.lastStatusText.SetText(status)
	} else {
		spacer := tui.NewSpacer(1)
		text := tui.NewPaddedText(status, 1, 0, nil)
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
