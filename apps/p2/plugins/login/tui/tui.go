// Package tui is `/login` and `/logout` in the TUI (p2-tui-port.md P6), after coding-agent's oauth-selector and login-dialog on `ProviderLogin` (owner only).
// Differences from pi, for a remote, sandboxed TUI: no browser is opened and nothing is copied by this worker. Opening the sign-in URL and copying it are launcher
// actions (`openUrl` external, `copy`), each confirmed in the launcher's own screen; secret prompts are masked and never echoed, kept in history, the reload
// snapshot or status lines.
//
// Ports apps/p2/plugins/login/tui.ts.
package tui

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/login/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type loginMethod struct{ typ, label string }

type provider struct {
	id, name, signedIn string
	loginMethods       []loginMethod
}

func providersOf(value any) []provider {
	var out []provider
	for _, each := range p2tui.JList(value) {
		p := provider{id: p2tui.JStr(p2tui.JField(each, "id")), name: p2tui.JStr(p2tui.JField(each, "name")), signedIn: p2tui.JStr(p2tui.JField(each, "signedIn"))}
		for _, method := range p2tui.JList(p2tui.JField(each, "loginMethods")) {
			p.loginMethods = append(p.loginMethods, loginMethod{p2tui.JStr(p2tui.JField(method, "type")), p2tui.JStr(p2tui.JField(method, "label"))})
		}
		out = append(out, p)
	}
	return out
}

const manualCodeHelp = "Complete sign-in in your browser, then paste the authorization code or the final redirect URL here. If you land on an unreachable localhost page, copy its full address; that page does not need to load."

// noticeURL is the URL a notice offers (sign-in page or device verification page), when it is https or http.
func noticeURL(event any) string {
	var raw string
	switch p2tui.JStr(p2tui.JField(event, "type")) {
	case "auth_url":
		raw = p2tui.JStr(p2tui.JField(event, "url"))
	case "device_code":
		raw = p2tui.JStr(p2tui.JField(event, "verificationUri"))
	}
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") {
		return raw
	}
	return ""
}

// opensInBrowser is whether the notice's page may be offered for opening: a provider may ask clients not to (`openBrowserOnConfirm`).
func opensInBrowser(event any) bool {
	return p2tui.JStr(p2tui.JField(event, "type")) != "device_code" || p2tui.JField(event, "openBrowserOnConfirm") != false
}

// LoginDialog is the login flow of one provider, rendered from the server's `LoginState` (pi's LoginDialogComponent). Its fields are the owner loop's.
type LoginDialog struct {
	focused    bool
	ctx        runtime.TuiContext
	ref        *host.Reference
	provider   provider
	onDone     func()
	state      any
	promptID   string
	hasPrompt  bool
	input      *pitui.TextInput
	secret     *components.SecretInput
	selectList *pitui.SelectList
	status     string
	stop       func()
	closed     bool
}

// NewLoginDialog is the dialog of provider over ref (a live owner reference); onDone closes its overlay.
func NewLoginDialog(ctx runtime.TuiContext, ref *host.Reference, p provider, onDone func()) *LoginDialog {
	d := &LoginDialog{ctx: ctx, ref: ref, provider: p, onDone: onDone, stop: func() {}}
	d.secret = components.NewSecretInput(runtime.Selectors(ctx))
	d.input = pitui.NewInput(pitui.InputOptions{})
	d.input.OnSubmit = d.answer
	d.secret.OnSubmit = d.answer
	if variant, _, _ := ref.Variant(); p2runtime.StatusOf(ref) == host.RefStateLive && variant == "owner" {
		d.stop = p2tui.FollowMember(ref.Member("login"), func(state delta.JsonValue) {
			ctx.Post(func() {
				if d.closed {
					return
				}
				d.state = state
				d.syncPrompt()
				ctx.RequestRender()
			})
		})
	}
	return d
}

// SetFocused records the focus.
func (d *LoginDialog) SetFocused(focused bool) {
	d.focused = focused
	d.input.SetFocused(focused)
	d.secret.SetFocused(focused)
}

