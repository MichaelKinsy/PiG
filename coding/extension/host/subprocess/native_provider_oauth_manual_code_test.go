//go:build !pig_strip_node_extensions

package subprocess_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi's interactive login answers a manual_code prompt with dialog.showManualInput(prompt.message)
// (interactive-mode.ts:6282-6283), so the message an extension flow supplies reaches the user. A native provider's
// login prompt of type manual_code crosses the wire as oauth.cb.onManualCodeInput; the host answers it with the
// caller's manual-code prompt callback and the flow's own message and placeholder, and only a flow that sends none
// falls back to the caller's plain manual-code input.
func TestNativeProviderOAuthManualCodePromptKeepsTheFlowsMessage(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "oauth-manual.mjs")
	source := `export default pi=>{
 const auth={name:"Manual OAuth",login:async interaction=>({type:"oauth",access:await interaction.prompt({type:"manual_code",message:"Enter the emailed code",placeholder:"123456"}),refresh:"r",expires:Date.now()+3600000}),refresh:async credential=>credential,toAuth:async credential=>({apiKey:credential.access})};
 pi.registerProvider({id:"manual-oauth",name:"Manual OAuth",auth:{oauth:auth},getModels:()=>[],stream(){throw new Error("unused")},streamSimple(){throw new Error("unused")}});
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
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	host.SetUIBridge(subprocess.NewUIBridge(nil))
	if _, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "oauth-manual", Source: entry, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	provider, ok := ai.GetOAuthProvider("manual-oauth")
	if !ok {
		t.Fatal("native OAuth absent from login registry")
	}
	contextual, ok := provider.(interface {
		LoginContext(context.Context, ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error)
	})
	if !ok {
		t.Fatal("native login has no cancellation context")
	}
	var got []ai.AuthManualCodePrompt
	plain := 0
	loggedIn, err := contextual.LoginContext(t.Context(), ai.OAuthLoginCallbacks{
		OnManualCodePromptContext: func(_ context.Context, prompt ai.AuthManualCodePrompt) (string, error) {
			got = append(got, prompt)
			return "from-prompt", nil
		},
		OnManualCodeInputContext: func(context.Context) (string, error) {
			plain++
			return "from-plain-input", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (ai.AuthManualCodePrompt{Message: "Enter the emailed code", Placeholder: "123456"}); len(got) != 1 || got[0] != want {
		t.Fatalf("manual-code prompt callback got %+v, want exactly [%+v]", got, want)
	}
	if plain != 0 || loggedIn.Access != "from-prompt" {
		t.Fatalf("plain manual-code input ran %d times and the credential holds %q; the prompt callback answers a flow's manual_code prompt", plain, loggedIn.Access)
	}
}
