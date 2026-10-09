package codingagent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
)

// The provider objects below are Pi's registerNativeProvider objects (models.ts Provider), written once per SDK so the
// login path runs through the real host carrier: the extension process, the provider.callback reverse calls and the
// registry composition.
//
//   - sso-proxy declares both methods. Its OAuth login reports progress and asks a select prompt; its loginLabel names
//     the account sign-in. Its own API-key login asks for the base URL, reports it, then asks for the key, and stores
//     both.
//   - plain-proxy declares an API-key method without a login, so the generic API-key prompt applies.
//   - odd-proxy and loud-proxy send a prompt and an event type no AuthPrompt or AuthEvent variant has.
const nativeLoginNodeFixture = `export default pi => {
 const model = {id:"sso-model", name:"SSO Model", api:"openai-completions", baseUrl:"http://127.0.0.1:9", reasoning:false, input:["text"], cost:{input:0,output:0,cacheRead:0,cacheWrite:0}, contextWindow:4000, maxTokens:100};
 const unused = () => { throw new Error("unused"); };
 const resolve = async ({credential}) => credential?.key ? {auth:{apiKey:credential.key}, source:"stored credential"} : undefined;
 const object = (id, name, auth) => ({id, name, getModels:()=>[{...model, provider:id}], stream:unused, streamSimple:unused, auth});
 pi.registerProvider(object("sso-proxy", "SSO Proxy", {
  apiKey:{name:"SSO Proxy key", resolve, login: async interaction => {
   const baseUrl = await interaction.prompt({type:"text", message:"Proxy base URL:", placeholder:"https://proxy.example"});
   interaction.notify({type:"info", message:"Proxy " + baseUrl + " accepted"});
   const key = await interaction.prompt({type:"secret", message:"Proxy API key:"});
   return {type:"api_key", key, env:{SSO_PROXY_BASE_URL: baseUrl}};
  }},
  oauth:{name:"SSO Proxy SSO", loginLabel:"Sign in with SSO Proxy", login: async interaction => {
   interaction.notify({type:"progress", message:"Contacting SSO"});
   const team = await interaction.prompt({type:"select", message:"Choose a team:", options:[{id:"red", label:"Red team"}, {id:"blue", label:"Blue team"}]});
   return {type:"oauth", access:"token-" + team, refresh:"r", expires:Date.now() + 3600000};
  }, refresh: async credential => credential, toAuth: async credential => ({apiKey:credential.access})},
 }));
 pi.registerProvider(object("plain-proxy", "Plain Proxy", {apiKey:{name:"Plain Proxy key", resolve}}));
 pi.registerProvider(object("odd-proxy", "Odd Proxy", {apiKey:{name:"Odd Proxy key", resolve, login: async interaction => ({type:"api_key", key:await interaction.prompt({type:"picture", message:"Draw a key"})})}}));
 pi.registerProvider(object("loud-proxy", "Loud Proxy", {apiKey:{name:"Loud Proxy key", resolve, login: async interaction => { interaction.notify({type:"fireworks", message:"Bang"}); return {type:"api_key", key:"loud"}; }}}));
};
`

