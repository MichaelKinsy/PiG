package tui

// Ports packages/coding-agent/src/modes/interactive/components/login-dialog.ts

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// webURL is login-dialog.ts webUrl: the normalized href of the http: or https: URL value names, and false for anything else. The shared launcher also opens
// files and custom protocols, so login only opens web URLs; normalizing percent-encodes control characters, which would otherwise end a terminal hyperlink early.
func webURL(value string) (string, bool) {
	parsed, err := nodeurl.ParseHTTPHref(value)
	if err != nil {
		return "", false
	}
	return parsed.Href, true
}

// displayURL is login-dialog.ts displayUrl: value normalized as a web URL, else with control characters replaced, since an untrusted string printed as it is
// could end a hyperlink early or send its own escape sequences to the terminal.
func displayURL(value string) string {
	if href, ok := webURL(value); ok {
		return href
	}
	return widthx.ReplaceControlCharacters(value)
}

// webLink is login-dialog.ts webLink: text as a terminal hyperlink to url when that is a web URL, else plain text.
func webLink(text, url string) string {
	target, ok := webURL(url)
	if !ok {
		return text
	}
	return Hyperlink(text, target)
}

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

// LoginDialogComponent renders provider authentication in the editor slot. The caller owns I/O and feeds prompt, progress and completion events.
type LoginDialogComponent struct {
	Container
	mu    sync.Mutex
	title string
	// content is upstream's contentContainer: the dynamic area between the title and the bottom border.
	content *Container
	input   *TextInput
	// inputSlots are the content children that show the dialog's input; a prompt shown again before the first is answered adds a second slot for the same input (login-dialog.ts adds this.input to the container each time).
	inputSlots      []*loginDialogInput
	inputActive     bool
	inputMasked     bool
	maskSecretInput bool
	secrets         []string
	inputCh         chan string
	done            bool
	cancelled       bool
	focused         bool
	onComplete      func(success bool, message string)
	// authURL is the shown sign-in URL, which `app.message.copy` copies.
	authURL       *AuthURL
	copyText      func(text string) error
	openBrowser   func(url string)
	requestRender func()
	// deviceCodeAction is the pending device-code confirmation: set while the dialog shows a device code that offers a
	// browser or clipboard action, and consumed by the first confirm key. deviceCodeHint is the row that shows the
	// offer and then the progress (login-dialog.ts deviceCodeAction and deviceCodeHint).
	deviceCodeAction *deviceCodeAction
	deviceCodeHint   *loginDialogDeviceHint
}

type deviceCodeAction struct {
	info OAuthDeviceCodeInfo
	hint *loginDialogDeviceHint
}

// SetOpenBrowser supplies the launcher a confirmed device-code login opens its verification page with. A nil launcher
// opens nothing.
func (d *LoginDialogComponent) SetOpenBrowser(openBrowser func(url string)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.openBrowser = openBrowser
}

// SetCopyToClipboard supplies the clipboard writer `app.message.copy` uses for a sign-in URL and the repaint request
// that shows the outcome; a nil requestRender keeps the constructor tui's.
func (d *LoginDialogComponent) SetCopyToClipboard(copyText func(text string) error, requestRender func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.copyText = copyText
	if requestRender != nil {
		d.requestRender = requestRender
	}
}

// NewLoginDialogComponent creates a provider dialog, as Pi's constructor does (login-dialog.ts:32-48), with PiG's configurable
// input privacy default enabled. onComplete runs with (false, "Login cancelled") when the user cancels. The title is "Login to
// <providerNameOverride or providerID>" unless titleOverride (the first value) is given; an empty providerNameOverride stands
// for Pi's undefined. A non-nil ui receives the dialog's render requests; SetCopyToClipboard may replace that request.
func NewLoginDialogComponent(ui TUI, providerID string, onComplete func(success bool, message string), providerNameOverride string, titleOverride ...string) *LoginDialogComponent {
	providerName := providerNameOverride
	if providerName == "" {
		providerName = providerID
	}
	// pig divergence (D80): callers can select Pi's plain-text behavior before prompting.
	dialog := &LoginDialogComponent{title: "Login to " + providerName, onComplete: onComplete, maskSecretInput: true, content: NewContainer()}
	if ui != nil {
		dialog.requestRender = func() { ui.RequestRender() }
	}
	if len(titleOverride) > 0 {
		dialog.title = titleOverride[0]
	}
	// upstream: login-dialog.ts constructor children.
	dialog.Add(NewDynamicBorder())
	dialog.Add(NewPaddedText(ActiveTheme().Fg("accent", "\x1b[1m"+dialog.title+SGRBoldDimReset), 1, 0, nil))
	dialog.Add(dialog.content)
	dialog.Add(NewDynamicBorder())
	return dialog
}

