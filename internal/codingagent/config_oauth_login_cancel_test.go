package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
)

// A config.oauth provider (registerProvider(name, {oauth})) whose login asks a select prompt and lets the user's cancel
// propagate. Its flow throws when the cancel resolves instead of rejecting, so a resolved cancel shows as a failure.
const configOAuthCancelNodeFixture = `export default pi => {
 pi.registerProvider("cfg-sso", {name:"Cfg SSO", baseUrl:"http://127.0.0.1:9", api:"openai-completions", apiKey:"CFG_SSO_KEY",
  models:[{id:"m", name:"M", reasoning:false, input:["text"], cost:{input:0,output:0,cacheRead:0,cacheWrite:0}, contextWindow:4000, maxTokens:100}],
  oauth:{name:"Cfg SSO", login: async callbacks => {
   const team = await callbacks.onSelect({message:"Choose a team:", options:[{id:"red", label:"Red team"}, {id:"blue", label:"Blue team"}]});
   if (team === undefined) throw new Error("the cancelled select resolved");
   return {access:"t-" + team, refresh:"r", expires:Date.now() + 3600000};
  }, refreshToken: async credentials => credentials, getApiKey: credentials => credentials.access}});
};
`

const configOAuthCancelGoFixture = `package fixture

import "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New("cfg-oauth")
	e.RegisterProvider("cfg-sso", sdk.ProviderConfig{
		"name": "Cfg SSO", "baseUrl": "http://127.0.0.1:9", "api": "openai-completions", "apiKey": "CFG_SSO_KEY",
		"models": []map[string]any{{"id": "m", "name": "M", "reasoning": false, "input": []string{"text"}, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 100}},
		"oauth": &sdk.OAuthProvider{Name: "Cfg SSO",
			Login: func(cb *sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
				team, err := cb.OnSelect(sdk.OAuthSelectPrompt{Message: "Choose a team:", Options: []sdk.OAuthSelectOption{{ID: "red", Label: "Red team"}, {ID: "blue", Label: "Blue team"}}})
				if err != nil {
					return sdk.OAuthCredentials{}, err
				}
				return sdk.OAuthCredentials{Access: "t-" + team, Refresh: "r"}, nil
			},
			RefreshToken: func(c sdk.OAuthCredentials) (sdk.OAuthCredentials, error) { return c, nil },
			GetAPIKey:    func(c sdk.OAuthCredentials) string { return c.Access },
		},
	})
	return e
}
`

// Pi adapts a config.oauth login's legacy callbacks to the login interaction's prompt (provider-composer.ts:361-364
// adaptOAuth: onSelect is prompt({type: "select"})). Cancelling the selector rejects that prompt with "Login cancelled"
// (interactive-mode.ts:6243-6276 showAuthSelect), the flow rejects with it, and /login reopens the menu the login was
// started from instead of reporting a failure (interactive-mode.ts:6365-6366).
func TestConfigOAuthLoginSelectCancelReturnsToMenu(t *testing.T) {
	for _, language := range nativeLoginSDKs {
		t.Run(language, func(t *testing.T) {
			m := newPostLoginTestMode(t)
			m.runCtx = t.Context()
			m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
			dir := t.TempDir()
			var config subprocess.ExtConfig
			switch language {
			case "node":
				entry := filepath.Join(dir, "cfg-oauth.mjs")
				if err := os.WriteFile(entry, []byte(configOAuthCancelNodeFixture), 0o600); err != nil {
					t.Fatal(err)
				}
				config = subprocess.ExtConfig{Name: "cfg-oauth", Source: entry, Enabled: true}
			case "go":
				config = goFactoryFixtureConfig(t, dir, "cfg-oauth", "example.com/cfgoauth", configOAuthCancelGoFixture)
			}
			host := subprocess.NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { host.Shutdown("test done") })
			host.SetProviderCallbacks(m.opts.ModelRegistry.RegisterExtensionProvider, m.opts.ModelRegistry.UnregisterProvider)
			if _, errs := host.LoadAll(t.Context(), []subprocess.ExtConfig{config}); len(errs) != 0 {
				t.Fatalf("load %s fixture: %v", language, errs)
			}
			done := dispatchLogin(t, m, "Cfg SSO")
			waitForRender(t, m.editorContainer, "Select authentication method for Cfg SSO:")
			deliverModalInput(t, m, []byte("\r"))
			waitForRender(t, m.editorContainer, "Choose a team:")
			deliverModalInput(t, m, []byte("\x1b"))
			waitForRender(t, m.editorContainer, "Select authentication method for Cfg SSO:")
			if got := plainRender(m.chatContainer) + plainRender(m.editorContainer); strings.Contains(got, "failed") || strings.Contains(got, "Failed") {
				t.Fatalf("a cancelled select reported a failure:\n%s", got)
			}
			deliverModalInput(t, m, []byte("\x1b"))
			awaitLogin(t, done)
			if credential, ok := storedNativeCredential(t, m, "cfg-sso"); ok {
				t.Fatalf("a cancelled login stored %+v", credential)
			}
		})
	}
}