func (d *LoginDialog) owner() *host.Reference {
	if variant, _, _ := d.ref.Variant(); variant == "owner" && p2runtime.StatusOf(d.ref) == host.RefStateLive {
		return d.ref
	}
	return nil
}

// current is the login state when it is this provider's, nil otherwise.
func (d *LoginDialog) current() any {
	// The live value, not the last one the loop saw: a key pressed right after the server's change must act on it.
	state := d.state
	if member := d.ref.Member("login"); member != nil && !d.closed {
		if value, err := member.Value(); err == nil {
			state = value
		}
	}
	if state != nil && p2tui.JStr(p2tui.JField(state, "providerId")) == d.provider.id {
		return state
	}
	return nil
}

// syncPrompt gives a new prompt fresh inputs; nothing typed for an earlier prompt survives.
func (d *LoginDialog) syncPrompt() {
	prompt := p2tui.JField(d.current(), "prompt")
	id := p2tui.JStr(p2tui.JField(prompt, "id"))
	if d.hasPrompt && id == d.promptID {
		return
	}
	d.promptID, d.hasPrompt = id, true
	d.input.SetValue("")
	d.secret.Clear()
	d.selectList = nil
	if p2tui.JStr(p2tui.JField(prompt, "type")) == "select" {
		var items []pitui.SelectItem
		for _, option := range p2tui.JList(p2tui.JField(prompt, "options")) {
			item := pitui.SelectItem{Value: p2tui.JStr(p2tui.JField(option, "id")), Label: render.LineText(p2tui.JStr(p2tui.JField(option, "label")))}
			if description := p2tui.JStr(p2tui.JField(option, "description")); description != "" {
				item.Description = render.LineText(description)
			}
			items = append(items, item)
		}
		d.selectList = pitui.NewSelectList(items, 8, d.ctx.Theme().SelectList)
		d.selectList.OnSelect = func(item pitui.SelectItem) { d.answer(item.Value) }
		d.selectList.OnCancel = d.cancel
	}
}

func (d *LoginDialog) answer(value string) {
	state, ref := d.current(), d.owner()
	prompt := p2tui.JField(state, "prompt")
	if state == nil || prompt == nil || ref == nil {
		return
	}
	secret := p2tui.JStr(p2tui.JField(prompt, "type")) == "secret"
	d.status = ""
	d.ctx.RequestRender()
	args := delta.JsonObjectOf("attemptId", p2tui.JStr(p2tui.JField(state, "attemptId")), "promptId", p2tui.JStr(p2tui.JField(prompt, "id")), "value", value)
	go func() {
		if _, err := ref.Call("answer", args, context.Background()); err != nil {
			d.ctx.Post(func() {
				// Never echo a secret, even inside a server error.
				message := err.Error()
				if secret && value != "" {
					message = strings.Join(strings.Split(message, value), "•••")
				}
				d.status = render.LineText(message)
				d.ctx.RequestRender()
			})
		}
	}()
	d.input.SetValue("")
	d.secret.Clear()
}

// latest is the latest notice offering a URL, and the URL.
func (d *LoginDialog) latest() (event any, address string) {
	notices := p2tui.JList(p2tui.JField(d.current(), "notices"))
	for index := len(notices) - 1; index >= 0; index-- {
		event := p2tui.JField(notices[index], "event")
		if address := noticeURL(event); address != "" {
			return event, address
		}
	}
	return nil, ""
}

// openURL: opening the page is the launcher's (`openUrl` external, https only; it shows the full URL first).
func (d *LoginDialog) openURL() {
	event, address := d.latest()
	if address == "" || !opensInBrowser(event) {
		return
	}
	go func() {
		result := d.ctx.Grant("openUrl", map[string]any{"kind": "external", "url": address})
		d.ctx.Post(func() {
			if !result.OK && result.Error != "denied" {
				if strings.HasPrefix(address, "https:") {
					d.status = "Not opened (" + result.Error + ")"
				} else {
					d.status = "Only https pages can be opened; copy the address instead"
				}
				d.ctx.RequestRender()
			}
		})
	}()
}

func (d *LoginDialog) copyURL() {
	if _, address := d.latest(); address != "" {
		go d.ctx.Grant("copy", map[string]any{"text": address, "label": "the sign-in address"})
	}
}

