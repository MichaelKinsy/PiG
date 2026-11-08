package screens

import (
	"context"

	logincontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/login/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/tui"
)

// Provider is a model provider as the sign-in lists it.
type Provider struct {
	ID, Name string
	// Methods are the ways to sign in: "api_key" or "oauth", with their labels.
	Methods []Method
	// SignedIn is how the stored credential signs in ("api_key", "oauth"), empty when none.
	SignedIn string
}

// Method is a way to sign in to a provider.
type Method struct{ Type, Label string }

// Notice is something the sign-in flow told the user: a URL to open, a device code, progress.
type Notice struct {
	ID int
	// Kind is the event's type: auth_url, device_code, info or progress.
	Kind                             string
	Message, URL, Code, Instructions string
	Links                            []Link
}

// Link is a link of an info notice.
type Link struct{ URL, Label string }

// Prompt is the question a sign-in flow waits for.
type Prompt struct {
	ID string
	// Kind is text, secret, manual_code or select.
	Kind, Message, Placeholder string
	Options                    []Option
}

// Option is an answer of a select prompt.
type Option struct{ ID, Label, Description string }

// Login is the sign-in in progress or just finished (one at a time).
type Login struct {
	AttemptID, ProviderID, AuthType string
	// Status is working, success, cancelled or error.
	Status  string
	Prompt  *Prompt
	Notices []Notice
	Message string
}

// Providers is the providers screen: the server's p2.provider-login (the web's Providers sheet) held while the screen is open,
// and p2.models for the model count and the signed-in hint.
type Providers struct {
	scope *tui.TuiScope
	ref   *host.Reference
	ctx   context.Context

	providersRaw func() (delta.JsonValue, bool)
	loginRaw     func() (delta.JsonValue, bool)
	resourcesRaw func() (delta.JsonValue, bool)
}

// OpenProviders holds the sign-in's references on scope.
func OpenProviders(h *surfacehost.Host, scope *tui.TuiScope) (*Providers, error) {
	ref, err := scope.Hold(logincontract.ProviderLogin, delta.NewJsonObject(0))
	if err != nil {
		return nil, err
	}
	models, err := scope.Hold(modelscontract.Models, delta.NewJsonObject(0))
	if err != nil {
		return nil, err
	}
	p := &Providers{scope: scope, ref: ref, ctx: h.Context()}
	p.providersRaw = tui.WatchMember(scope, ref.Member("providers"))
	p.loginRaw = tui.WatchMember(scope, ref.Member("login"))
	p.resourcesRaw = tui.WatchMember(scope, models.Member("resources"))
	return p, nil
}

func s(v any) string { str, _ := v.(string); return str }

// Providers are the providers that can be signed in to or are, in the server's order.
func (p *Providers) Providers() ([]Provider, bool) {
	value, ok := p.providersRaw()
	if !ok {
		return nil, false
	}
	list, _ := value.([]any)
	out := make([]Provider, 0, len(list))
	for _, each := range list {
		o, _ := each.(*delta.JsonObject)
		if o == nil {
			continue
		}
		provider := Provider{ID: s(o.Value("id")), Name: s(o.Value("name")), SignedIn: s(o.Value("signedIn"))}
		methods, _ := o.Value("loginMethods").([]any)
		for _, m := range methods {
			if mo, _ := m.(*delta.JsonObject); mo != nil {
				provider.Methods = append(provider.Methods, Method{Type: s(mo.Value("type")), Label: s(mo.Value("label"))})
			}
		}
		out = append(out, provider)
	}
	return out, true
}

// NoCredentials says no provider has a stored credential: the empty state offers "Sign in to a provider" then. Unknown (the
// server's snapshot has not arrived) counts as having some, so the button does not flash.
func (p *Providers) NoCredentials() bool {
	value, ok := p.resourcesRaw()
	if !ok {
		return false
	}
	o, _ := value.(*delta.JsonObject)
	if o == nil {
		return false
	}
	credentials, ok := o.Value("credentials").([]any)
	return ok && len(credentials) == 0
}

// Login is the sign-in in progress or just finished; nil without one.
func (p *Providers) Login() *Login {
	value, _ := p.loginRaw()
	o, _ := value.(*delta.JsonObject)
	if o == nil {
		return nil
	}
	login := &Login{AttemptID: s(o.Value("attemptId")), ProviderID: s(o.Value("providerId")), AuthType: s(o.Value("authType")), Status: s(o.Value("status")), Message: s(o.Value("message"))}
	if po, _ := o.Value("prompt").(*delta.JsonObject); po != nil {
		prompt := &Prompt{ID: s(po.Value("id")), Kind: s(po.Value("type")), Message: s(po.Value("message")), Placeholder: s(po.Value("placeholder"))}
		options, _ := po.Value("options").([]any)
		for _, each := range options {
			if oo, _ := each.(*delta.JsonObject); oo != nil {
				prompt.Options = append(prompt.Options, Option{ID: s(oo.Value("id")), Label: s(oo.Value("label")), Description: s(oo.Value("description"))})
			}
		}
		login.Prompt = prompt
	}
	notices, _ := o.Value("notices").([]any)
	for _, each := range notices {
		no, _ := each.(*delta.JsonObject)
		if no == nil {
			continue
		}
		id, _ := no.Value("id").(float64)
		event, _ := no.Value("event").(*delta.JsonObject)
		if event == nil {
			continue
		}
		n := Notice{ID: int(id), Kind: s(event.Value("type")), Message: s(event.Value("message")), URL: s(event.Value("url")), Instructions: s(event.Value("instructions"))}
		switch n.Kind {
		case "device_code":
			n.Code, n.URL = s(event.Value("userCode")), s(event.Value("verificationUri"))
		case "info":
			links, _ := event.Value("links").([]any)
			for _, l := range links {
				if lo, _ := l.(*delta.JsonObject); lo != nil {
					n.Links = append(n.Links, Link{URL: s(lo.Value("url")), Label: s(lo.Value("label"))})
				}
			}
		}
		login.Notices = append(login.Notices, n)
	}
	return login
}

// Start begins signing in to provider with authType ("api_key" or "oauth").
func (p *Providers) Start(provider, authType string) error {
	_, err := p.ref.Call("start", delta.JsonObjectOf("providerId", provider, "authType", authType), p.ctx)
	return err
}

// Answer answers the flow's question.
func (p *Providers) Answer(attempt, prompt, value string) error {
	_, err := p.ref.Call("answer", delta.JsonObjectOf("attemptId", attempt, "promptId", prompt, "value", value), p.ctx)
	return err
}

// Cancel stops the sign-in in progress.
func (p *Providers) Cancel(attempt string) error {
	_, err := p.ref.Call("cancel", delta.JsonObjectOf("attemptId", attempt), p.ctx)
	return err
}

// Dismiss clears a finished sign-in.
func (p *Providers) Dismiss(attempt string) error {
	_, err := p.ref.Call("dismiss", delta.JsonObjectOf("attemptId", attempt), p.ctx)
	return err
}

// Logout removes provider's stored credential.
func (p *Providers) Logout(provider string) error {
	_, err := p.ref.Call("logout", delta.JsonObjectOf("providerId", provider), p.ctx)
	return err
}
