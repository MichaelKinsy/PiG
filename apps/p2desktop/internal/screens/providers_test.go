package screens_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/screens"
	"github.com/MichaelKinsy/PiG/apps/p2desktop/internal/surfacehost"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

func TestProvidersListAndApiKeyLoginAgainstTheServer(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	fake, zero := false, 0
	server, err := hosts.StartServer(hosts.ServerOptions{DataDir: t.TempDir(), Port: &zero, Fake: &fake, Facets: []host.HostFacet{surfacehost.ServerFacet(nil)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(p2test.Background) })
	h, err := surfacehost.Open(surfacehost.Options{Server: server.Host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	scope := h.Scope(func() {})
	t.Cleanup(scope.Dispose)
	p, err := screens.OpenProviders(h, scope)
	if err != nil {
		t.Fatal(err)
	}
	var providers []screens.Provider
	waitFor(t, "the providers", func() bool { var ok bool; providers, ok = p.Providers(); return ok })
	t.Logf("providers: %d", len(providers))
	var target *screens.Provider
	for i := range providers {
		for _, m := range providers[i].Methods {
			if m.Type == "api_key" && providers[i].ID == "openai" {
				target = &providers[i]
			}
		}
	}
	if target == nil {
		t.Fatalf("no provider with an API key login among %d", len(providers))
	}
	waitFor(t, "no credentials", p.NoCredentials)
	if err := p.Start(target.ID, "api_key"); err != nil {
		t.Fatal(err)
	}
	var login *screens.Login
	waitFor(t, "the key prompt", func() bool { login = p.Login(); return login != nil && login.Prompt != nil })
	if login.Prompt.Kind != "secret" && login.Prompt.Kind != "text" {
		t.Fatalf("prompt = %+v", login.Prompt)
	}
	if err := p.Answer(login.AttemptID, login.Prompt.ID, "sk-test-key"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "success", func() bool { login = p.Login(); return login != nil && login.Status != "working" })
	if login.Status != "success" {
		t.Fatalf("login = %+v", login)
	}
	if err := p.Dismiss(login.AttemptID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "signed in", func() bool {
		list, _ := p.Providers()
		for _, each := range list {
			if each.ID == target.ID && each.SignedIn == "api_key" {
				return !p.NoCredentials()
			}
		}
		return false
	})
	if err := p.Logout(target.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "signed out", p.NoCredentials)
}
