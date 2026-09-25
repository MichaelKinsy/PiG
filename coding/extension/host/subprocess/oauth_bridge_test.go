package subprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// oauthProxyRig wires a host-side Conn to a raw client net.Conn that a test
// drives as the extension side of the oauth protocol.
type oauthProxyRig struct {
	host   *Host
	me     *managedExt
	client net.Conn
}

func newOAuthProxyRig(t *testing.T) *oauthProxyRig {
	t.Helper()
	// net.Pipe avoids the ~104-char unix-socket path limit that long test names
	// blow past on macOS; framing works over any stream.
	serverConn, client := net.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	conn := NewConn("example-extension", serverConn)
	conn.Start(ctx)

	me := &managedExt{conn: conn, config: ExtConfig{Name: "example-extension"}}
	host := &Host{}

	t.Cleanup(func() {
		me.shuttingDown.Store(true)
		cancel()
		if err := client.Close(); err != nil {
			t.Errorf("close OAuth test peer: %v", err)
		}
		if err := conn.Close("test done"); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
			t.Errorf("close OAuth test host: %v", err)
		}
	})
	return &oauthProxyRig{host: host, me: me, client: client}
}

func writeFramed(t *testing.T, w net.Conn, env Envelope) {
	t.Helper()
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(data)))
	if _, err := w.Write(l[:]); err != nil {
		t.Fatalf("write len: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write payload: %v", err)
	}
}

func readFramed(t *testing.T, r net.Conn) Envelope {
	t.Helper()
	env, err := tryReadFramed(r)
	if err != nil {
		t.Fatalf("read framed: %v", err)
	}
	return env
}

