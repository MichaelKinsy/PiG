package tui_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/login"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/login/contract"
	logintui "github.com/MichaelKinsy/PiG/apps/p2/plugins/login/tui"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/tuitest"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

const (
	secret = "sk-fixture-SECRET-42"
	code   = "private-pasted-code"
)

type setup struct {
	*tuitest.Fixture
	pasted  *[]string
	mu      *sync.Mutex
	grants  *[][2]any
	watcher *host.Reference
	server  *p2test.Fixture
}

func (s *setup) pastedCodes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), *s.pasted...)
}

func fixtureModels(pasted *[]string, mu *sync.Mutex) func(ai.CredentialStore, ai.ModelsStore) (*ai.MutableModels, error) {
	return func(credentials ai.CredentialStore, store ai.ModelsStore) (*ai.MutableModels, error) {
		models := ai.CreateModels(ai.CreateModelsOptions{Credentials: credentials, ModelsStore: store})
		faux := ai.NewFauxProvider(ai.FauxConfig{API: "test-tui-login"})
		provider := *faux.Provider()
		provider.Auth = ai.ProviderAuth{
			APIKey: &ai.APIKeyAuth{
				Name: "Fixture key",
				Login: func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
					key, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "Fixture key"})
					if err != nil {
						return ai.Credential{}, err
					}
					return ai.Credential{Type: ai.CredentialAPIKey, Key: key}, nil
				},
				Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return nil, nil },
			},
			OAuth: &ai.OAuthAuth{
				Name: "Fixture OAuth",
				Login: func(ctx context.Context, interaction ai.AuthInteraction, _ ai.LoginOptions) (ai.Credential, error) {
					interaction.Notify(ai.AuthURLEvent{URL: "https://example.test/login?state=x"})
					entered, err := interaction.Prompt(ctx, ai.AuthManualCodePrompt{Message: "Paste code or final redirect URL"})
					if err != nil {
						return ai.Credential{}, err
					}
					mu.Lock()
					*pasted = append(*pasted, entered)
					mu.Unlock()
					if _, err := interaction.Prompt(ctx, ai.AuthSelectPrompt{Message: "Account", Options: []ai.AuthSelectOption{{ID: "account", Label: "Fixture account"}}}); err != nil {
						return ai.Credential{}, err
					}
					return ai.Credential{Type: ai.CredentialOAuth, Access: "a", Refresh: "r", Expires: time.Now().Add(10 * time.Minute).UnixMilli()}, nil
				},
				Refresh: func(_ context.Context, credential ai.Credential) (ai.Credential, error) { return credential, nil },
				ToAuth: func(credential ai.Credential) (ai.ModelAuth, error) {
					return ai.ModelAuth{APIKey: credential.Access}, nil
				},
			},
		}
		models.SetProvider(&provider)
		return models, nil
	}
}

