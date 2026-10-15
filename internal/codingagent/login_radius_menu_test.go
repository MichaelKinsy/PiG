package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/radius-login-selector.ts

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 1.0.0 interactive-mode.ts:5809-5925: the top-level /login menu offers Radius as its last option, and cancelling a
// login ("Login cancelled") reopens the selector it was started from: the top-level menu for Radius, the provider list
// for a provider picked from it.
func TestLoginCancelReturnsToTheMenuItWasStartedFrom(t *testing.T) {
	providers := []tui.OAuthProvider{{ID: "anthropic", Name: "Anthropic", AuthType: "oauth"}, {ID: "radius", Name: "Radius", AuthType: "oauth"}, {ID: "openai", Name: "OpenAI", AuthType: "api_key"}}
	newContext := func(choices []string, picks []string, logins map[string]error) (*SlashContext, *[]string) {
		sc, _, _, _ := newTestSlashContext()
		var trace []string
		sc.LoginProviders = func() []tui.OAuthProvider { return providers }
		sc.SelectAuthMethod = func(scoped []tui.OAuthProvider) (string, bool) {
			trace = append(trace, "method")
			if scoped != nil || len(choices) == 0 {
				return "", false
			}
			choice := choices[0]
			choices = choices[1:]
			return choice, choice != ""
		}
		sc.SelectAuthProvider = func(_ string, options []tui.OAuthProvider, _ string) (tui.OAuthProvider, bool) {
			trace = append(trace, "providers")
			if len(picks) == 0 {
				return tui.OAuthProvider{}, false
			}
			pick := picks[0]
			picks = picks[1:]
			index := slices.IndexFunc(options, func(p tui.OAuthProvider) bool { return p.ID == pick })
			if index < 0 {
				return tui.OAuthProvider{}, false
			}
			return options[index], true
		}
		sc.StartProviderLogin = func(p tui.OAuthProvider) error {
			trace = append(trace, "login:"+p.ID)
			err := logins[p.ID]
			delete(logins, p.ID)
			return err
		}
		return sc, &trace
	}

	t.Run("radius", func(t *testing.T) {
		sc, trace := newContext([]string{"radius", ""}, nil, map[string]error{"radius": errLoginCancelled})
		if err := loginHandler(sc); err != nil {
			t.Fatal(err)
		}
		if want := []string{"method", "login:radius", "method"}; !slices.Equal(*trace, want) {
			t.Fatalf("trace=%v, want %v", *trace, want)
		}
	})
	t.Run("provider list", func(t *testing.T) {
		sc, trace := newContext([]string{"oauth"}, []string{"anthropic", "anthropic"}, map[string]error{"anthropic": errLoginCancelled})
		if err := loginHandler(sc); err != nil {
			t.Fatal(err)
		}
		if want := []string{"method", "providers", "login:anthropic", "providers", "login:anthropic"}; !slices.Equal(*trace, want) {
			t.Fatalf("trace=%v, want %v", *trace, want)
		}
	})
	t.Run("exact argument has no menu to return to", func(t *testing.T) {
		sc, trace := newContext(nil, nil, map[string]error{"anthropic": errLoginCancelled})
		sc.Args = "anthropic"
		if err := loginHandler(sc); err != nil {
			t.Fatal(err)
		}
		if want := []string{"login:anthropic"}; !slices.Equal(*trace, want) {
			t.Fatalf("trace=%v, want %v", *trace, want)
		}
	})
	t.Run("a failure is not a cancellation", func(t *testing.T) {
		failure := errLoginAborted
		sc, trace := newContext([]string{"oauth"}, []string{"anthropic"}, map[string]error{"anthropic": failure})
		if err := loginHandler(sc); !errors.Is(err, failure) {
			t.Fatalf("err=%v", err)
		}
		if want := []string{"method", "providers", "login:anthropic"}; !slices.Equal(*trace, want) {
			t.Fatalf("trace=%v, want %v", *trace, want)
		}
	})
}

// Pi 1.0.0 interactive-mode.ts:5887-5893 names OAuth login providers "account" providers.
func TestLoginWithoutAccountProvidersSaysAccount(t *testing.T) {
	sc, out, _, _ := newTestSlashContext()
	sc.LoginProviders = func() []tui.OAuthProvider {
		return []tui.OAuthProvider{{ID: "openai", Name: "OpenAI", AuthType: "api_key"}}
	}
	calls := 0
	sc.SelectAuthMethod = func([]tui.OAuthProvider) (string, bool) { calls++; return "oauth", calls == 1 }
	sc.SelectAuthProvider = func(string, []tui.OAuthProvider, string) (tui.OAuthProvider, bool) {
		t.Fatal("empty provider list opened")
		return tui.OAuthProvider{}, false
	}
	sc.StartProviderLogin = func(tui.OAuthProvider) error { return nil }
	if err := loginHandler(sc); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No account providers available.") {
		t.Fatalf("output=%q", out.String())
	}
}

// Pi 1.0.0 radius-login-selector.ts: the selected "Sign in with Radius" row shimmers in the Radius logo colors, and the
// row renders normally when another option is selected.
func TestRadiusLoginMenuShimmersOnlyTheSelectedRadiusRow(t *testing.T) {
	label := "Sign in with Radius" + tui.FormatAuthSelectorProviderStatus(tui.OAuthProvider{ID: "radius", Name: "Radius", AuthType: "oauth"})
	selector := tui.NewExtensionSelectorComponent("Select authentication method:", []string{"Sign in with an account", "Sign in with an API key", label}, nil, nil)
	renders := make(chan struct{}, 1)
	menu := newRadiusLoginMenu(selector, label, "Sign in with Radius", func() {
		select {
		case renders <- struct{}{}:
		default:
		}
	})
	defer menu.Dispose()
	plain := selector.Render(120)
	if got := menu.Render(120); !slices.Equal(got, plain) || menu.animating.Load() {
		t.Fatal("an unselected Radius row was animated")
	}
	selector.HandleInput("\x1b[B")
	selector.HandleInput("\x1b[B")
	plain = selector.Render(120)
	animated := menu.Render(120)
	if !menu.animating.Load() {
		t.Fatal("the selected Radius row did not animate")
	}
	changed := -1
	for i := range plain {
		if plain[i] != animated[i] {
			if changed >= 0 {
				t.Fatalf("more than one row changed: %d and %d", changed, i)
			}
			changed = i
		}
	}
	if changed < 0 || llamaTestANSI.ReplaceAllString(animated[changed], "") != llamaTestANSI.ReplaceAllString(plain[changed], "") {
		t.Fatalf("animated row %d: %q, plain %q", changed, animated[changed], plain[changed])
	}
	if !strings.Contains(animated[changed], "\x1b[38;2;77;154;191mS") && !strings.Contains(animated[changed], "\x1b[38;5;") {
		t.Fatalf("animated row lacks the Radius blue: %q", animated[changed])
	}
	select {
	case <-renders:
	case <-time.After(2 * time.Second):
		t.Fatal("the animation did not request a render")
	}
}