const nativeLoginGoFixture = `package fixture

import (
	"context"
	"errors"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

func object(id, name string, auth sdk.ProviderAuth) *sdk.Provider {
	unused := func(map[string]any, map[string]any, sdk.ProviderStreamOptions) (*sdk.ModelEventStream, error) { return nil, errors.New("unused") }
	model := map[string]any{"id": "sso-model", "name": "SSO Model", "provider": id, "api": "openai-completions", "baseUrl": "http://127.0.0.1:9", "reasoning": false, "input": []string{"text"}, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 100}
	return &sdk.Provider{ID: id, Name: name, Auth: auth, Stream: unused, StreamSimple: unused,
		GetModels: func() ([]map[string]any, error) { return []map[string]any{model}, nil }}
}

func resolve(input sdk.APIKeyAuthInput) (*sdk.AuthResult, error) {
	key, _ := input.Credential["key"].(string)
	if key == "" {
		return nil, nil
	}
	source := "stored credential"
	return &sdk.AuthResult{Auth: map[string]any{"apiKey": key}, Source: &source}, nil
}

func Extension() *sdk.Extension {
	e := sdk.New("native-login")
	label := "Sign in with SSO Proxy"
	for _, provider := range []*sdk.Provider{
		object("sso-proxy", "SSO Proxy", sdk.ProviderAuth{
			APIKey: &sdk.APIKeyAuth{Name: "SSO Proxy key", Resolve: resolve, Login: func(interaction sdk.AuthInteraction) (map[string]any, error) {
				baseURL, err := interaction.Prompt(map[string]any{"type": "text", "message": "Proxy base URL:", "placeholder": "https://proxy.example"})
				if err != nil {
					return nil, err
				}
				if err := interaction.Notify(map[string]any{"type": "info", "message": "Proxy " + baseURL + " accepted"}); err != nil {
					return nil, err
				}
				key, err := interaction.Prompt(map[string]any{"type": "secret", "message": "Proxy API key:"})
				if err != nil {
					return nil, err
				}
				return map[string]any{"type": "api_key", "key": key, "env": map[string]string{"SSO_PROXY_BASE_URL": baseURL}}, nil
			}},
			OAuth: &sdk.OAuthAuth{Name: "SSO Proxy SSO", LoginLabel: &label,
				Login: func(interaction sdk.AuthInteraction) (map[string]any, error) {
					if err := interaction.Notify(map[string]any{"type": "progress", "message": "Contacting SSO"}); err != nil {
						return nil, err
					}
					team, err := interaction.Prompt(map[string]any{"type": "select", "message": "Choose a team:", "options": []map[string]string{{"id": "red", "label": "Red team"}, {"id": "blue", "label": "Blue team"}}})
					if err != nil {
						return nil, err
					}
					return map[string]any{"type": "oauth", "access": "token-" + team, "refresh": "r", "expires": time.Now().Add(time.Hour).UnixMilli()}, nil
				},
				Refresh: func(credential map[string]any, _ context.Context) (map[string]any, error) { return credential, nil },
				ToAuth:  func(credential map[string]any) (map[string]any, error) { return map[string]any{"apiKey": credential["access"]}, nil },
			},
		}),
		object("plain-proxy", "Plain Proxy", sdk.ProviderAuth{APIKey: &sdk.APIKeyAuth{Name: "Plain Proxy key", Resolve: resolve}}),
		object("odd-proxy", "Odd Proxy", sdk.ProviderAuth{APIKey: &sdk.APIKeyAuth{Name: "Odd Proxy key", Resolve: resolve, Login: func(interaction sdk.AuthInteraction) (map[string]any, error) {
			key, err := interaction.Prompt(map[string]any{"type": "picture", "message": "Draw a key"})
			return map[string]any{"type": "api_key", "key": key}, err
		}}}),
		object("loud-proxy", "Loud Proxy", sdk.ProviderAuth{APIKey: &sdk.APIKeyAuth{Name: "Loud Proxy key", Resolve: resolve, Login: func(interaction sdk.AuthInteraction) (map[string]any, error) {
			if err := interaction.Notify(map[string]any{"type": "fireworks", "message": "Bang"}); err != nil {
				return nil, err
			}
			return map[string]any{"type": "api_key", "key": "loud"}, nil
		}}}),
	} {
		if err := e.RegisterNativeProvider(provider); err != nil {
			panic(err)
		}
	}
	return e
}
`

// nativeLoginSDKs are the SDKs whose provider objects the login tests load through the real host carrier.
var nativeLoginSDKs = []string{"node", "go"}