func (d *LoginDialog) cancel() {
	state, ref := d.current(), d.owner()
	if p2tui.JStr(p2tui.JField(state, "status")) == "working" && ref != nil {
		attempt := p2tui.JStr(p2tui.JField(state, "attemptId"))
		go func() { _, _ = ref.Call("cancel", delta.JsonObjectOf("attemptId", attempt), context.Background()) }()
	} else {
		d.Close()
	}
}

// Close ends an unfinished login and releases the hold.
func (d *LoginDialog) Close() {
	if d.closed {
		return
	}
	state, ref := d.current(), d.owner()
	d.closed = true
	d.stop()
	d.input.SetValue("")
	d.secret.Clear()
	if state != nil && ref != nil {
		attempt := p2tui.JStr(p2tui.JField(state, "attemptId"))
		working := p2tui.JStr(p2tui.JField(state, "status")) == "working"
		// A cancelled flow settles asynchronously; only a settled login can be dismissed.
		settled := make(chan struct{}, 1)
		stop := p2tui.FollowMember(ref.Member("login"), func(current delta.JsonValue) {
			if current == nil || p2tui.JStr(p2tui.JField(current, "attemptId")) != attempt || p2tui.JStr(p2tui.JField(current, "status")) != "working" {
				select {
				case settled <- struct{}{}:
				default:
				}
			}
		})
		go func() {
			defer ref.Release()
			defer stop()
			if working {
				_, _ = ref.Call("cancel", delta.JsonObjectOf("attemptId", attempt), context.Background())
			}
			select {
			case <-settled:
			case <-time.After(5 * time.Second):
			}
			_, _ = ref.Call("dismiss", delta.JsonObjectOf("attemptId", attempt), context.Background())
		}()
	} else {
		d.ref.Release()
	}
	d.onDone()
}

// HandleInput routes keys.
func (d *LoginDialog) HandleInput(data string) {
	keys := d.ctx.Keys()
	state := d.current()
	prompt := p2tui.JField(state, "prompt")
	promptType := p2tui.JStr(p2tui.JField(prompt, "type"))
	if d.selectList != nil && promptType == "select" {
		d.selectList.HandleInput(data)
		return
	}
	if keys.Matches(data, "tui.select.cancel") {
		d.cancel()
		return
	}
	// pi: app.message.copy copies the sign-in URL (the launcher suspends its own copy key while this dialog has focus).
	if keys.Matches(data, "app.message.copy") {
		d.copyURL()
		return
	}
	if prompt == nil {
		if keys.Matches(data, "tui.select.confirm") {
			if state != nil && p2tui.JStr(p2tui.JField(state, "status")) != "working" {
				d.Close()
			} else {
				d.openURL()
			}
		}
		return
	}
	if keys.Matches(data, "app.tools.expand") {
		d.openURL()
		return
	}
	if promptType == "secret" {
		d.secret.HandleInput(data)
	} else {
		d.input.HandleInput(data)
	}
}

func (d *LoginDialog) notice(event any) []string {
	t := d.ctx.Theme().Current()
	switch p2tui.JStr(p2tui.JField(event, "type")) {
	case "auth_url":
		lines := []string{t.Fg("accent", render.LineText(p2tui.JStr(p2tui.JField(event, "url"))))}
		if instructions := p2tui.JStr(p2tui.JField(event, "instructions")); instructions != "" {
			lines = append(lines, t.Fg("warning", render.LineText(instructions)))
		}
		return lines
	case "device_code":
		return []string{t.Fg("accent", render.LineText(p2tui.JStr(p2tui.JField(event, "verificationUri")))), t.Fg("warning", "Enter code: "+render.LineText(p2tui.JStr(p2tui.JField(event, "userCode"))))}
	case "progress":
		return []string{t.Fg("dim", render.LineText(p2tui.JStr(p2tui.JField(event, "message"))))}
	case "info":
		lines := []string{render.LineText(p2tui.JStr(p2tui.JField(event, "message")))}
		for _, link := range p2tui.JList(p2tui.JField(event, "links")) {
			text := p2tui.JStr(p2tui.JField(link, "url"))
			if label := p2tui.JStr(p2tui.JField(link, "label")); label != "" {
				text = label + ": " + text
			}
			lines = append(lines, t.Fg("accent", render.LineText(text)))
		}
		return lines
	}
	return nil
}

