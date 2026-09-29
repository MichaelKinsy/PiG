package subprocess

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// refreshRig answers each oauth_refresh with fresh credentials and reports every later frame the host writes.
func refreshRig(t *testing.T) (p *oauthProxy, rig *oauthProxyRig, requests <-chan Envelope, frames <-chan Envelope) {
	t.Helper()
	rig = newOAuthProxyRig(t)
	p = &oauthProxy{host: rig.host, me: rig.me, name: "example-extension", cfg: ProviderOAuthConfig{HasRefresh: true}}
	req, out := make(chan Envelope, 8), make(chan Envelope, 8)
	go func() {
		for {
			env, err := tryReadFramed(rig.client)
			if err != nil {
				return
			}
			if env.Type == MsgRequest {
				req <- env
				res, _ := json.Marshal(OAuthCredentialsWire{Access: "fresh", Refresh: "r2", Expires: 42})
				writeFramed(t, rig.client, Envelope{Type: MsgResponse, ID: env.ID, Response: &ResponsePayload{Result: res}})
				continue
			}
			out <- env
		}
	}()
	return p, rig, req, out
}

func expectCancelFrame(t *testing.T, frames <-chan Envelope, id string) {
	t.Helper()
	select {
	case env := <-frames:
		if env.Type != MsgCancel || env.Cancel == nil || env.Cancel.RequestID != id {
			t.Fatalf("frame = %+v, want a cancel for %s", env, id)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no cancel frame for the retained signal of %s", id)
	}
}

func expectNoFrame(t *testing.T, frames <-chan Envelope) {
	t.Helper()
	select {
	case env := <-frames:
		t.Fatalf("unexpected frame %+v", env)
	case <-time.After(150 * time.Millisecond):
	}
}

// Pi composes AbortSignal.timeout into the refresh signal only in resolveStoredOAuth (auth/resolve.ts:149-153); Models.refresh passes the caller's signal alone (models.ts:474).
func TestOAuthProxyRefreshSignalTimeoutFollowsTheCaller(t *testing.T) {
	p, _, requests, _ := refreshRig(t)
	caller, cancel := context.WithCancel(t.Context())
	defer cancel()

	if _, err := p.RefreshTokenContext(caller, ai.OAuthCredentials{Refresh: "r"}); err != nil {
		t.Fatal(err)
	}
	if got := (<-requests).Request.SignalTimeoutMS; got != nil {
		t.Errorf("Models.refresh path sent signal_timeout_ms = %v", *got)
	}
	if _, err := p.RefreshTokenContext(ai.WithOAuthRefreshTimeout(caller, caller, 15*time.Second), ai.OAuthCredentials{Refresh: "r"}); err != nil {
		t.Fatal(err)
	}
	if got := (<-requests).Request.SignalTimeoutMS; got == nil || *got != 15000 {
		t.Errorf("resolveStoredOAuth path signal_timeout_ms = %v, want 15000", got)
	}
}

// A signal the extension keeps after its request settled still follows the caller: Pi's refresh signal is AbortSignal.any([caller, ...]). The composed timeout is the runtime's own timer, so the refresh context's deadline is not forwarded.
func TestOAuthProxyForwardsCallerAbortAfterSettlement(t *testing.T) {
	for _, composed := range []bool{false, true} {
		name := map[bool]string{false: "caller signal alone", true: "composed with the timeout"}[composed]
		t.Run(name, func(t *testing.T) {
			p, _, requests, frames := refreshRig(t)
			caller, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx := caller
			if composed {
				refresh, stop := context.WithTimeout(caller, 40*time.Millisecond)
				defer stop()
				ctx = ai.WithOAuthRefreshTimeout(refresh, caller, 40*time.Millisecond)
			}
			if _, err := p.RefreshTokenContext(ctx, ai.OAuthCredentials{Refresh: "r"}); err != nil {
				t.Fatal(err)
			}
			id := (<-requests).ID
			expectNoFrame(t, frames) // settlement and the refresh context's own deadline abort nothing
			cancel()
			expectCancelFrame(t, frames, id)
		})
	}
}

// A request cancelled in flight already carried the caller's cancellation; no forward remains for it.
func TestOAuthProxyDoesNotForwardAfterInFlightCancellation(t *testing.T) {
	rig := newOAuthProxyRig(t)
	p := &oauthProxy{host: rig.host, me: rig.me, name: "example-extension", cfg: ProviderOAuthConfig{HasRefresh: true}}
	caller, cancel := context.WithCancel(t.Context())
	go func() {
		env, err := tryReadFramed(rig.client)
		if err != nil || env.Type != MsgRequest {
			return
		}
		cancel()
	}()
	if _, err := p.RefreshTokenContext(caller, ai.OAuthCredentials{Refresh: "r"}); err == nil {
		t.Fatal("cancelled refresh succeeded")
	}
	if cancelFrame := readFramed(t, rig.client); cancelFrame.Type != MsgCancel {
		t.Fatalf("in-flight cancellation frame = %+v", cancelFrame)
	}
	rig.me.connection().abortForwards.mu.Lock()
	forwards := len(rig.me.connection().abortForwards.stops)
	rig.me.connection().abortForwards.mu.Unlock()
	if forwards != 0 {
		t.Fatalf("%d forwards remain for a cancelled request", forwards)
	}
}

// The extension reports a collected signal with oauth.signal_release; the caller's later abort then sends nothing, and a closed connection ends every forward.
func TestOAuthProxyStopsForwardingWhenTheSignalIsReleased(t *testing.T) {
	p, rig, requests, frames := refreshRig(t)
	go rig.host.handleIncoming(rig.me, rig.me.connection())
	caller, cancel := context.WithCancel(t.Context())
	defer cancel()
	for range 2 {
		if _, err := p.RefreshTokenContext(caller, ai.OAuthCredentials{Refresh: "r"}); err != nil {
			t.Fatal(err)
		}
	}
	released, kept := (<-requests).ID, (<-requests).ID
	args, _ := json.Marshal(OAuthSignalReleaseArgs{RequestID: released})
	writeFramed(t, rig.client, Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: notifyOAuthSignalRelease, Args: args}})
	deadline := time.Now().Add(3 * time.Second)
	for {
		rig.me.connection().abortForwards.mu.Lock()
		_, still := rig.me.connection().abortForwards.stops[released]
		rig.me.connection().abortForwards.mu.Unlock()
		if !still {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the released signal is still forwarded")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	expectCancelFrame(t, frames, kept)
	expectNoFrame(t, frames)

	closing, stop := context.WithCancel(t.Context())
	if _, err := p.RefreshTokenContext(closing, ai.OAuthCredentials{Refresh: "r"}); err != nil {
		t.Fatal(err)
	}
	<-requests
	rig.me.connection().stopAbortForwards()
	stop()
	expectNoFrame(t, frames)
}
