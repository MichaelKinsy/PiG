package codingagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestLoginCloudflareRepliesStayInDialog(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	m := newPostLoginTestMode(t)
	m.opts.Model = &ai.Model{ID: "existing", ProviderMeta: ai.ProviderMetadata{ProviderID: "existing"}}
	m.runCtx = t.Context()
	done := make(chan error, 1)
	go func() {
		done <- NewSlashRegistry().Dispatch(m.buildSlashContext(t.Context()), "/login cloudflare-ai-gateway", nil)
	}()
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct{ prompt, reply string }{
		{"Enter Cloudflare API key", "  fake-key  "}, {"Enter Cloudflare account ID", "account-123"}, {"Enter Cloudflare AI Gateway ID", "gateway-456"},
	} {
		waitForRender(t, m.editorContainer, step.prompt)
		if _, stored, err := auth.Get("cloudflare-ai-gateway"); err != nil || stored {
			t.Fatalf("credential saved before final prompt: %v, %v", stored, err)
		}
		deliverModalInput(t, m, []byte(step.reply))
		deliverModalInput(t, m, []byte("\r"))
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	credential, ok, err := auth.Get("cloudflare-ai-gateway")
	if err != nil || !ok || credential.Key != "  fake-key  " || credential.Env["CLOUDFLARE_ACCOUNT_ID"] != "account-123" || credential.Env["CLOUDFLARE_GATEWAY_ID"] != "gateway-456" {
		t.Fatalf("credential=%+v ok=%v err=%v", credential, ok, err)
	}
	if text := plainRender(m.chatContainer); strings.Contains(text, "account-123") || strings.Contains(text, "gateway-456") || strings.Contains(text, "fake-key") {
		t.Fatalf("login input entered chat: %s", text)
	}
	if m.editor.Text() != "" {
		t.Fatalf("login input entered editor: %q", m.editor.Text())
	}
	if got := len(m.agent.MessagesSnapshot()); got != 0 {
		t.Fatalf("login input reached agent (%d messages)", got)
	}
}

func TestLoginProviderCancellationKeepsOldCredential(t *testing.T) {
	t.Setenv("PI_OFFLINE", "1")
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.Set("cloudflare-workers-ai", ai.Credential{Type: ai.CredentialAPIKey, Key: "old", Env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "old-account"}}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- NewSlashRegistry().Dispatch(m.buildSlashContext(t.Context()), "/login cloudflare-workers-ai", nil)
	}()
	waitForRender(t, m.editorContainer, "Enter Cloudflare API key")
	deliverModalInput(t, m, []byte("new-secret"))
	deliverModalInput(t, m, []byte("\r"))
	waitForRender(t, m.editorContainer, "Enter Cloudflare account ID")
	deliverModalInput(t, m, []byte("\x1b"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	credential, _, err := auth.Get("cloudflare-workers-ai")
	if err != nil || credential.Key != "old" || credential.Env["CLOUDFLARE_ACCOUNT_ID"] != "old-account" {
		t.Fatalf("cancel altered credential: %+v %v", credential, err)
	}
	if text := plainRender(m.chatContainer); !strings.Contains(text, "Failed to save API key for Cloudflare Workers AI: This operation was aborted") {
		t.Fatalf("cancel message: %s", text)
	}
}

func TestLoginArgumentAndBackNavigation(t *testing.T) {
	providers := []tui.OAuthProvider{{ID: "dual", Name: "Dual", AuthType: "oauth", LoginLabel: "Sign in with Dual"}, {ID: "dual", Name: "Dual", AuthType: "api_key"}, {ID: "key", Name: "Only Key", AuthType: "api_key"}}
	for _, ref := range []string{"key", "ONLY KEY", "dual", "unknown", ""} {
		t.Run(ref, func(t *testing.T) {
			sc, _, _, _ := newTestSlashContext()
			sc.Args = ref
			sc.LoginProviders = func() []tui.OAuthProvider { return providers }
			selected := ""
			methodCalls, providerCalls := 0, 0
			sc.StartProviderLogin = func(p tui.OAuthProvider) error { selected = p.ID + "/" + p.AuthType; return nil }
			sc.SelectAuthMethod = func(options []tui.OAuthProvider) (string, bool) {
				methodCalls++
				if ref == "dual" && len(options) != 2 {
					t.Errorf("unscoped method options: %v", options)
				}
				return "api_key", true
			}
			sc.SelectAuthProvider = func(mode string, options []tui.OAuthProvider, search string) (tui.OAuthProvider, bool) {
				providerCalls++
				if ref == "" && providerCalls == 1 {
					return tui.OAuthProvider{}, false
				}
				if ref == "unknown" && search != "unknown" {
					t.Errorf("search=%q", search)
				}
				return providers[2], true
			}
			if err := loginHandler(sc); err != nil {
				t.Fatal(err)
			}
			if ref == "dual" {
				if selected != "dual/api_key" || methodCalls != 1 || providerCalls != 0 {
					t.Fatalf("selected=%s methods=%d providers=%d", selected, methodCalls, providerCalls)
				}
			} else if selected != "key/api_key" {
				t.Fatalf("selected=%s", selected)
			}
			if (ref == "key" || ref == "ONLY KEY") && (methodCalls != 0 || providerCalls != 0) {
				t.Fatal("exact single-method argument opened a selector")
			}
			if ref == "" && (methodCalls != 2 || providerCalls != 2) {
				t.Fatalf("Escape did not return to method selector: %d %d", methodCalls, providerCalls)
			}
		})
	}
}

func TestLoginArgumentCompletionsUseProviderMetadata(t *testing.T) {
	m := NewInteractiveMode(InteractiveOptions{AgentDir: t.TempDir()})
	items := m.loginArgCompletions("openrouter")
	if len(items) != 1 || items[0].Value != "openrouter" || items[0].Description != "OpenRouter · subscription/API key" {
		t.Fatalf("completions=%+v", items)
	}
	if m.loginArgCompletions("no-provider-zzzz") != nil {
		t.Fatal("unmatched completion must be absent")
	}
}

func TestLogoutEmptyAndAPIKeyMessages(t *testing.T) {
	for _, empty := range []bool{true, false} {
		sc, out, _, _ := newTestSlashContext()
		sc.LogoutProviders = func() ([]tui.OAuthProvider, error) {
			if empty {
				return nil, nil
			}
			return []tui.OAuthProvider{{ID: "key", Name: "Key", AuthType: "api_key"}}, nil
		}
		sc.SelectAuthProvider = func(_ string, p []tui.OAuthProvider, _ string) (tui.OAuthProvider, bool) {
			if empty {
				t.Fatal("empty logout opened a selector")
			}
			return p[0], true
		}
		sc.Logout = func(string) error { return nil }
		if err := logoutHandler(sc); err != nil {
			t.Fatal(err)
		}
		want := "Removed stored API key for Key. Environment variables and models.json config are unchanged."
		if empty {
			want = "No stored credentials to remove. /logout only removes credentials saved by /login; environment variables and models.json config are unchanged."
		}
		if !strings.Contains(out.String(), want) {
			t.Fatalf("logout=%q", out.String())
		}
	}
}
