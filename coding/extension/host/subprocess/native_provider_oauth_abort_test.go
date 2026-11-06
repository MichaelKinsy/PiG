//go:build !pig_strip_node_extensions

package subprocess_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// nativeOAuthAbortFixture starts a subprocess extension whose OAuth provider "native-oauth" has a slow token endpoint: refresh() writes the "started" file and answers 600 ms later with a rotated token. The stored credential is expired, so the request-time auth refreshes it.
func nativeOAuthAbortFixture(t *testing.T) (services *coding.AgentSessionServices, started string) {
	t.Helper()
	dir := t.TempDir()
	started = filepath.Join(dir, "refresh-started")
	entry := filepath.Join(dir, "oauth-abort.mjs")
	// A JSON string is a JavaScript string literal (JSON.stringify), so a Windows path's backslashes are escaped rather than read as escape sequences.
	startedLiteral, err := json.Marshal(started)
	if err != nil {
		t.Fatal(err)
	}
	source := `import {writeFileSync} from "node:fs";
export default pi=>{
 const auth={name:"Native OAuth",isSubscription:true,login:async()=>{throw new Error("unused")},refresh:async credential=>{writeFileSync(` + string(startedLiteral) + `,"started");await new Promise(r=>setTimeout(r,600));return {...credential,access:"new-access",refresh:"new-refresh",expires:Date.now()+3600000}},toAuth:async credential=>({apiKey:credential.access})};
 pi.registerProvider({id:"native-oauth",name:"Native OAuth",auth:{oauth:auth},getModels:()=>[],stream(){throw new Error("unused")},streamSimple(){throw new Error("unused")}});
};`
	if err := os.WriteFile(entry, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	services, err = coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if err := services.Auth().Set("native-oauth", ai.Credential{Type: ai.CredentialOAuth, Access: "old-access", Refresh: "old-refresh", Expires: 1}); err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(dir)
	t.Cleanup(func() { host.Shutdown("done") })
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	host.SetUIBridge(subprocess.NewUIBridge(nil))
	if _, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "oauth-abort", Source: entry, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	applyQueuedRegistrations(host)
	return services, started
}

// The abort arrives while the extension's OAuth refresh is in flight. The refresh that ai.ResolveProviderAuth started runs its Modify on the registry's RuntimeCredentials, which tracks it, so the rotated token reaches auth.json although the caller returned at once, and an exit waits for it (Settled) for at most the 15 s refresh timeout (Pi: oauth.refresh runs under modify with AbortSignal.timeout(15 s), resolve.ts:119-155).
func TestNativeProviderAuthSavesAnExtensionsRefreshAbortedMidRequest(t *testing.T) {
	services, started := nativeOAuthAbortFixture(t)
	callCtx, cancelCall := context.WithCancel(t.Context())
	defer cancelCall()
	returned := make(chan error, 1)
	go func() {
		_, err := services.Registry().NativeProviderAuth(callCtx, "native-oauth", ai.AuthResolutionOverrides{})
		returned <- err
	}()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the extension's refresh never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancelCall()
	if err := <-returned; err == nil {
		t.Fatal("the aborted caller got no error")
	}
	begin := time.Now()
	if err := services.Registry().SettleCredentialWrites(t.Context()); err != nil {
		t.Fatalf("SettleCredentialWrites = %v", err)
	}
	if waited := time.Since(begin); waited > 15*time.Second+time.Second {
		t.Fatalf("exit waited %s, more than the refresh timeout", waited)
	}
	stored, err := services.Auth().Read(t.Context(), "native-oauth")
	if err != nil || stored == nil || stored.Refresh != "new-refresh" || stored.Access != "new-access" {
		t.Fatalf("auth.json after the exit wait = %+v, %v; want the rotated token", stored, err)
	}
}

// A caller cancelled before the call starts no refresh: no token request is made and the credential is untouched (resolveProviderAuth's signal.throwIfAborted(), resolve.ts:53). The abort that arrives between the read and the transaction is covered in-process by TestNativeProviderAuthNeverStartsARefreshTheAbortBeatInsideTheTransaction.
func TestNativeProviderAuthStartsNoRefreshForACallerThatWasAlreadyCancelled(t *testing.T) {
	services, started := nativeOAuthAbortFixture(t)
	callCtx, cancelCall := context.WithCancel(t.Context())
	cancelCall()
	if _, err := services.Registry().NativeProviderAuth(callCtx, "native-oauth", ai.AuthResolutionOverrides{}); err == nil {
		t.Fatal("an aborted caller got no error")
	}
	if err := services.Registry().SettleCredentialWrites(t.Context()); err != nil {
		t.Fatalf("SettleCredentialWrites = %v", err)
	}
	time.Sleep(900 * time.Millisecond)
	if _, err := os.Stat(started); err == nil {
		t.Fatal("a refresh the abort beat was started")
	}
	stored, err := services.Auth().Read(t.Context(), "native-oauth")
	if err != nil || stored == nil || stored.Refresh != "old-refresh" {
		t.Fatalf("stored credential = %+v, %v; want it untouched", stored, err)
	}
}
