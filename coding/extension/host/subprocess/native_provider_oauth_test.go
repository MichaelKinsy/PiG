package subprocess_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	services, err := coding.NewServices(coding.ServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
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
	host.SetProviderCallbacks(services.Registry().RegisterProvider, services.Registry().UnregisterProvider)
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