// SetFocused implements Focusable: the focus flag propagates to the active prompt input so the hardware cursor lands in it
// (upstream's focused setter assigns input.focused, login-dialog.ts:25-28).
func (d *LoginDialogComponent) SetFocused(focused bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.focused = focused
	if d.input != nil {
		d.input.SetFocused(focused)
	}
}

// Focused reports the Focusable flag (upstream's focused getter, login-dialog.ts:23-24).
func (d *LoginDialogComponent) Focused() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.focused
}

// SetMaskSecretInput selects masking for subsequent secret prompts. Already masked history stays masked.
func (d *LoginDialogComponent) SetMaskSecretInput(enabled bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maskSecretInput = enabled
}

func loginKeyHint(action TUIKeybinding, description string) string {
	key := FormatKeyText(strings.Join(GetTUIKeybindings().GetKeys(action), "/"), false)
	return ActiveTheme().Fg("dim", key) + ActiveTheme().Fg("muted", " "+description)
}

// ShowAuth replaces the dialog content with a URL and optional instructions. It returns the URL the caller opens in the browser, which is the normalized
// href of a web URL and empty for any other URL (login-dialog.ts showAuth opens only that target).
func (d *LoginDialogComponent) ShowAuth(url, instructions string) (browserTarget string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clearDeviceCodeActionLocked()
	target, isWeb := webURL(url)
	shown := target
	if !isWeb {
		shown = widthx.ReplaceControlCharacters(url)
	}
	d.authURL = NewAuthURL(shown, d.copyText, func() {
		d.Invalidate()
		if d.requestRender != nil {
			d.requestRender()
		}
	})
	d.resetContent()
	d.addSpacer()
	d.content.Add(&loginDialogAuthURL{dialog: d, url: d.authURL})
	if instructions != "" {
		d.addSpacer()
		d.addLine(" " + ActiveTheme().Fg("warning", d.redactLocked(instructions)))
	}
	d.Invalidate()
	return target
}

// OAuthDeviceCodeInfo is the user code and verification URL a device-code login shows (Pi's OAuthDeviceCodeInfo, ai compat/extension-oauth-types.ts:17). OpenBrowserOnConfirm and CopyCodeOnConfirm
// are nil for Pi's undefined, which means true: a provider turns an action off with a pointer to false.
type OAuthDeviceCodeInfo struct {
	UserCode             string
	VerificationURI      string
	OpenBrowserOnConfirm *bool
	CopyCodeOnConfirm    *bool
	IntervalSeconds      float64
	ExpiresInSeconds     float64
}

// ShowDeviceCode replaces the dialog content with a verification URL and the user code, as Pi's showDeviceCode(info) does (login-dialog.ts:117).
// Unless the provider opted out, it also offers to open the browser and copy the code on the confirm key.
func (d *LoginDialogComponent) ShowDeviceCode(info OAuthDeviceCodeInfo) {
	verificationURI, userCode := info.VerificationURI, info.UserCode
	d.mu.Lock()
	defer d.mu.Unlock()
	d.authURL = nil
	openBrowser, copyCode := info.OpenBrowserOnConfirm == nil || *info.OpenBrowserOnConfirm, info.CopyCodeOnConfirm == nil || *info.CopyCodeOnConfirm
	info.OpenBrowserOnConfirm, info.CopyCodeOnConfirm = &openBrowser, &copyCode
	d.clearDeviceCodeActionLocked()
	d.resetContent()
	d.addSpacer()
	for _, line := range linkedURLLines(verificationURI) {
		d.addLine(line)
	}
	d.addSpacer()
	d.addLine(" " + ActiveTheme().Fg("warning", "Enter code: "+userCode))
	if !d.cancelled && (openBrowser || copyCode) {
		label := "to copy code"
		switch {
		case openBrowser && copyCode:
			label = "to open browser and copy code"
		case openBrowser:
			label = "to open browser"
		}
		// This single row shows the progress and result text too, including in narrow terminals.
		hint := &loginDialogDeviceHint{row: NewTruncatedText("("+loginKeyHint(KBSelectConfirm, label)+")", 1, 0)}
		d.content.Add(hint)
		d.deviceCodeHint = hint
		d.deviceCodeAction = &deviceCodeAction{info: info, hint: hint}
	}
	d.Invalidate()
}

