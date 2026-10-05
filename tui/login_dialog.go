package tui

// Ports packages/coding-agent/src/modes/interactive/components/login-dialog.ts

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

const secretInputSuffixCharacters = 4 // pig divergence (D80): reveal only the final four characters of inputs longer than four.
const secretInputMaxDots = 8          // pig divergence (D80): bound the preview independently of input length; the counter reports the full length.
const secretInputHint = "Input hidden (PiG default). Show like Pi: /settings → Mask secret input"

// SecretInputPreview returns a bounded masked preview and the number of user-perceived characters. Inputs shorter than five characters reveal no suffix.
func SecretInputPreview(value string) (string, int) {
	segments := graphemeSegments(value)
	count := len(segments)
	hidden, suffix := count, ""
	if count > secretInputSuffixCharacters {
		hidden -= secretInputSuffixCharacters
		suffix = value[segments[hidden].Start:]
	}
	return strings.Repeat("•", min(hidden, secretInputMaxDots)) + suffix, count
}

// RedactSecretInput removes a submitted input and its trimmed credential form from an authentication diagnostic. It does not modify credentials or persist the input.
func RedactSecretInput(text, value string) string {
	for _, candidate := range []string{value, strings.TrimSpace(value)} {
		if candidate != "" {
			masked, _ := SecretInputPreview(candidate)
			text = strings.ReplaceAll(text, candidate, masked)
		}
	}
	return text
}

// LoginDialog renders provider authentication in the editor slot. The caller owns I/O and feeds prompt, progress and completion events.
type LoginDialog struct {
	invalidatable
	mu              sync.Mutex
	title           string
	lines           []string
	input           *TextInput
	inputIndex      int
	inputActive     bool
	inputMasked     bool
	maskSecretInput bool
	secrets         []string
	inputCh         chan string
	done            bool
	cancelled       bool
	onCancel        func()
}

// NewLoginDialog creates a provider dialog with PiG's configurable input privacy default enabled.
func NewLoginDialog(providerName string, onCancel func(), titleOverride ...string) *LoginDialog {
	// pig divergence (D80): callers can select Pi's plain-text behavior before prompting.
	dialog := &LoginDialog{title: "Login to " + providerName, onCancel: onCancel, maskSecretInput: true, inputIndex: -1}
	if len(titleOverride) > 0 {
		dialog.title = titleOverride[0]
	}
	return dialog
}

// SetMaskSecretInput selects masking for subsequent secret prompts. Already masked history stays masked.
func (d *LoginDialog) SetMaskSecretInput(enabled bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maskSecretInput = enabled
}

func loginKeyHint(action TUIKeybinding, description string) string {
	key := FormatKeyText(strings.Join(GetTUIKeybindings().GetKeys(action), "/"), false)
	return ActiveTheme().FgText("dim", key) + ActiveTheme().FgText("muted", " "+description)
}

// ShowAuth replaces the dialog content with a URL and optional instructions.
func (d *LoginDialog) ShowAuth(url, instructions string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = linkedURLLines(url)
	d.inputIndex = -1
	if instructions != "" {
		d.lines = append(d.lines, "", " "+ActiveTheme().FgText("warning", d.redactLocked(instructions)))
	}
	d.Invalidate()
}

// ShowDeviceCode replaces the dialog content with a verification URL and the user code, as Pi's showDeviceCode does.
func (d *LoginDialog) ShowDeviceCode(verificationURI, userCode string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = append(linkedURLLines(verificationURI), "", " "+ActiveTheme().FgText("warning", "Enter code: "+userCode))
	d.inputIndex = -1
	d.Invalidate()
}

// linkedURLLines renders a spacer, the hyperlinked URL and the platform's click hint.
func linkedURLLines(url string) []string {
	t := ActiveTheme()
	linked := func(label string) string { return "\x1b]8;;" + url + "\x07" + label + "\x1b]8;;\x07" }
	hint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		hint = "Cmd+click to open"
	}
	return []string{"", " " + t.FgText("accent", linked(url)), " " + t.FgText("dim", linked(hint))}
}

// ShowDetails replaces the content with informational lines before a provider prompt.
func (d *LoginDialog) ShowDetails(lines []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = []string{""}
	d.inputIndex = -1
	for _, line := range lines {
		d.lines = append(d.lines, " "+line)
	}
	d.Invalidate()
}

// AuthInfoLink is a provider-owned documentation link shown in a login dialog.
type AuthInfoLink struct{ URL, Label string }

// ShowInfo appends provider instructions and links before the next prompt.
func (d *LoginDialog) ShowInfo(message string, links []AuthInfoLink, showCloseHint bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := ActiveTheme()
	d.lines = append(d.lines, "", " "+t.FgText("text", d.redactLocked(message)))
	for _, link := range links {
		text := link.URL
		if link.Label != "" {
			text = link.Label + ": " + link.URL
		}
		d.lines = append(d.lines, " "+t.FgText("accent", "\x1b]8;;"+link.URL+"\x07"+text+"\x1b]8;;\x07"))
	}
	if showCloseHint {
		d.lines = append(d.lines, "", " ("+loginKeyHint(KBSelectCancel, "to close")+")")
	}
	d.Invalidate()
}

// ShowWaiting appends a waiting message and cancellation hint.
func (d *LoginDialog) ShowWaiting(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = append(d.lines, "", " "+ActiveTheme().FgText("dim", d.redactLocked(msg)), " ("+loginKeyHint(KBSelectCancel, "to cancel")+")")
	d.Invalidate()
}

// ShowProgress appends an authentication diagnostic, redacting masked prompt values.
func (d *LoginDialog) ShowProgress(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = append(d.lines, " "+ActiveTheme().FgText("dim", d.redactLocked(msg)))
	d.Invalidate()
}

