package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// packages/ai/src/compat/extension-oauth-types.ts:3-47 (legacy extension OAuthLoginCallbacks): every callback and every field of
// OAuthAuthInfo, OAuthDeviceCodeInfo, OAuthPrompt and OAuthSelectPrompt crosses the extension wire to the host login callbacks. The
// user's cancel ("Login cancelled") answers the extension with a cancel, which each SDK raises as "Login cancelled" as Pi's adaptOAuth
// rejects the legacy callback (provider-composer.ts:361-364); any other failing value callback is the call's error, as Pi's
// showAuthPrompt rejects the flow's await with it (interactive-mode.ts:6278-6300).
func TestOAuthProxy_LoginRelaysTheLegacyCallbackSurface(t *testing.T) {
	rig := newOAuthProxyRig(t)
	go rig.host.handleIncoming(rig.me, rig.me.connection())
	p := &oauthProxy{host: rig.host, me: rig.me, name: "example-provider", cfg: ProviderOAuthConfig{HasLogin: true}}

	var mu sync.Mutex
	var seen []any
	record := func(v any) { mu.Lock(); seen = append(seen, v); mu.Unlock() }
	cb := ai.OAuthLoginCallbacks{
		OnAuth:       func(i ai.OAuthAuthInfo) { record(i) },
		OnDeviceCode: func(i ai.OAuthDeviceCodeInfo) { record(i) },
		OnProgress:   func(m string) { record(m) },
		OnPrompt:     func(pr ai.OAuthPrompt) (string, error) { record(pr); return "prompted", nil },
		OnSelect: func(pr ai.OAuthSelectPrompt) (string, error) {
			record(pr)
			switch pr.Message {
			case "fail":
				return "", errors.New("closed")
			case "cancel":
				return "", errors.New("Login cancelled")
			}
			return pr.Options[1].ID, nil
		},
		OnManualCodeInput: func() (string, error) { record("manual"); return "pasted", nil },
	}
	done := make(chan error, 1)
	go func() { _, err := p.Login(cb); done <- err }()
	req := readFramed(t, rig.client)
	if req.Request == nil || req.Request.Method != MethodOAuthLogin {
		t.Fatalf("first request = %+v, want oauth_login", req.Request)
	}

	call := func(id, method string, args any) *CallResultPayload {
		t.Helper()
		raw, _ := json.Marshal(args)
		writeFramed(t, rig.client, Envelope{Type: MsgCall, ID: id, Call: &CallPayload{Method: method, Args: raw}})
		reply := readFramed(t, rig.client)
		if reply.Type != MsgCallResult || reply.ID != id || reply.CallResult == nil {
			t.Fatalf("%s reply = %+v", method, reply)
		}
		return reply.CallResult
	}
	input := func(r *CallResultPayload) OAuthInputResult {
		t.Helper()
		var out OAuthInputResult
		if err := json.Unmarshal(r.Result, &out); err != nil {
			t.Fatalf("input result %s: %v", r.Result, err)
		}
		return out
	}
	call("1", CallOAuthOnAuth, OAuthAuthInfoWire{URL: "https://auth.example/x", Instructions: "Sign in"})
	call("2", CallOAuthOnDeviceCode, OAuthDeviceCodeInfoWire{UserCode: "AB-12", VerificationURI: "https://verify.example", IntervalSeconds: 5, ExpiresInSeconds: 600})
	call("3", CallOAuthOnProgress, OAuthProgressWire{Message: "waiting"})
	if got := input(call("4", CallOAuthOnPrompt, OAuthPromptWire{Message: "Token", Placeholder: "ghp_", AllowEmpty: true})); got != (OAuthInputResult{Value: "prompted"}) {
		t.Errorf("prompt result = %+v", got)
	}
	options := []OAuthSelectOptionWire{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}}
	if got := input(call("5", CallOAuthOnSelect, OAuthSelectPromptWire{Message: "pick", Options: options})); got != (OAuthInputResult{Value: "b"}) {
		t.Errorf("select result = %+v", got)
	}
	if failed := call("6", CallOAuthOnSelect, OAuthSelectPromptWire{Message: "fail", Options: options}); failed.Error == nil || failed.Error.Message != "closed" || len(failed.Result) != 0 {
		t.Errorf("failed select result = %+v, want the handler's error", failed)
	}
	if got := input(call("6b", CallOAuthOnSelect, OAuthSelectPromptWire{Message: "cancel", Options: options})); got != (OAuthInputResult{Cancel: true}) {
		t.Errorf("cancelled select result = %+v, want a cancel", got)
	}
	if got := input(call("7", CallOAuthOnManualCodeInput, struct{}{})); got != (OAuthInputResult{Value: "pasted"}) {
		t.Errorf("manual code result = %+v", got)
	}
	credWire, _ := json.Marshal(OAuthCredentialsWire{Access: "tok", Expires: 1})
	writeFramed(t, rig.client, Envelope{Type: MsgResponse, ID: req.ID, Response: &ResponsePayload{Result: credWire}})
	if err := <-done; err != nil {
		t.Fatalf("login: %v", err)
	}

	selectOptions := []ai.OAuthSelectOption{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}}
	want := []any{
		ai.OAuthAuthInfo{URL: "https://auth.example/x", Instructions: "Sign in"},
		ai.OAuthDeviceCodeInfo{UserCode: "AB-12", VerificationURI: "https://verify.example", IntervalSeconds: 5, ExpiresInSeconds: 600},
		"waiting",
		ai.OAuthPrompt{Message: "Token", Placeholder: "ghp_", AllowEmpty: true},
		ai.OAuthSelectPrompt{Message: "pick", Options: selectOptions},
		ai.OAuthSelectPrompt{Message: "fail", Options: selectOptions},
		ai.OAuthSelectPrompt{Message: "cancel", Options: selectOptions},
		"manual",
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("host callbacks saw\n%#v\nwant\n%#v", seen, want)
	}
}