// Render draws the dialog.
func (d *LoginDialog) Render(width int) []string {
	t := d.ctx.Theme().Current()
	hint := func(id, label string) string { return d.ctx.Hints().Key(id, label) }
	wrap := func(line string) string { return widthx.TruncateToWidth(" "+line, width, "…", false) }
	border := t.Fg("border", strings.Repeat("─", max(1, width)))
	state := d.current()
	lines := []string{border, wrap(t.Fg("accent", t.Bold("Login to "+render.LineText(d.provider.name)))), ""}
	if state == nil {
		lines = append(lines, wrap(t.Fg("dim", "Starting…")))
	}
	for _, entry := range p2tui.JList(p2tui.JField(state, "notices")) {
		for _, line := range d.notice(p2tui.JField(entry, "event")) {
			lines = append(lines, wrap(line))
		}
	}
	prompt := p2tui.JField(state, "prompt")
	promptType := p2tui.JStr(p2tui.JField(prompt, "type"))
	if prompt != nil {
		lines = append(lines, "", wrap(render.LineText(p2tui.JStr(p2tui.JField(prompt, "message")))))
		if promptType == "manual_code" {
			lines = append(lines, wrap(t.Fg("dim", manualCodeHelp)))
		}
		if placeholder, ok := p2tui.JField(prompt, "placeholder").(string); ok && promptType != "secret" {
			lines = append(lines, wrap(t.Fg("dim", "e.g., "+render.LineText(placeholder))))
		}
		switch {
		case promptType == "select" && d.selectList != nil:
			lines = append(lines, d.selectList.Render(width)...)
		case promptType == "secret":
			for _, line := range d.secret.Render(width - 1) {
				lines = append(lines, " "+line)
			}
			lines = append(lines, wrap(t.Fg("dim", "Hidden as you type; never shown again")))
		default:
			for _, line := range d.input.Render(width - 1) {
				lines = append(lines, " "+line)
			}
		}
	}
	status := p2tui.JStr(p2tui.JField(state, "status"))
	message := p2tui.JStr(p2tui.JField(state, "message"))
	switch {
	case status == "success":
		if message == "" {
			message = "Signed in."
		}
		lines = append(lines, "", wrap(t.Fg("success", "✓ "+render.LineText(message))))
	case state != nil && status != "working":
		color := "muted"
		if status == "error" {
			color = "error"
		}
		if message == "" {
			message = status
		}
		lines = append(lines, "", wrap(t.Fg(color, render.LineText(message))))
	}
	if d.status != "" {
		lines = append(lines, wrap(t.Fg("error", d.status)))
	}
	event, address := d.latest()
	opens := address != "" && opensInBrowser(event)
	var hints []string
	if state != nil && status != "working" {
		hints = []string{hint("tui.select.confirm", "close")}
	} else {
		if opens && prompt == nil {
			hints = append(hints, hint("tui.select.confirm", "open in browser"))
		}
		if opens && prompt != nil && promptType != "select" {
			hints = append(hints, hint("app.tools.expand", "open in browser"))
		}
		if address != "" {
			hints = append(hints, hint("app.message.copy", "copy address"))
		}
		if prompt != nil && promptType != "select" {
			hints = append(hints, hint("tui.input.submit", "submit"))
		}
		hints = append(hints, hint("tui.select.cancel", "cancel"))
	}
	return append(lines, "", wrap(strings.Join(hints, t.Fg("dim", " · "))), border)
}

// Invalidate invalidates the inputs.
func (d *LoginDialog) Invalidate() {
	d.input.Invalidate()
	if d.selectList != nil {
		d.selectList.Invalidate()
	}
}

// Dispose is called when the overlay closes for any reason: it ends an unfinished login and releases the hold.
func (d *LoginDialog) Dispose() { d.Close() }