// Redact removes masked prompt values from authentication errors emitted after the dialog closes.
func (d *LoginDialog) Redact(text string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.redactLocked(text)
}
func (d *LoginDialog) redactLocked(text string) string {
	values := slices.Clone(d.secrets)
	if d.inputActive && d.inputMasked && d.input != nil {
		values = append(values, d.input.Text())
	}
	slices.SortFunc(values, func(a, b string) int { return len(b) - len(a) })
	for _, value := range values {
		text = RedactSecretInput(text, value)
	}
	return text
}

// ShowInput appends a text prompt. Its returned channel delivers the original submitted value and closes on cancellation.
func (d *LoginDialog) ShowInput(prompt, placeholder string) <-chan string {
	return d.showInput(prompt, placeholder, false, false)
}

// ShowManualInput appends a dim callback-code prompt with a cancel-only hint. Its returned channel delivers the submitted value and closes on cancellation.
func (d *LoginDialog) ShowManualInput(prompt string) <-chan string {
	return d.showInput(prompt, "", false, true)
}

// ShowSecretInput honors the configured privacy setting; false uses Pi's ordinary prompt and submitted-text rendering.
func (d *LoginDialog) ShowSecretInput(prompt, placeholder string) <-chan string {
	return d.showInput(prompt, placeholder, true, false)
}

func (d *LoginDialog) showInput(prompt, placeholder string, secret, manual bool) <-chan string {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := ActiveTheme()
	d.inputMasked = secret && d.maskSecretInput
	color := "text"
	hint := loginKeyHint(KBSelectCancel, "to cancel,") + " " + loginKeyHint(KBSelectConfirm, "to submit")
	if manual {
		color = "dim"
		hint = loginKeyHint(KBSelectCancel, "to cancel")
	}
	d.lines = append(d.lines, "", " "+t.FgText(color, d.redactLocked(prompt)))
	if placeholder != "" {
		d.lines = append(d.lines, " "+t.FgText("dim", "e.g., "+d.redactLocked(placeholder)))
	}
	// pig divergence (D80): the hint explains both the selected default and its Pi-compatible opt-out.
	if d.inputMasked {
		d.lines = append(d.lines, " "+t.FgText("dim", secretInputHint))
	}
	d.inputIndex = len(d.lines)
	d.lines = append(d.lines, "", " ("+hint+")")
	d.inputActive = true
	d.inputCh = make(chan string, 1)
	d.input = NewInput(InputOptions{})
	// The host mounts the active prompt in the editor slot.
	d.input.Focused = true
	d.input.OnSubmit = func(value string) {
		submitted := value
		// pig divergence (D80): only the preview is retained in dialog history when masking is enabled.
		if d.inputMasked {
			submitted, _ = SecretInputPreview(value)
			if value != "" {
				d.secrets = append(d.secrets, value)
			}
		}
		if d.inputIndex >= 0 {
			d.lines[d.inputIndex] = "> " + submitted
		}
		if d.inputCh != nil {
			d.inputCh <- value
			close(d.inputCh)
			d.inputCh = nil
		}
		d.inputActive = false
		d.input = nil
	}
	d.Invalidate()
	return d.inputCh
}

// HandleInput routes editing to the active prompt and cancels with the configured selector binding.
func (d *LoginDialog) HandleInput(data string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.done {
		return
	}
	if GetTUIKeybindings().Matches(data, KBSelectCancel) {
		d.done = true
		d.cancelled = true
		// Pi's cancel aborts the login's signal before it rejects the pending input (login-dialog.ts:86-93), so a prompt
		// that sees its input end already finds the login cancelled.
		if d.onCancel != nil {
			d.onCancel()
		}
		if d.inputCh != nil {
			close(d.inputCh)
			d.inputCh = nil
		}
		return
	}
	if !d.inputActive {
		return
	}
	d.input.HandleInput(data)
	d.Invalidate()
}

// Render returns Pi's dialog layout, adding the preview, count and hint only for masked prompts.
func (d *LoginDialog) Render(width int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := ActiveTheme()
	border := NewDynamicBorder("")
	out := border.Render(width)
	out = append(out, NewPaddedText(t.FgText("accent", "\x1b[1m"+d.title+SGRBoldDimReset), 1, 0, nil).Render(width)...)
	for i, line := range d.lines {
		if d.inputActive && i == d.inputIndex {
			input := d.input
			// pig divergence (D80): a bounded preview shows the suffix and count, never the full secret.
			if d.inputMasked {
				preview, count := SecretInputPreview(d.input.Text())
				unit := "characters"
				if count == 1 {
					unit = "character"
				}
				input = NewInput(InputOptions{})
				input.SetText(fmt.Sprintf("%s (%d %s)", preview, count, unit))
				input.cursor = jsstring.Length(preview)
				input.Focused = d.input.Focused
			}
			out = append(out, input.Render(width)...)
		} else {
			out = append(out, wrapWithIndent(d.redactLocked(line), width)...)
		}
	}
	return append(out, border.Render(width)...)
}

func (d *LoginDialog) Done() bool      { d.mu.Lock(); defer d.mu.Unlock(); return d.done }
func (d *LoginDialog) Cancelled() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.cancelled }

// Success marks the provider operation complete; the caller restores the editor.
func (d *LoginDialog) Success() { d.mu.Lock(); defer d.mu.Unlock(); d.done = true; d.Invalidate() }

var _ Component = (*LoginDialog)(nil)

func wrapWithIndent(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	body := strings.TrimLeft(line, " ")
	indent := len(line) - len(body)
	if body == "" {
		return []string{""}
	}
	return NewPaddedText(body, indent, 0, nil).Render(width)
}
