//go:build !pig_strip_node_extensions

package subprocess_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

func TestNativeProviderOAuthRefreshAndLogin(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "oauth-native.mjs")
	source := `export default pi=>{
 const auth={name:"Native OAuth",isSubscription:true,login:async interaction=>({type:"oauth",access:await interaction.prompt({type:"secret",message:"Native token"}),refresh:"r",expires:Date.now()+3600000,account:{id:"native-account"}}),refresh:async credential=>{if(credential.account.id!=="native-account")throw new Error("lost native credential fields");return {...credential,access:"rotated",expires:Date.now()+3600000}},toAuth:async credential=>({apiKey:credential.access,headers:{"X-Account":credential.account.id}})};
 pi.registerProvider({id:"native-oauth",name:"Native OAuth",auth:{oauth:auth},getModels:()=>[],stream(){throw new Error("unused")},streamSimple(){throw new Error("unused")}});
};`
	if err := os.WriteFile(entry, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	var credential ai.Credential
	if err := json.Unmarshal([]byte(`{"type":"oauth","access":"old","refresh":"r","expires":1,"account":{"id":"native-account"}}`), &credential); err != nil {
		t.Fatal(err)
	}
	if err := services.Auth().Set("native-oauth", credential); err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(dir)
	defer host.Shutdown("done")
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	host.SetUIBridge(subprocess.NewUIBridge(nil))
	if _, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "oauth-native", Source: entry, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	result, err := services.Registry().NativeProviderAuth(t.Context(), "native-oauth", ai.AuthResolutionOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Auth.APIKey != "rotated" || *result.Auth.Headers["X-Account"] != "native-account" {
		t.Fatalf("auth: %+v", result)
	}
	stored, err := services.Auth().Read(t.Context(), "native-oauth")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Access != "rotated" || !strings.Contains(string(stored.Extra["account"]), "native-account") {
		t.Fatalf("rotated credential: %+v", stored)
	}
	provider, ok := ai.GetOAuthProvider("native-oauth")
	if !ok {
		t.Fatal("native OAuth absent from login registry")
	}
	callbacks := ai.OAuthLoginCallbacks{OnPrompt: func(prompt ai.OAuthPrompt) (string, error) {
		if prompt.Message != "Native token" {
			t.Errorf("prompt: %+v", prompt)
		}
		return "login-token", nil
	}}
	contextual, ok := provider.(interface {
		LoginContext(context.Context, ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error)
	})
	if !ok {
		t.Fatal("native login has no cancellation context")
	}
	loggedIn, err := contextual.LoginContext(t.Context(), callbacks)
	if err != nil {
		t.Fatal(err)
	}
	if loggedIn.Access != "login-token" || !strings.Contains(string(loggedIn.Extra["account"]), "native-account") {
		t.Fatalf("login: %+v", loggedIn)
	}
}

// The account login of a provider object answers a select prompt with the login's select callback and a cancelled select
// rejects the flow with "Login cancelled" (interactive-mode.ts:6198-6229). A prompt or event type outside Pi's
// AuthInteraction fails the login instead of reaching another callback (auth/types.ts AuthPrompt, AuthEvent).
func TestNativeProviderOAuthLoginCallbacks(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "oauth-select.mjs")
	source := `export default pi=>{
 const object=(id, login)=>({id,name:id,auth:{oauth:{name:id,login,refresh:async c=>c,toAuth:async c=>({apiKey:c.access})}},getModels:()=>[],stream(){throw new Error("unused")},streamSimple(){throw new Error("unused")}});
 pi.registerProvider(object("select-oauth", async interaction=>{
  const team=await interaction.prompt({type:"select",message:"Team",options:[{id:"red",label:"Red"},{id:"blue",label:"Blue"}]});
  return {type:"oauth",access:"token-"+team,refresh:"r",expires:Date.now()+3600000};
 }));
 pi.registerProvider(object("picture-oauth", async interaction=>({type:"oauth",access:await interaction.prompt({type:"picture",message:"Draw"}),refresh:"r",expires:1})));
 pi.registerProvider(object("info-oauth", async interaction=>{
  interaction.notify({type:"info",message:"Use your work account",links:[{url:"https://example.test/help",label:"Help"},{url:"https://example.test/terms"}]});
  interaction.notify({type:"progress",message:"Checking"});
  const team=await interaction.prompt({type:"select",message:"Team",options:[{id:"red",label:"Red",description:"The red team"}]});
  return {type:"oauth",access:"token-"+team,refresh:"r",expires:1};
 }));
 pi.registerProvider(object("fireworks-oauth", async interaction=>{interaction.notify({type:"fireworks"});return {type:"oauth",access:"a",refresh:"r",expires:1};}));
};`
	if err := os.WriteFile(entry, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHost(dir)
	defer host.Shutdown("done")
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	if _, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "oauth-select", Source: entry, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	login := func(id string, callbacks ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
		t.Helper()
		provider, ok := ai.GetOAuthProvider(id)
		if !ok {
			t.Fatalf("%s absent from the login registry", id)
		}
		return provider.(interface {
			LoginContext(context.Context, ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error)
		}).LoginContext(t.Context(), callbacks)
	}
	var asked ai.OAuthSelectPrompt
	choose := func(_ context.Context, prompt ai.OAuthSelectPrompt) (string, error) {
		asked = prompt
		return prompt.Options[1].ID, nil
	}
	credentials, err := login("select-oauth", ai.OAuthLoginCallbacks{OnSelectContext: choose})
	if err != nil || credentials.Access != "token-blue" {
		t.Fatalf("login = %+v, %v", credentials, err)
	}
	if asked.Message != "Team" || len(asked.Options) != 2 || asked.Options[0] != (ai.OAuthSelectOption{ID: "red", Label: "Red"}) {
		t.Fatalf("select prompt = %+v", asked)
	}
	cancel := func(context.Context, ai.OAuthSelectPrompt) (string, error) { return "", errors.New("Login cancelled") }
	if _, err := login("select-oauth", ai.OAuthLoginCallbacks{OnSelectContext: cancel}); err == nil || err.Error() != "Login cancelled" {
		t.Fatalf("a cancelled select returned %v, want Login cancelled", err)
	}
	// A provider object's login takes Pi's AuthInteraction (interactive-mode.ts:6315-6336 loginProvider): every prompt and event reaches it as the flow sent it, an info event with its links and a select option with its description.
	provider, ok := ai.GetOAuthProvider("info-oauth")
	if !ok {
		t.Fatal("info-oauth absent from the login registry")
	}
	var events []ai.AuthEvent
	var prompts []ai.AuthPrompt
	interaction := ai.AuthInteraction{
		Notify: func(event ai.AuthEvent) { events = append(events, event) },
		Prompt: func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
			prompts = append(prompts, prompt)
			return "red", nil
		},
	}
	credentials, err = provider.(interface {
		LoginInteraction(context.Context, ai.AuthInteraction) (ai.OAuthCredentials, error)
	}).LoginInteraction(t.Context(), interaction)
	if err != nil || credentials.Access != "token-red" {
		t.Fatalf("login = %+v, %v", credentials, err)
	}
	wantEvents := []ai.AuthEvent{
		ai.AuthInfoEvent{Message: "Use your work account", Links: []ai.AuthInfoLink{{URL: "https://example.test/help", Label: "Help"}, {URL: "https://example.test/terms"}}},
		ai.AuthProgressEvent{Message: "Checking"},
	}
	wantPrompts := []ai.AuthPrompt{ai.AuthSelectPrompt{Message: "Team", Options: []ai.AuthSelectOption{{ID: "red", Label: "Red", Description: "The red team"}}}}
	if !reflect.DeepEqual(events, wantEvents) || !reflect.DeepEqual(prompts, wantPrompts) {
		t.Fatalf("events = %+v, prompts = %+v; want %+v and %+v", events, prompts, wantEvents, wantPrompts)
	}
	unexpected := ai.OAuthLoginCallbacks{
		OnPromptContext: func(context.Context, ai.OAuthPrompt) (string, error) {
			return "", errors.New("unknown prompt reached the text callback")
		},
		OnProgress: func(string) { t.Error("unknown event reached the progress callback") },
	}
	if _, err := login("picture-oauth", unexpected); err == nil || !strings.Contains(err.Error(), `unknown login prompt type "picture"`) {
		t.Fatalf("an unknown prompt type returned %v", err)
	}
	if _, err := login("fireworks-oauth", unexpected); err == nil || !strings.Contains(err.Error(), `unknown login event type "fireworks"`) {
		t.Fatalf("an unknown event type returned %v", err)
	}
}