// providerLogin holds `ProviderLogin` for the whole flow (the server cancels an unfinished login once no hold remains); it blocks until it is live and returns nil
// (after showing why) when it is not the owner's.
func providerLogin(ctx runtime.TuiContext) *host.Reference {
	ref, err := ctx.Host().Hold(context.Background(), contract.ProviderLogin, delta.JsonObjectOf(), host.GetOptions{})
	if err != nil {
		ctx.Post(func() { ctx.Notify(err.Error(), runtime.NoticeError) })
		return nil
	}
	if err := p2runtime.WhenLive(context.Background(), ref); err != nil {
		ref.Release()
		ctx.Post(func() { ctx.Notify(err.Error(), runtime.NoticeError) })
		return nil
	}
	if variant, _, _ := ref.Variant(); variant != "owner" {
		ref.Release()
		return nil
	}
	return ref
}

func providersOfRef(ref *host.Reference) []provider {
	value, err := ref.Member("providers").Value()
	if err != nil {
		return nil
	}
	return providersOf(value)
}

func find(list []provider, name string) *provider {
	if name == "" {
		return nil
	}
	for i := range list {
		if list[i].id == name || strings.EqualFold(list[i].name, name) {
			return &list[i]
		}
	}
	return nil
}

type row struct{ key, label, detail string }