// loadNativeLoginFixture loads the fixture's provider objects into m's registry through a subprocess host, as Pi's
// registerNativeProvider adds them to the model runtime.
func loadNativeLoginFixture(t *testing.T, m *InteractiveMode, language string) {
	t.Helper()
	dir := t.TempDir()
	var config subprocess.ExtConfig
	switch language {
	case "node":
		if _, err := exec.LookPath("node"); err != nil {
			t.Fatalf("node is required: %v", err)
		}
		entry := filepath.Join(dir, "native-login.mjs")
		if err := os.WriteFile(entry, []byte(nativeLoginNodeFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		config = subprocess.ExtConfig{Name: "native-login", Source: entry, Enabled: true}
	case "go":
		config = goFactoryFixtureConfig(t, dir, "native-login", "example.com/nativelogin", nativeLoginGoFixture)
	default:
		t.Fatalf("unknown SDK %q", language)
	}
	host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetNativeProviderCallback(m.opts.ModelRegistry.RegisterNativeProvider)
	if _, errs := host.LoadAll(t.Context(), []subprocess.ExtConfig{config}); len(errs) != 0 {
		t.Fatalf("load %s fixture: %v", language, errs)
	}
	for _, id := range []string{"sso-proxy", "plain-proxy", "odd-proxy", "loud-proxy"} {
		if m.opts.ModelRegistry.NativeProvider(id) == nil {
			t.Fatalf("%s fixture did not register %s", language, id)
		}
	}
}

// goFactoryFixtureConfig writes source as an isolated Go SDK factory extension module in dir. The module builds against
// this tree's Go SDK, not an SDK an ambient PIG_SDK_GO_ROOT or an installed pig staged.
func goFactoryFixtureConfig(t *testing.T, dir, name, module, source string) subprocess.ExtConfig {
	t.Helper()
	root := testenv.ModuleRoot(t)
	t.Setenv("PIG_SDK_GO_ROOT", filepath.Join(root, "extensions", "sdk"))
	manifest, err := os.ReadFile(filepath.Join(root, "extensions", "sdk", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	goMod := strings.Replace(string(manifest), "module github.com/MichaelKinsy/PiG/extensions/sdk", "module "+module, 1) + "\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extension.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return subprocess.ExtConfig{Name: name, Enabled: true, Source: dir, RuntimeKind: "subprocess", RuntimeLanguage: "go",
		SDKName: "github.com/MichaelKinsy/PiG/extensions/sdk", Isolation: "isolated", EntrypointKind: "factory",
		ModulePath: module, Package: module, Factory: "Extension", ContentHash: name}
}

func newNativeLoginMode(t *testing.T, language string) *InteractiveMode {
	t.Helper()
	m := newPostLoginTestMode(t)
	m.runCtx = t.Context()
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	loadNativeLoginFixture(t, m, language)
	return m
}

func nativeLoginOptions(m *InteractiveMode, id string) []tui.OAuthProvider {
	return slices.DeleteFunc(m.getLoginProviderOptions(false), func(option tui.OAuthProvider) bool { return option.ID != id })
}

func dispatchLogin(t *testing.T, m *InteractiveMode, args string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- NewSlashRegistry().Dispatch(m.buildSlashContext(t.Context()), "/login "+args, nil) }()
	return done
}

func awaitLogin(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, errLoginCancelled) {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("/login did not return")
	}
}

func storedNativeCredential(t *testing.T, m *InteractiveMode, id string) (ai.Credential, bool) {
	t.Helper()
	auth, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	credential, ok, err := auth.GetRaw(id)
	if err != nil {
		t.Fatal(err)
	}
	return credential, ok
}

// Pi composes a provider object's own auth (model-runtime.ts registerNativeProvider, composeProvider's base): its
// declared OAuth method is the provider's account login, labelled by its loginLabel (interactive-mode.ts:5893-5895), and
// it stays offered after the refresh every registration queues. A provider object without an OAuth method has no account
// login.
func TestNativeProviderLoginOffersDeclaredMethods(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		t.Run(language, func(t *testing.T) {
			m := newNativeLoginMode(t, language)
			check := func(stage string) {
				t.Helper()
				options := nativeLoginOptions(m, "sso-proxy")
				var kinds []string
				for _, option := range options {
					kinds = append(kinds, option.AuthType)
					if option.AuthType == "oauth" {
						oauth, ok := option.Method.(*ai.OAuthAuth)
						if !ok || oauth.LoginLabel != "Sign in with SSO Proxy" || oauth.Name != "SSO Proxy SSO" {
							t.Fatalf("%s: OAuth method = %#v", stage, option.Method)
						}
					}
					if option.AuthType == "api_key" {
						key, ok := option.Method.(*ai.APIKeyAuth)
						if !ok || key.Name != "SSO Proxy key" {
							t.Fatalf("%s: API-key method = %#v", stage, option.Method)
						}
					}
				}
				if slices.Sort(kinds); !slices.Equal(kinds, []string{"api_key", "oauth"}) {
					t.Fatalf("%s: sso-proxy login options = %v, want oauth and api_key", stage, kinds)
				}
				for _, option := range nativeLoginOptions(m, "plain-proxy") {
					if option.AuthType == "oauth" {
						t.Fatalf("%s: plain-proxy declares no OAuth method but offers an account login", stage)
					}
				}
			}
			check("registered")
			if err := m.opts.ModelRegistry.refreshNativeProvider(t.Context(), m.opts.ModelRegistry.NativeProvider("sso-proxy"), false, nil); err != nil {
				t.Fatal(err)
			}
			check("refreshed")

			done := dispatchLogin(t, m, "SSO Proxy")
			waitForRender(t, m.editorContainer, "Select authentication method for SSO Proxy:")
			waitForRender(t, m.editorContainer, "Sign in with SSO Proxy")
			if got := plainRender(m.editorContainer); strings.Contains(got, "Sign in with an account") {
				t.Fatalf("the auth-type menu shows the generic account label instead of the flow's loginLabel:\n%s", got)
			}
			deliverModalInput(t, m, []byte("\x1b"))
			awaitLogin(t, done)
		})
	}
}

