package native

import (
	"fmt"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// pig additive (D481): the providers sheet, as the web's "Providers": the providers that can be signed in to, each with its ways
// (an API key, OAuth), the flow of the sign-in in progress (a URL to open, a device code, the question it waits for) and signing
// out. The flows run on the server (p2.provider-login) over the same credentials the web and the TUI use.

// providersSheet is the modal; a.pickProvider shows it. As the web's: the list (search, one row per provider with a chevron, "Signed in"
// where a credential is stored), the provider's own sheet (how it signs in, its ways, update model catalog, sign out) and the flow
// of the sign-in running for it. Closing the sheet ends an unfinished sign-in, as the web does.
func (a *App) providersSheet(c *ui.Context) {
	if a.providers == nil {
		return
	}
	if !a.pickProvider {
		if a.providerSel != "" || a.providerQuery != "" {
			a.endProviderFlow()
			a.providerSel, a.providerQuery = "", ""
			a.setProviderResult("")
		}
		return
	}
	t := c.Theme()
	list, known := a.providers.Providers()
	var selected *screens.Provider
	for i := range list {
		if list[i].ID == a.providerSel {
			selected = &list[i]
		}
	}
	if a.providerSel != "" && known && selected == nil {
		a.providerSel = ""
	}
	login := a.providers.Login()
	ui.Modal(c, &a.pickProvider, func() {
		ui.Column(c).Width(480).Children(func() {
			title, back := "Providers", false
			if selected != nil {
				title, back = selected.Name, login == nil || login.ProviderID != selected.ID || login.Status != "working"
			}
			ui.Row(c).Height(44).AlignItems(ui.Center).Padding(0, 8, 0, 16).Children(func() {
				if selected != nil && back && (login == nil || login.ProviderID != selected.ID) {
					if iconButton(c, iconChevronL, "Back").Clicked() {
						a.providerSel = ""
						a.setProviderResult("")
					}
				}
				ui.Text(c, title).Bold().FontSize(16).Grow(1).SingleLine()
				if iconButton(c, iconClose, "Close").Clicked() {
					a.endProviderFlow()
					a.pickProvider = false
				}
			})
			ui.Divider(c)
			switch {
			case selected != nil && login != nil && login.ProviderID == selected.ID:
				a.loginFlow(c, login)
			case selected != nil:
				a.providerDetail(c, *selected)
			default:
				a.providerList(c, list, known)
			}
		})
		_ = t
	})
}

// endProviderFlow cancels and clears the sign-in running for the selected provider (the sheet is closing or going back).
func (a *App) endProviderFlow() {
	login := a.providers.Login()
	if login == nil || login.ProviderID != a.providerSel {
		return
	}
	id, working := login.AttemptID, login.Status == "working"
	go func() {
		if working {
			_ = a.providers.Cancel(id)
		}
		_ = a.providers.Dismiss(id)
	}()
}

func (a *App) providerList(c *ui.Context, list []screens.Provider, known bool) {
	t := c.Theme()
	ui.Row(c).Height(44).AlignItems(ui.Center).Gap(8).Padding(0, 16).Children(func() {
		ui.Icon(c, iconSearch).Size(16, 16).TextColor(t.TextMuted)
		ui.TextInput(c.Key("provider-search"), &a.providerQuery).Placeholder("Search providers").Label("Search providers").Grow(1)
	})
	ui.Divider(c)
	if !known {
		ui.Row(c).Height(80).Center().Children(func() { ui.Spinner(c) })
		return
	}
	q := strings.ToLower(strings.TrimSpace(a.providerQuery))
	var shown []screens.Provider
	for _, p := range list {
		if q == "" || strings.Contains(strings.ToLower(p.Name+" "+p.ID), q) {
			shown = append(shown, p)
		}
	}
	sort.SliceStable(shown, func(i, j int) bool {
		if (shown[i].SignedIn != "") != (shown[j].SignedIn != "") {
			return shown[i].SignedIn != ""
		}
		return strings.ToLower(shown[i].Name) < strings.ToLower(shown[j].Name)
	})
	if len(shown) == 0 {
		text := "No providers available."
		if q != "" {
			text = "No provider matches."
		}
		ui.Row(c).Height(80).Center().Children(func() { ui.Text(c, text).TextColor(t.TextMuted) })
		return
	}
	ui.Column(c).MaxHeight(560).Children(func() {
		ui.Scroll(c).Children(func() {
			for _, p := range shown {
				p := p
				row := ui.Row(c.Key("provider/"+p.ID)).Height(44).AlignItems(ui.Center).Gap(8).Padding(0, 16).Focusable().Label(p.Name).Children(func() {
					ui.Text(c, p.Name).FontSize(14).Grow(1).SingleLine()
					if p.SignedIn != "" {
						ui.Text(c, "Signed in").FontSize(12).TextColor(t.TextMuted)
					}
					ui.Icon(c, iconChevronR).Size(16, 16).TextColor(t.TextMuted)
				})
				if row.Hovered() {
					row.Background(t.SurfaceHover)
				}
				if row.Clicked() {
					a.providerSel = p.ID
					a.setProviderResult("")
				}
			}
		})
	})
}

// providerDetail is one provider's sheet: how it signs in now, the ways to sign in, refreshing its catalog, signing out.
func (a *App) providerDetail(c *ui.Context, p screens.Provider) {
	t := c.Theme()
	result, busy := a.providerResultState()
	ui.Column(c).Gap(12).Padding(16).Children(func() {
		status := "Not signed in. Environment keys may still apply."
		if p.SignedIn != "" {
			status = "Signed in with an API key."
			if p.SignedIn == "oauth" {
				status = "Signed in with a subscription."
			}
		}
		ui.Text(c, status+" Credentials are stored on the server.").FontSize(12).TextColor(t.TextMuted)
		action := func(key, label string, danger bool, enabled bool, chevron bool) bool {
			row := ui.Row(c.Key(key)).Height(44).AlignItems(ui.Center).Gap(8).Padding(0, 12).Radius(8).BorderWidth(1).BorderColor(t.Border).Focusable().Label(label).Children(func() {
				colour := t.Text
				if danger {
					colour = t.Danger
				}
				ui.Text(c, label).FontSize(14).Grow(1).TextColor(colour)
				if chevron {
					ui.Icon(c, iconChevronR).Size(16, 16).TextColor(t.TextMuted)
				}
			})
			if row.Hovered() && enabled {
				row.Background(t.SurfaceHover)
			}
			return row.Clicked() && enabled
		}
		for _, m := range p.Methods {
			if action("method/"+m.Type, m.Label, false, true, true) {
				id, kind := p.ID, m.Type
				go func() { a.toastError(a.providers.Start(id, kind)) }()
			}
		}
		if p.SignedIn != "" {
			label := "Update model catalog"
			if busy {
				label = "Updating model catalog…"
			}
			if action("refresh", label, false, !busy, !busy) {
				id := p.ID
				a.setProviderBusy(true)
				go func() {
					models, err := a.providers.Refresh(id)
					a.setProviderBusy(false)
					switch {
					case err != nil:
						a.toastError(err)
					case models > 0:
						a.setProviderResult(fmt.Sprintf("%d model%s available.", models, map[bool]string{true: "", false: "s"}[models == 1]))
					default:
						a.setProviderResult("Catalog updated; no models reported.")
					}
				}()
			}
			if action("signout", "Sign out", true, true, false) {
				id := p.ID
				go func() { a.toastError(a.providers.Logout(id)) }()
			}
		}
		if result != "" {
			ui.Text(c, result).FontSize(13).TextColor(t.TextMuted)
		}
	})
}

func (a *App) setProviderResult(text string) {
	a.mu.Lock()
	a.providerResult = text
	a.mu.Unlock()
	a.Invalidate()
}

func (a *App) setProviderBusy(busy bool) {
	a.mu.Lock()
	a.providerBusy = busy
	a.mu.Unlock()
	a.Invalidate()
}

func (a *App) providerResultState() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.providerResult, a.providerBusy
}