// Pi asks a legacy onManualCodeInput() as the manual_code prompt "Paste the authorization code" (provider-composer.ts:363
// adaptOAuth) and shows a provider object's own manual_code prompt with its message (interactive-mode.ts:6283-6284
// showAuthPrompt). Both reach the login dialog's manual code prompt.
func TestOAuthProxy_ManualCodeInputShowsPisPromptMessage(t *testing.T) {
	rig := newOAuthProxyRig(t)
	go rig.host.handleIncoming(rig.me, rig.me.connection())
	p := &oauthProxy{host: rig.host, me: rig.me, name: "example-provider", cfg: ProviderOAuthConfig{HasLogin: true}}
	var mu sync.Mutex
	var seen []ai.AuthManualCodePrompt
	cb := ai.OAuthLoginCallbacks{
		OnManualCodeInput: func() (string, error) { return "", errors.New("the prompt's message was dropped") },
		OnManualCodePromptContext: func(_ context.Context, prompt ai.AuthManualCodePrompt) (string, error) {
			mu.Lock()
			seen = append(seen, prompt)
			mu.Unlock()
			return "pasted", nil
		},
	}
	done := make(chan error, 1)
	go func() { _, err := p.Login(cb); done <- err }()
	req := readFramed(t, rig.client)
	for i, args := range []string{`null`, `{}`, `{"type":"manual_code","message":"Paste the SSO code:","placeholder":"sso-"}`} {
		id := fmt.Sprint("m", i)
		writeFramed(t, rig.client, Envelope{Type: MsgCall, ID: id, Call: &CallPayload{Method: CallOAuthOnManualCodeInput, Args: json.RawMessage(args)}})
		reply := readFramed(t, rig.client)
		if reply.CallResult == nil || reply.CallResult.Error != nil || string(reply.CallResult.Result) != `{"value":"pasted"}` {
			t.Fatalf("manual code %s reply = %+v", args, reply.CallResult)
		}
	}
	credWire, _ := json.Marshal(OAuthCredentialsWire{Access: "tok", Expires: 1})
	writeFramed(t, rig.client, Envelope{Type: MsgResponse, ID: req.ID, Response: &ResponsePayload{Result: credWire}})
	if err := <-done; err != nil {
		t.Fatalf("login: %v", err)
	}
	want := []ai.AuthManualCodePrompt{{Message: "Paste the authorization code"}, {Message: "Paste the authorization code"}, {Message: "Paste the SSO code:", Placeholder: "sso-"}}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("manual code prompts = %#v, want %#v", seen, want)
	}
}