// loginDialogDeviceHint is the device-code offer row; its single child is replaced by progress and result text.
type loginDialogDeviceHint struct{ row *TruncatedText }

func (h *loginDialogDeviceHint) Render(width int) []string { return h.row.Render(width) }
func (*loginDialogDeviceHint) Invalidate()                 {}

// clearDeviceCodeActionLocked drops a pending device-code offer and its row. Every step that replaces or extends the
// content calls it, so a confirm key after one acts on nothing.
func (d *LoginDialogComponent) clearDeviceCodeActionLocked() {
	if d.deviceCodeHint != nil {
		d.content.Remove(d.deviceCodeHint)
	}
	d.deviceCodeHint = nil
	d.deviceCodeAction = nil
}

func (d *LoginDialogComponent) setDeviceCodeStatusLocked(hint *loginDialogDeviceHint, message string) {
	if d.cancelled || !d.focused || d.deviceCodeHint != hint {
		return
	}
	hint.row = NewTruncatedText(ActiveTheme().Fg("dim", message), 1, 0)
	d.Invalidate()
}

// activateDeviceCodeLocked is activateDeviceCode: it copies the code without delaying the browser launch or the login's
// polling, and opens only web URLs, since the shared launcher also opens files.
func (d *LoginDialogComponent) activateDeviceCodeLocked(info OAuthDeviceCodeInfo, hint *loginDialogDeviceHint) {
	copyCode := info.CopyCodeOnConfirm != nil && *info.CopyCodeOnConfirm
	if copyCode {
		d.setDeviceCodeStatusLocked(hint, "Copying code...")
		copyText := d.copyText
		//portlint:allow golifetime the goroutine ends when the clipboard writer returns; it only takes the dialog lock and requests a repaint
		go func() {
			message := "Code copied to clipboard"
			if copyText == nil || copyText(info.UserCode) != nil {
				message = "Copy code manually"
			}
			d.mu.Lock()
			d.setDeviceCodeStatusLocked(hint, message)
			requestRender := d.requestRender
			d.mu.Unlock()
			// The repaint request stays outside the lock: a render takes it.
			if requestRender != nil {
				requestRender()
			}
		}()
	} else {
		d.setDeviceCodeStatusLocked(hint, "Browser open requested")
	}
	if info.OpenBrowserOnConfirm != nil && *info.OpenBrowserOnConfirm && d.openBrowser != nil {
		if target, ok := webURL(info.VerificationURI); ok {
			d.openBrowser(target)
		}
	}
}

// linkedURLLines renders the hyperlinked URL and the platform's click hint.
func linkedURLLines(verificationURI string) []string {
	t := ActiveTheme()
	shown := displayURL(verificationURI)
	hint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		hint = "Cmd+click to open"
	}
	return []string{" " + t.Fg("accent", webLink(shown, shown)), " " + t.Fg("dim", webLink(hint, verificationURI))}
}

// ShowDetails replaces the content with informational lines before a provider prompt.
func (d *LoginDialogComponent) ShowDetails(lines []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.authURL = nil
	d.clearDeviceCodeActionLocked()
	d.resetContent()
	d.addSpacer()
	for _, line := range lines {
		d.addLine(" " + line)
	}
	d.Invalidate()
}

// AuthInfoLink is a provider-owned documentation link shown in a login dialog.
type AuthInfoLink struct{ URL, Label string }