func start(t *testing.T) *setup {
	t.Helper()
	var mu sync.Mutex
	pasted := &[]string{}
	server := p2test.StartFixture(t, hosts.ServerOptions{
		Plugins:   []string{"login", "models"},
		Resources: p2runtime.ServerResourceOptions{CreateModels: fixtureModels(pasted, &mu)},
	})
	// A remote owner: the server offers the manual code paste (no reachable localhost callback).
	client := server.As(p2runtime.OwnerPrincipal{Access: "relay"})
	if report, err := client.Load([]host.HostFacet{{Name: "tui-core", Facet: p2tui.CoreFacet}, {Name: "tui-login", Facet: logintui.Facet}}, p2test.Background); err != nil || len(report.Failed) > 0 {
		t.Fatalf("load: %v %v", err, report.Failed)
	}
	f := tuitest.OnHost(t, client, runtime.RoleOwner, 100, 30, false)
	grants := &[][2]any{}
	var grantsMu sync.Mutex
	f.Do(func() {
		f.Runtime.OnGrant = func(action string, payload any) runtime.GrantResult {
			grantsMu.Lock()
			*grants = append(*grants, [2]any{action, payload})
			grantsMu.Unlock()
			return runtime.GrantResult{OK: true}
		}
	})
	watcher, err := p2test.HoldIn(client, contract.ProviderLogin, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(watcher.Release)
	return &setup{Fixture: f, pasted: pasted, mu: &mu, grants: grants, watcher: watcher, server: server}
}

func (s *setup) screenHas(text string) bool { return strings.Contains(s.Screen(), text) }

func (s *setup) waitFor(text string) {
	s.T.Helper()
	tuitest.Poll(s.T, func() bool { return s.screenHas(text) })
}

func (s *setup) command(slash string) {
	s.T.Helper()
	s.Poll(func() bool {
		for _, item := range s.Runtime.Ctx.Commands().List(nil) {
			if item.Slash == "login" {
				return true
			}
		}
		return false
	})
	s.Do(func() {
		if !s.Runtime.Ctx.Commands().RunSlash(slash) {
			s.T.Errorf("%s is no command", slash)
		}
	})
}

func (s *setup) loginState(predicate func(any) bool) {
	s.T.Helper()
	if _, err := p2test.Until(s.watcher.Member("login"), predicate, 15*time.Second); err != nil {
		s.T.Fatal(err)
	}
}

func statusIs(status string) func(any) bool {
	return func(v any) bool { return p2tui.JStr(p2tui.JField(v, "status")) == status }
}

func TestLoginWithASecretMaskedWhileTypedNeverEchoedNotInTheSnapshotOrStatusLines(t *testing.T) {
	s := start(t)
	s.command("/login faux")
	s.waitFor("Fixture key")
	s.Send("\r")
	s.waitFor("Hidden as you type")
	for _, char := range secret[:8] {
		s.Send(string(char))
	}
	// A paste goes in whole, still masked.
	s.Send("\x1b[200~" + secret[8:] + "\x1b[201~")
	s.waitFor(strings.Repeat("•", len(secret)))
	s.Send("\r")
	s.waitFor("✓")
	list, err := s.watcher.Member("providers").Value()
	if err != nil {
		t.Fatal(err)
	}
	signedIn := ""
	for _, each := range p2tui.JList(list) {
		if p2tui.JStr(p2tui.JField(each, "id")) == "faux" {
			signedIn = p2tui.JStr(p2tui.JField(each, "signedIn"))
		}
	}
	if signedIn != "api_key" {
		t.Fatalf("the stored credential is %q, want api_key", signedIn)
	}
	rendered := strings.Join(append(s.Terminal.ScrollBuffer(), s.Terminal.Viewport()...), "\n")
	if strings.Contains(rendered, secret[:6]) {
		t.Error("the secret was rendered")
	}
	s.Do(func() {
		if strings.Contains(fmt.Sprintf("%+v", p2tui.SnapshotRuntime(s.Runtime)), secret[:6]) {
			t.Error("the secret is in the reload snapshot")
		}
		for _, notice := range s.Runtime.Notices {
			if strings.Contains(notice.Text, secret[:6]) {
				t.Errorf("the secret is in a status line: %q", notice.Text)
			}
		}
	})
	s.Send("\r")
	tuitest.Poll(t, func() bool { return !s.screenHas("Login to") })
}

func TestLoginOAuthRemotelyTheLauncherOpensAndCopiesTheAddressTheManualCodeCompletesIt(t *testing.T) {
	s := start(t)
	s.command("/login")
	s.waitFor("Select provider to login")
	s.Send("\r")
	s.waitFor("Fixture OAuth")
	s.Send("\x1b[B")
	s.Send("\r")
	s.waitFor("https://example.test/login?state=x")
	s.waitFor("Paste code or final redirect URL")
	s.Send("\x0f")
	s.Send("\x18")
	tuitest.Poll(t, func() bool { return len(*s.grants) == 2 })
	want := [][2]any{
		{"openUrl", map[string]any{"kind": "external", "url": "https://example.test/login?state=x"}},
		{"copy", map[string]any{"text": "https://example.test/login?state=x", "label": "the sign-in address"}},
	}
	if fmt.Sprint(*s.grants) != fmt.Sprint(want) {
		t.Fatalf("grants = %v, want %v", *s.grants, want)
	}
	for _, char := range code {
		s.Send(string(char))
	}
	s.Send("\r")
	s.waitFor("Fixture account")
	s.Send("\r")
	s.waitFor("✓")
	if got := s.pastedCodes(); fmt.Sprint(got) != fmt.Sprint([]string{code}) {
		t.Fatalf("pasted = %v", got)
	}
	s.Send("\x1b")
	s.loginState(func(v any) bool { return v == nil })
}

func TestCancelAndDismissEscapeCancelsARunningLoginTheNextEscapeClosesAndClearsItClosingEndsIt(t *testing.T) {
	s := start(t)
	s.command("/login faux")
	s.waitFor("Fixture OAuth")
	s.Send("\x1b[B")
	s.Send("\r")
	s.waitFor("Paste code")
	s.Send("\x1b")
	s.loginState(statusIs("cancelled"))
	s.Send("\x1b")
	s.loginState(func(v any) bool { return v == nil })
	s.Do(func() {
		if s.Runtime.Contexts()["overlay"] {
			t.Error("an overlay is still open")
		}
	})
	// A closed overlay (navigation, reload) also ends the flow.
	s.Do(func() { s.Runtime.Ctx.Commands().RunSlash("/login faux") })
	s.waitFor("Fixture OAuth")
	s.Send("\x1b[B")
	s.Send("\r")
	s.loginState(statusIs("working"))
	s.Do(func() { s.Runtime.CloseOverlays() })
	s.loginState(func(v any) bool { return v == nil })
	// /logout lists stored credentials (this fixture's provider list never changes, so there are none).
	s.Do(func() { s.Runtime.Ctx.Commands().RunSlash("/logout") })
	s.Poll(func() bool {
		for _, notice := range s.Runtime.Notices {
			if strings.HasPrefix(notice.Text, "No stored provider credentials") {
				return true
			}
		}
		return false
	})
}
