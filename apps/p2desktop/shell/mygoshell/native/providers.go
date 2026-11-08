package native

import (
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// pig additive (D481): the providers sheet, as the web's "Providers": the providers that can be signed in to, each with its ways
// (an API key, OAuth), the flow of the sign-in in progress (a URL to open, a device code, the question it waits for) and signing
// out. The flows run on the server (p2.provider-login) over the same credentials the web and the TUI use.

// providersSheet is the modal; a.pickProvider shows it.
func (a *App) providersSheet(c *ui.Context) {
	if a.providers == nil {
		return
	}
	t := c.Theme()
	ui.Modal(c, &a.pickProvider, func() {
		ui.Column(c).Width(520).Gap(12).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Providers").Bold().FontSize(18).Grow(1)
				if ui.Button(c, "Done").Clicked() {
					a.pickProvider = false
				}
			})
			if login := a.providers.Login(); login != nil {
				a.loginFlow(c, login)
				return
			}
			ui.Text(c, "Sign in to the providers whose models PiG should use. Credentials are kept by the server and never shown.").FontSize(12).TextColor(t.TextMuted)
			ui.TextInput(c.Key("provider-search"), &a.providerQuery).Placeholder("Search providers").Label("Search providers")
			list, ok := a.providers.Providers()
			if !ok {
				ui.Spinner(c)
				return
			}
			q := strings.ToLower(strings.TrimSpace(a.providerQuery))
			ui.Column(c).MaxHeight(380).Gap(2).Children(func() {
				ui.Scroll(c).Gap(2).Children(func() {
					for _, p := range list {
						if q != "" && !strings.Contains(strings.ToLower(p.Name+" "+p.ID), q) {
							continue
						}
						a.providerRow(c, p)
					}
				})
			})
		})
	})
}

func (a *App) providerRow(c *ui.Context, p screens.Provider) {
	t := c.Theme()
	ui.Row(c.Key("provider/"+p.ID)).Gap(8).AlignItems(ui.Center).Padding(6, 4).Children(func() {
		ui.Column(c).Grow(1).Children(func() {
			ui.Text(c, p.Name).FontSize(14).SingleLine()
			if p.SignedIn != "" {
				label := "Signed in with an API key"
				if p.SignedIn == "oauth" {
					label = "Signed in with OAuth"
				}
				ui.Text(c, label).FontSize(11).TextColor(t.Success)
			}
		})
		for _, m := range p.Methods {
			label := "Sign in"
			if len(p.Methods) > 1 || p.SignedIn != "" {
				label = m.Label
			}
			if label == "" {
				label = "Sign in"
			}
			if ui.Button(c.Key(m.Type), label).Clicked() {
				id, kind := p.ID, m.Type
				go func() {
					if err := a.providers.Start(id, kind); err != nil {
						c.Toast(err.Error())
					}
				}()
			}
		}
		if p.SignedIn != "" {
			if ui.Button(c, "Sign out").Clicked() {
				id := p.ID
				go func() {
					if err := a.providers.Logout(id); err != nil {
						c.Toast(err.Error())
					}
				}()
			}
		}
	})
}

// loginFlow is the sign-in in progress or just finished.
func (a *App) loginFlow(c *ui.Context, login *screens.Login) {
	t := c.Theme()
	name := login.ProviderID
	if list, ok := a.providers.Providers(); ok {
		for _, p := range list {
			if p.ID == login.ProviderID {
				name = p.Name
			}
		}
	}
	ui.Text(c, "Signing in to "+name).Bold().FontSize(15)
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
		if ui.PrimaryButton(c, "Back to providers").Clicked() {
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
	go func() {
		if err := a.providers.Answer(attempt, prompt, value); err != nil {
			c.Toast(err.Error())
		}
	}()
}