// ShowInfo appends provider instructions and links before the next prompt.
func (d *LoginDialogComponent) ShowInfo(message string, links []AuthInfoLink, showCloseHint bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := ActiveTheme()
	d.addSpacer()
	d.addLine(" " + t.Fg("text", d.redactLocked(message)))
	for _, link := range links {
		shown := displayURL(link.URL)
		text := shown
		if link.Label != "" {
			text = link.Label + ": " + shown
		}
		d.addLine(" " + t.Fg("accent", webLink(text, shown)))
	}
	if showCloseHint {
		d.addSpacer()
		d.addLine(" (" + loginKeyHint(KBSelectCancel, "to close") + ")")
	}
	d.Invalidate()
}

// ShowWaiting appends a waiting message and cancellation hint.
func (d *LoginDialogComponent) ShowWaiting(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addSpacer()
	d.addLine(" " + ActiveTheme().Fg("dim", d.redactLocked(msg)))
	d.addLine(" (" + loginKeyHint(KBSelectCancel, "to cancel") + ")")
	d.Invalidate()
}

// ShowProgress appends an authentication diagnostic, redacting masked prompt values.
func (d *LoginDialogComponent) ShowProgress(msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addLine(" " + ActiveTheme().Fg("dim", d.redactLocked(msg)))
	d.Invalidate()
}