// loginFlow is the sign-in in progress or just finished, as the web's flow: notices, the question, the state, Cancel or Done.
func (a *App) loginFlow(c *ui.Context, login *screens.Login) {
	t := c.Theme()
	ui.Column(c).Gap(12).Padding(16).Children(func() {
		a.loginFlowBody(c, login, t)
	})
}

func (a *App) loginFlowBody(c *ui.Context, login *screens.Login, t *ui.Theme) {
	for _, n := range login.Notices {
		ui.Column(c.Key("notice")).Gap(4).Padding(8, 10).Radius(6).Background(t.SurfaceHover).Children(func() {
			switch n.Kind {
			case "auth_url":
				ui.Text(c, "Open this page to sign in:").FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, n.URL).FontSize(12).Font("monospace").Selectable()
				if n.Instructions != "" {
					ui.Text(c, n.Instructions).FontSize(12).TextColor(t.TextMuted)
				}
				a.urlButtons(c, n.URL)
			case "device_code":
				ui.Text(c, "Enter this code at "+n.URL).FontSize(12).TextColor(t.TextMuted)
				ui.Text(c, n.Code).Bold().FontSize(22).Font("monospace").Selectable()
				a.urlButtons(c, n.URL)
				if ui.Button(c.Key("copy-code"), "Copy code").Clicked() {
					mygo.Clipboard.WriteText(n.Code)
				}
			default:
				ui.Text(c, n.Message).FontSize(12).Selectable()
				for _, l := range n.Links {
					label := l.Label
					if label == "" {
						label = l.URL
					}
					if ui.Button(c.Key(l.URL), label).Clicked() {
						_ = mygo.Shell.OpenExternal(l.URL)
					}
				}
			}
		})
	}
	if p := login.Prompt; p != nil {
		ui.Column(c.Key("prompt/" + p.ID)).Gap(8).Children(func() {
			ui.Text(c, p.Message).FontSize(13)
			if p.Kind == "select" {
				for _, o := range p.Options {
					if ui.Button(c.Key(o.ID), o.Label).Clicked() {
						a.answer(c, login, p.ID, o.ID)
					}
				}
				return
			}
			input := ui.TextInput(c.Key("answer"), &a.loginAnswer).Placeholder(p.Placeholder).Label(p.Message).AutoFocus()
			if p.Kind == "secret" {
				input.Password()
			}
			submit := input.Submitted()
			if ui.PrimaryButton(c, "Continue").Disabled(a.loginAnswer == "").Clicked() {
				submit = true
			}
			if submit && a.loginAnswer != "" {
				value := a.loginAnswer
				a.loginAnswer = ""
				a.answer(c, login, p.ID, value)
			}
		})
	}
	switch login.Status {
	case "working":
		if login.Prompt == nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Size(14, 14)
				ui.Text(c, "Waiting…").FontSize(12).TextColor(t.TextMuted)
			})
		}
		if ui.Button(c, "Cancel").Clicked() {
			id := login.AttemptID
			go func() { _ = a.providers.Cancel(id) }()
		}
	default:
		text, colour := "Signed in.", t.Success
		switch login.Status {
		case "error":
			text, colour = login.Message, t.Danger
		case "cancelled":
			text, colour = "Cancelled.", t.TextMuted
		}
		ui.Text(c, text).TextColor(colour).Selectable()
		if ui.Button(c, "Done").Clicked() {
			id := login.AttemptID
			a.loginAnswer = ""
			go func() { _ = a.providers.Dismiss(id) }()
		}
	}
}

func (a *App) urlButtons(c *ui.Context, url string) {
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c.Key("open-url"), "Open in browser").Clicked() {
			_ = mygo.Shell.OpenExternal(url)
		}
		if ui.Button(c.Key("copy-url"), "Copy link").Clicked() {
			mygo.Clipboard.WriteText(url)
		}
	})
}

func (a *App) answer(c *ui.Context, login *screens.Login, prompt, value string) {
	attempt := login.AttemptID
	go func() { a.toastError(a.providers.Answer(attempt, prompt, value)) }()
}

// toastError queues err's message for the next frame: a toast belongs to a UI context, which only the frame's goroutine may use,
// so work running off the frame (the sign-in calls) hands its errors over here and the frame shows them.
func (a *App) toastError(err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	a.toasts = append(a.toasts, err.Error())
	a.mu.Unlock()
	a.Invalidate()
}

// showToasts shows what toastError queued; it runs in the frame.
func (a *App) showToasts(c *ui.Context) {
	a.mu.Lock()
	queued := a.toasts
	a.toasts = nil
	a.mu.Unlock()
	for _, message := range queued {
		c.Toast(message)
	}
}