// tryReadFramed reads one framed envelope, returning an error instead of failing
// the test, for looping fake-extension goroutines that exit when the host closes
// the connection at cleanup.
func tryReadFramed(r net.Conn) (Envelope, error) {
	_ = r.SetReadDeadline(time.Now().Add(3 * time.Second))
	var l [4]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return Envelope{}, err
	}
	buf := make([]byte, binary.BigEndian.Uint32(l[:]))
	if _, err := io.ReadFull(r, buf); err != nil {
		return Envelope{}, err
	}
	var env Envelope
	if err := json.Unmarshal(buf, &env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func TestOAuthCallbackInitiationFollowsRealCallbackEntry(t *testing.T) {
	tests := []struct {
		name string
		run  func(oauthLoginSession, context.Context) error
	}{
		{name: "prompt", run: func(session oauthLoginSession, ctx context.Context) error {
			_, err := session.OnPrompt(ctx, ai.OAuthPrompt{})
			return err
		}},
		{name: "select", run: func(session oauthLoginSession, ctx context.Context) error {
			_, err := session.OnSelect(ctx, ai.OAuthSelectPrompt{})
			return err
		}},
		{name: "manual code", run: func(session oauthLoginSession, ctx context.Context) error {
			_, err := session.OnManualCodeInput(ctx)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entered := false
			callback := func() (string, error) {
				entered = true
				return "ok", nil
			}
			session := initiatedOAuthLoginSession(ai.OAuthLoginCallbacks{
				OnPrompt:          func(ai.OAuthPrompt) (string, error) { return callback() },
				OnSelect:          func(ai.OAuthSelectPrompt) (string, error) { return callback() },
				OnManualCodeInput: callback,
			})
			ctx := extension.WithCallInitiation(context.Background(), func() {
				if !entered {
					t.Error("OAuth lane released before real callback entry")
				}
			})
			if err := test.run(session, ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// GetAPIKey is deterministic in credentials, so the proxy caches the resolved
// bearer and only re-RPCs when credentials change.
func TestOAuthProxy_GetAPIKeyCachesPerCredential(t *testing.T) {
	rig := newOAuthProxyRig(t)
	p := &oauthProxy{host: rig.host, me: rig.me, name: "example-extension", cfg: ProviderOAuthConfig{HasGetAPIKey: true}}

	var reqCount atomic.Int32
	go func() {
		for {
			req, err := tryReadFramed(rig.client)
			if err != nil || req.Request == nil || req.Request.Method != MethodOAuthGetAPIKey {
				return
			}
			reqCount.Add(1)
			var w OAuthCredentialsWire
			_ = json.Unmarshal(req.Request.Args, &w)
			res, _ := json.Marshal(OAuthAPIKeyResult{APIKey: "key-for-" + w.Access})
			writeFramed(t, rig.client, Envelope{Type: MsgResponse, ID: req.ID, Response: &ResponsePayload{Result: res}})
		}
	}()

	creds1 := ai.OAuthCredentials{Access: "a1", Expires: 1}
	if got := p.GetAPIKey(creds1); got != "key-for-a1" {
		t.Fatalf("GetAPIKey(a1) = %q", got)
	}
	if got := p.GetAPIKey(creds1); got != "key-for-a1" { // served from cache
		t.Fatalf("GetAPIKey(a1) cached = %q", got)
	}
	if got := p.GetAPIKey(ai.OAuthCredentials{Access: "a2", Expires: 2}); got != "key-for-a2" {
		t.Fatalf("GetAPIKey(a2) = %q", got)
	}
	if n := reqCount.Load(); n != 2 {
		t.Fatalf("get_api_key RPCs = %d, want 2 (the repeat of a1 must hit the cache)", n)
	}
}

// Login publishes the host callbacks, sends oauth_login, and while it is in
// flight the extension's oauth.cb.* calls route back through handleIncoming to
// those callbacks; a value-returning prompt round-trips to the extension.
func TestOAuthProxy_LoginRelaysCallbacks(t *testing.T) {
	rig := newOAuthProxyRig(t)
	go rig.host.handleIncoming(rig.me)

	p := &oauthProxy{host: rig.host, me: rig.me, name: "example-provider", cfg: ProviderOAuthConfig{HasLogin: true}}

	// The provider name ("example-provider") is deliberately distinct from the
	// extension name (rig.me.config.Name == "example-extension"): the login session must be
	// keyed by extension name so the oauth.cb.* calls, which carry only the
	// extension identity, route back. Equal names would mask that regression.

	deviceCh := make(chan ai.OAuthDeviceCodeInfo, 1)
	cb := ai.OAuthLoginCallbacks{
		OnDeviceCode: func(i ai.OAuthDeviceCodeInfo) { deviceCh <- i },
		OnPrompt:     func(pr ai.OAuthPrompt) (string, error) { return "typed:" + pr.Message, nil },
	}

	type loginRes struct {
		creds ai.OAuthCredentials
		err   error
	}
	done := make(chan loginRes, 1)
	go func() {
		creds, err := p.Login(cb)
		done <- loginRes{creds, err}
	}()

	// Extension side.
	req := readFramed(t, rig.client)
	if req.Request == nil || req.Request.Method != MethodOAuthLogin {
		t.Fatalf("first request = %+v, want oauth_login", req.Request)
	}
	// Fire onDeviceCode, then read its ack.
	dc, _ := json.Marshal(OAuthDeviceCodeInfoWire{UserCode: "WXYZ", VerificationURI: "https://verify"})
	writeFramed(t, rig.client, Envelope{Type: MsgCall, ID: "cb1", Call: &CallPayload{Method: CallOAuthOnDeviceCode, Args: dc}})
	if ack := readFramed(t, rig.client); ack.Type != MsgCallResult || ack.ID != "cb1" {
		t.Fatalf("onDeviceCode ack = %+v", ack)
	}
	// Prompt, then read the returned value.
	pr, _ := json.Marshal(OAuthPromptWire{Message: "URL"})
	writeFramed(t, rig.client, Envelope{Type: MsgCall, ID: "cb2", Call: &CallPayload{Method: CallOAuthOnPrompt, Args: pr}})
	promptRes := readFramed(t, rig.client)
	if promptRes.CallResult == nil || promptRes.CallResult.Result == nil {
		t.Fatalf("onPrompt result = %+v", promptRes.CallResult)
	}
	var inp OAuthInputResult
	if err := json.Unmarshal(promptRes.CallResult.Result, &inp); err != nil {
		t.Fatalf("unmarshal prompt result: %v", err)
	}
	// Respond to login with creds echoing the prompt value.
	credWire, _ := json.Marshal(OAuthCredentialsWire{Access: inp.Value, Expires: 999})
	writeFramed(t, rig.client, Envelope{Type: MsgResponse, ID: req.ID, Response: &ResponsePayload{Result: credWire}})

	select {
	case dc := <-deviceCh:
		if dc.UserCode != "WXYZ" || dc.VerificationURI != "https://verify" {
			t.Fatalf("device code not relayed to host callback: %+v", dc)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("onDeviceCode never reached the host callback")
	}

	res := <-done
	if res.err != nil {
		t.Fatalf("login error: %v", res.err)
	}
	if res.creds.Access != "typed:URL" {
		t.Fatalf("login creds Access = %q, want the round-tripped prompt value", res.creds.Access)
	}
}

// RefreshToken RPCs oauth_refresh and returns the refreshed credentials.
func TestOAuthProxy_RefreshToken(t *testing.T) {
	rig := newOAuthProxyRig(t)
	p := &oauthProxy{host: rig.host, me: rig.me, name: "example-extension", cfg: ProviderOAuthConfig{HasRefresh: true}}

	go func() {
		req := readFramed(t, rig.client)
		if req.Request == nil || req.Request.Method != MethodOAuthRefresh {
			return
		}
		res, _ := json.Marshal(OAuthCredentialsWire{Access: "fresh", Refresh: "r2", Expires: 42})
		writeFramed(t, rig.client, Envelope{Type: MsgResponse, ID: req.ID, Response: &ResponsePayload{Result: res}})
	}()

	got, err := p.RefreshToken(ai.OAuthCredentials{Access: "stale", Refresh: "r1"})
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if got.Access != "fresh" || got.Refresh != "r2" || got.Expires != 42 {
		t.Fatalf("RefreshToken = %+v", got)
	}
}

// A provider that does not opt into a credential store must not satisfy
// ai.OAuthCredentialStore, so core falls back to auth.json; one that does must.
func TestOAuthProxy_CredentialStoreIsConditional(t *testing.T) {
	base := &oauthProxy{name: "example-extension", cfg: ProviderOAuthConfig{}}
	if _, ok := any(base).(ai.OAuthCredentialStore); ok {
		t.Fatal("plain oauthProxy must not implement OAuthCredentialStore")
	}
	withStore := &oauthProxyWithStore{oauthProxy: base}
	if _, ok := any(withStore).(ai.OAuthCredentialStore); !ok {
		t.Fatal("oauthProxyWithStore must implement OAuthCredentialStore")
	}
}