// Redact removes masked prompt values from authentication errors emitted after the dialog closes.
func (d *LoginDialogComponent) Redact(text string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.redactLocked(text)
}
func (d *LoginDialogComponent) redactLocked(text string) string {
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

// ShowPrompt appends a text prompt to the existing content (login-dialog.ts:150-176 showPrompt). Its returned channel delivers the original submitted value and closes on cancellation.
func (d *LoginDialogComponent) ShowPrompt(prompt, placeholder string) <-chan string {
	return d.showInput(prompt, placeholder, false, false)
}

// ShowManualInput appends a dim callback-code prompt with a cancel-only hint. Its returned channel delivers the submitted value and closes on cancellation.
func (d *LoginDialogComponent) ShowManualInput(prompt string) <-chan string {
	return d.showInput(prompt, "", false, true)
}

// ShowSecretInput honors the configured privacy setting; false uses Pi's ordinary prompt and submitted-text rendering.
func (d *LoginDialogComponent) ShowSecretInput(prompt, placeholder string) <-chan string {
	return d.showInput(prompt, placeholder, true, false)
}

func (d *LoginDialogComponent) showInput(prompt, placeholder string, secret, manual bool) <-chan string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clearDeviceCodeActionLocked()
	t := ActiveTheme()
	d.inputMasked = secret && d.maskSecretInput
	color := "text"
	hint := loginKeyHint(KBSelectCancel, "to cancel,") + " " + loginKeyHint(KBSelectConfirm, "to submit")
	if manual {
		color = "dim"
		hint = loginKeyHint(KBSelectCancel, "to cancel")
	}
	d.addSpacer()
	d.addLine(" " + t.Fg(color, d.redactLocked(prompt)))
	if placeholder != "" {
		d.addLine(" " + t.Fg("dim", "e.g., "+d.redactLocked(placeholder)))
	}
	// pig divergence (D80): the hint explains both the selected default and its Pi-compatible opt-out.
	if d.inputMasked {
		d.addLine(" " + t.Fg("dim", secretInputHint))
	}
	slot := &loginDialogInput{dialog: d}
	d.inputSlots = append(d.inputSlots, slot)
	d.content.Add(slot)
	d.addLine(" (" + hint + ")")
	d.inputActive = true
	d.inputCh = make(chan string, 1)
	d.input = NewInput(InputOptions{})
	d.input.Focused = d.focused
	d.input.OnSubmit = func(value string) {
		// login-dialog.ts onSubmit acts only while an input resolver is pending, so a submit after cancel changes nothing.
		if d.inputCh == nil {
			return
		}
		submitted := value
		// pig divergence (D80): only the preview is retained in dialog history when masking is enabled.
		if d.inputMasked {
			submitted, _ = SecretInputPreview(value)
			if value != "" {
				d.secrets = append(d.secrets, value)
			}
		}
		// upstream: replaceInputWithSubmittedText replaces every child that is the input.
		for _, slot := range d.inputSlots {
			d.content.Replace(slot, &loginDialogLine{dialog: d, line: "> " + submitted})
		}
		d.inputSlots = nil
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
func (d *LoginDialogComponent) HandleInput(data string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if GetTUIKeybindings().Matches(data, KBSelectCancel) {
		d.clearDeviceCodeActionLocked()
		d.done = true
		d.cancelled = true
		// Pi's cancel aborts the login's signal before it rejects the pending input (login-dialog.ts:86-93), so a prompt
		// that sees its input end already finds the login cancelled.
		if d.onComplete != nil {
			d.onComplete(false, "Login cancelled")
		}
		if d.inputCh != nil {
			close(d.inputCh)
			d.inputCh = nil
		}
		return
	}
	if d.authURL != nil && GetTUIKeybindings().Matches(data, "app.message.copy") {
		d.authURL.Copy()
		return
	}
	if d.deviceCodeAction != nil && d.inputCh == nil && !d.cancelled && d.focused && GetTUIKeybindings().Matches(data, KBSelectConfirm) {
		action := d.deviceCodeAction
		// Consume once, so a repeated confirm cannot launch several browsers or clipboard writes.
		d.deviceCodeAction = nil
		d.activateDeviceCodeLocked(action.info, action.hint)
		d.Invalidate()
		return
	}
	if !d.inputActive {
		return
	}
	d.input.HandleInput(data)
	d.Invalidate()
}

// Render returns Pi's dialog layout: border, title, the content container, border. The content children read the dialog's redaction state, so the dialog's lock covers the whole render.
func (d *LoginDialogComponent) Render(width int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Container.Render(width)
}

// resetContent is upstream's contentContainer.clear() before a step that replaces the dialog content.
func (d *LoginDialogComponent) resetContent() {
	d.content.Clear()
	d.inputSlots = nil
}

func (d *LoginDialogComponent) addSpacer() { d.content.Add(NewSpacer(1)) }

// addLine appends a Text(line, indent, 0) whose rendered text has masked prompt values redacted at render time.
func (d *LoginDialogComponent) addLine(line string) {
	d.content.Add(&loginDialogLine{dialog: d, line: line})
}

// loginDialogLine is a Text child of the content container. It redacts every submitted secret when it renders, so a secret entered after the line was shown still disappears from it. The caller holds the dialog's lock.
type loginDialogLine struct {
	dialog *LoginDialogComponent
	line   string
}

func (l *loginDialogLine) Render(width int) []string {
	return wrapWithIndent(l.dialog.redactLocked(l.line), width)
}
func (*loginDialogLine) Invalidate() {}

// loginDialogAuthURL shows the sign-in URL and its copy hint, redacted like every other line. The hint changes after a copy, so it reads the current text on each render.
type loginDialogAuthURL struct {
	dialog *LoginDialogComponent
	url    *AuthURL
}

func (a *loginDialogAuthURL) Render(width int) []string {
	var out []string
	for _, line := range a.url.Lines() {
		out = append(out, wrapWithIndent(a.dialog.redactLocked(line), width)...)
	}
	return out
}
func (*loginDialogAuthURL) Invalidate() {}

// loginDialogInput is an input child of the content container: every slot renders the one input, until a submit replaces the slots with the submitted text.
type loginDialogInput struct{ dialog *LoginDialogComponent }

func (i *loginDialogInput) Render(width int) []string {
	d := i.dialog
	if d.input == nil {
		return []string{""}
	}
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
	return input.Render(width)
}
func (*loginDialogInput) Invalidate() {}

func (d *LoginDialogComponent) Done() bool      { d.mu.Lock(); defer d.mu.Unlock(); return d.done }
func (d *LoginDialogComponent) Cancelled() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.cancelled }

// Success marks the provider operation complete; the caller restores the editor.
func (d *LoginDialogComponent) Success() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.done = true
	d.Invalidate()
}

var _ Component = (*LoginDialogComponent)(nil)

func wrapWithIndent(line string, width int) []string {
	body := strings.TrimLeft(line, " ")
	indent := len(line) - len(body)
	if body == "" {
		// Text renders no row for an empty or blank string (text.ts render), so the line adds nothing.
		return nil
	}
	return NewPaddedText(body, indent, 0, nil).Render(width)
}