// Pi's showAuthPrompt opens a selector for a select prompt (interactive-mode.ts:6243-6276 showAuthSelect) and answers
// with the chosen option's id; cancelling the selector rejects with "Login cancelled", which ends the login and reopens
// the menu it was started from (interactive-mode.ts:6365-6366).
func TestNativeProviderOAuthLoginSelectPrompt(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		t.Run(language+"/choose", func(t *testing.T) {
			m := newNativeLoginMode(t, language)
			done := dispatchLogin(t, m, "SSO Proxy")
			waitForRender(t, m.editorContainer, "Sign in with SSO Proxy")
			deliverModalInput(t, m, []byte("\r"))
			waitForRender(t, m.editorContainer, "Choose a team:")
			waitForRender(t, m.editorContainer, "Blue team")
			deliverModalInput(t, m, []byte("\x1b[B"))
			deliverModalInput(t, m, []byte("\r"))
			awaitLogin(t, done)
			credential, ok := storedNativeCredential(t, m, "sso-proxy")
			if !ok || credential.Type != ai.CredentialOAuth || credential.Access != "token-blue" {
				t.Fatalf("stored credential = %+v (present %v), want the blue team's OAuth token", credential, ok)
			}
			if got := plainRender(m.chatContainer); strings.Contains(got, "Login cancelled") || strings.Contains(got, "Failed") {
				t.Fatalf("a completed login reported a failure:\n%s", got)
			}
		})
		t.Run(language+"/cancel", func(t *testing.T) {
			m := newNativeLoginMode(t, language)
			done := dispatchLogin(t, m, "SSO Proxy")
			waitForRender(t, m.editorContainer, "Sign in with SSO Proxy")
			deliverModalInput(t, m, []byte("\r"))
			waitForRender(t, m.editorContainer, "Choose a team:")
			deliverModalInput(t, m, []byte("\x1b"))
			waitForRender(t, m.editorContainer, "Select authentication method for SSO Proxy:")
			if got := plainRender(m.chatContainer); strings.Contains(got, "Failed") {
				t.Fatalf("a cancelled select reported a failure:\n%s", got)
			}
			deliverModalInput(t, m, []byte("\x1b"))
			awaitLogin(t, done)
			if credential, ok := storedNativeCredential(t, m, "sso-proxy"); ok {
				t.Fatalf("a cancelled login stored %+v", credential)
			}
		})
	}
}