func choose(ctx runtime.TuiContext, id, title string, rows []row, onSelect func(key string, ok bool)) {
	done := false
	var overlay runtime.TuiOverlayHandle
	finish := func(key string, ok bool) {
		if done {
			return
		}
		done = true
		overlay.Close()
		onSelect(key, ok)
	}
	overlay = ctx.Overlay(id, components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string { return title },
		Rows: func() []components.ListRow {
			var out []components.ListRow
			for _, r := range rows {
				out = append(out, components.ListRow{Key: r.key, Label: r.label, Detail: r.detail})
			}
			return out
		},
		OnSelect: func(r components.ListRow) { finish(r.Key, true) },
		OnClose:  func() { finish("", false) },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
}

func start(ctx runtime.TuiContext, ref *host.Reference, p provider, authType string) {
	if variant, _, _ := ref.Variant(); variant != "owner" {
		return
	}
	var overlay runtime.TuiOverlayHandle
	dialog := NewLoginDialog(ctx, ref, p, func() { overlay.Close() })
	overlay = ctx.Overlay("login", dialog, pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	go func() {
		if _, err := ref.Call("start", delta.JsonObjectOf("providerId", p.id, "authType", authType), context.Background()); err != nil {
			ctx.Post(func() {
				ctx.Notify(err.Error(), runtime.NoticeError)
				dialog.Close()
			})
		}
	}()
}

func login(ctx runtime.TuiContext, name string) error {
	go func() {
		ref := providerLogin(ctx)
		if ref == nil {
			return
		}
		var list []provider
		for _, entry := range providersOfRef(ref) {
			if len(entry.loginMethods) > 0 {
				list = append(list, entry)
			}
		}
		ctx.Post(func() {
			pick := func(p *provider) {
				if p == nil {
					ref.Release()
					return
				}
				if len(p.loginMethods) == 1 {
					start(ctx, ref, *p, p.loginMethods[0].typ)
					return
				}
				var rows []row
				for _, method := range p.loginMethods {
					rows = append(rows, row{key: method.typ, label: render.LineText(method.label)})
				}
				choose(ctx, "login-method", "Login to "+render.LineText(p.name), rows, func(key string, ok bool) {
					for _, method := range p.loginMethods {
						if ok && method.typ == key {
							start(ctx, ref, *p, method.typ)
							return
						}
					}
					ref.Release()
				})
			}
			named := find(list, name)
			if name != "" && named == nil {
				ctx.Notify("Unknown provider: "+render.LineText(name), runtime.NoticeWarning)
			}
			if named != nil {
				pick(named)
				return
			}
			var rows []row
			for _, entry := range list {
				detail := ""
				if entry.signedIn != "" {
					detail = "✓ API key"
					if entry.signedIn == "oauth" {
						detail = "✓ subscription"
					}
				}
				rows = append(rows, row{key: entry.id, label: render.LineText(entry.name), detail: detail})
			}
			choose(ctx, "login-provider", "Select provider to login", rows, func(key string, ok bool) {
				var chosen *provider
				if ok {
					chosen = find(list, key)
				}
				pick(chosen)
			})
		})
	}()
	return nil
}

func logout(ctx runtime.TuiContext, name string) error {
	go func() {
		ref := providerLogin(ctx)
		if ref == nil {
			return
		}
		var list []provider
		for _, entry := range providersOfRef(ref) {
			if entry.signedIn != "" {
				list = append(list, entry)
			}
		}
		ctx.Post(func() {
			run := func(p *provider) {
				if p == nil {
					ref.Release()
					return
				}
				// Shown at once; a refused sign-out says so after it.
				ctx.Notify("Logged out of "+render.LineText(p.name), runtime.NoticeInfo)
				id := p.id
				go func() {
					defer ref.Release()
					if _, err := ref.Call("logout", delta.JsonObjectOf("providerId", id), context.Background()); err != nil {
						ctx.Post(func() { ctx.Fail(err) })
					}
				}()
			}
			if len(list) == 0 {
				ctx.Notify("No stored provider credentials. Environment keys may still apply.", runtime.NoticeWarning)
				ref.Release()
				return
			}
			named := find(list, name)
			if name != "" && named == nil {
				ctx.Notify("Not signed in to "+render.LineText(name), runtime.NoticeWarning)
			}
			if named != nil {
				run(named)
				return
			}
			var rows []row
			for _, entry := range list {
				rows = append(rows, row{key: entry.id, label: render.LineText(entry.name)})
			}
			choose(ctx, "logout-provider", "Select provider to logout", rows, func(key string, ok bool) {
				var chosen *provider
				if ok {
					chosen = find(list, key)
				}
				run(chosen)
			})
		})
	}()
	return nil
}

var errNotOwner = errors.New("not the owner")

// complete completes provider ids (a blocking call: the completer runs off the editor's keystroke).
func complete(prefix string, ctx runtime.TuiContext, signedIn bool) ([]pitui.AutocompleteItem, error) {
	ref, err := ctx.Host().Hold(context.Background(), contract.ProviderLogin, delta.JsonObjectOf(), host.GetOptions{})
	if err != nil {
		return nil, err
	}
	defer ref.Release()
	if err := p2runtime.WhenLive(context.Background(), ref); err != nil {
		return nil, err
	}
	if variant, _, _ := ref.Variant(); variant != "owner" {
		return nil, errNotOwner
	}
	var items []pitui.AutocompleteItem
	for _, entry := range providersOfRef(ref) {
		eligible := len(entry.loginMethods) > 0
		if signedIn {
			eligible = entry.signedIn != ""
		}
		if eligible && strings.HasPrefix(entry.id, strings.TrimSpace(prefix)) {
			items = append(items, pitui.AutocompleteItem{Value: entry.id, Label: entry.id, Description: render.LineText(entry.name)})
		}
	}
	return items, nil
}

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.login", runtime.TuiCommand{
		Title: "Login to a model provider", Category: "Owner", When: "owner && !overlay",
		Slash: &runtime.TuiCommandSlash{Name: "login", ArgumentHint: "[provider]", Complete: func(prefix string, ctx runtime.TuiContext) ([]pitui.AutocompleteItem, error) {
			return complete(prefix, ctx, false)
		}},
		Run: func(ctx runtime.TuiContext, args string) error { return login(ctx, args) },
	}),
	p2runtime.Entry("p2.logout", runtime.TuiCommand{
		Title: "Logout from a model provider", Category: "Owner", When: "owner && !overlay",
		Slash: &runtime.TuiCommandSlash{Name: "logout", ArgumentHint: "[provider]", Complete: func(prefix string, ctx runtime.TuiContext) ([]pitui.AutocompleteItem, error) {
			return complete(prefix, ctx, true)
		}},
		Run: func(ctx runtime.TuiContext, args string) error { return logout(ctx, args) },
	}),
}

// Facet is the plugin's TUI facet.
var Facet = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return p2runtime.Contribute(facet, "p2.tui-login", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiCommands, commands...)
		return nil
	})
})