// Pi's showApiKeyLoginDialog runs the provider's own apiKey.login with the dialog's prompts and events
// (interactive-mode.ts:6192-6241, 6315-6336). An answered prompt stays in the dialog as "> value" (login-dialog.ts:80-84
// replaceInputWithSubmittedText), so the base URL is still shown while the key is asked, and the stored credential keeps
// both values.
func TestNativeProviderAPIKeyLoginRunsDeclaredLogin(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		t.Run(language, func(t *testing.T) {
			m := newNativeLoginMode(t, language)
			done := dispatchLogin(t, m, "SSO Proxy")
			waitForRender(t, m.editorContainer, "Sign in with an API key")
			deliverModalInput(t, m, []byte("\x1b[B"))
			deliverModalInput(t, m, []byte("\r"))
			waitForRender(t, m.editorContainer, "Proxy base URL:")
			waitForRender(t, m.editorContainer, "e.g., https://proxy.example")
			deliverModalInput(t, m, []byte("https://sso.example/v1"))
			deliverModalInput(t, m, []byte("\r"))
			waitForRender(t, m.editorContainer, "Proxy API key:")
			got := plainRender(m.editorContainer)
			for _, want := range []string{"Proxy base URL:", "> https://sso.example/v1", "Proxy https://sso.example/v1 accepted"} {
				if !strings.Contains(got, want) {
					t.Fatalf("the dialog dropped %q while asking for the key:\n%s", want, got)
				}
			}
			if strings.Contains(got, "Enter API key") {
				t.Fatalf("the generic API-key prompt replaced the provider's own login:\n%s", got)
			}
			deliverModalInput(t, m, []byte("sk-proxy"))
			deliverModalInput(t, m, []byte("\r"))
			awaitLogin(t, done)
			credential, ok := storedNativeCredential(t, m, "sso-proxy")
			if !ok || credential.Type != ai.CredentialAPIKey || credential.Key != "sk-proxy" || credential.Env["SSO_PROXY_BASE_URL"] != "https://sso.example/v1" {
				t.Fatalf("stored credential = %+v (present %v), want the key and its base URL", credential, ok)
			}
		})
	}
}

// Without a declared apiKey.login the composed API-key method asks for the key with the generic secret prompt
// (provider-composer.ts composeApiKeyAuth: inherited?.login ?? the "Enter API key" prompt).
func TestNativeProviderAPIKeyLoginWithoutDeclaredLoginUsesGenericPrompt(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		t.Run(language, func(t *testing.T) {
			m := newNativeLoginMode(t, language)
			done := dispatchLogin(t, m, "Plain Proxy")
			waitForRender(t, m.editorContainer, "Enter API key")
			deliverModalInput(t, m, []byte("plain-key"))
			deliverModalInput(t, m, []byte("\r"))
			awaitLogin(t, done)
			credential, ok := storedNativeCredential(t, m, "plain-proxy")
			if !ok || credential.Key != "plain-key" {
				t.Fatalf("stored credential = %+v (present %v)", credential, ok)
			}
		})
	}
}

// AuthPrompt and AuthEvent are closed unions (auth/types.ts). A prompt or event type outside them fails the login with
// an error naming the type; it is never answered as another type or dropped.
func TestNativeProviderLoginRejectsUnknownInteractionTypes(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		for _, tc := range []struct{ provider, name, kind string }{
			{"odd-proxy", "Odd Proxy", `"picture"`},
			{"loud-proxy", "Loud Proxy", `"fireworks"`},
		} {
			t.Run(language+"/"+tc.provider, func(t *testing.T) {
				m := newNativeLoginMode(t, language)
				done := dispatchLogin(t, m, tc.name)
				awaitLogin(t, done)
				got := plainRender(m.chatContainer)
				if !strings.Contains(got, "Failed to save API key for "+tc.name) || !strings.Contains(got, tc.kind) {
					t.Fatalf("an unknown interaction type was not reported as a login failure:\n%s", got)
				}
				if credential, ok := storedNativeCredential(t, m, tc.provider); ok {
					t.Fatalf("a failed login stored %+v", credential)
				}
			})
		}
	}
}
