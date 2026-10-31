package http

import (
	"encoding/base64"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Tests of the default headers (security baseline (4), "A CSP nonce per response"): the fixed ones, `X-Frame-Options`
// only without framers, the CSP with the response's nonce, the WebSocket origin, the framers (a relay's too) and
// contributed sources; a fresh nonce each time; and headers checked as they are set, several lines for a list. Ports
// test/next-node/http/headers.test.ts.

// headersRouterOf is the router of one contribution.
func headersRouterOf(t testing.TB, value HttpContribution) *Router {
	t.Helper()
	table := registries.RegistryTable[HttpContribution]{{Plugin: "@app/a", Version: 1, Value: value}}
	router, err := ComposeRouter(table, registries.ComposeContext{Report: func(registries.RegistryConflict) {}})
	if err != nil {
		t.Fatal(err)
	}
	return router
}

// headersLocal is a request admitted on loopback.
func headersLocal() *Admitted {
	return &Admitted{Provenance: Provenance{Kind: ProvenanceLocal}, Origin: "http://127.0.0.1:8788"}
}

// headersDefaults is the default headers of one component.
var headersDefaults = NewDefaultHeaders()

func headersPolicy(t *testing.T, headers *AnswerHeaders) string {
	t.Helper()
	value, ok := headers.Get("content-security-policy")
	if !ok {
		t.Fatal("no Content-Security-Policy")
	}
	return value
}

func TestDefaultHeaders(t *testing.T) {
	t.Run("refuse caching, sniffing, referrers and framing; the CSP allows scripts by nonce and the WebSocket origin", func(t *testing.T) {
		headers := headersDefaults.Of(headersLocal(), headersRouterOf(t, HttpContribution{}), "N")
		want := []HeaderLine{
			{"Cache-Control", "no-store"},
			{"X-Content-Type-Options", "nosniff"},
			{"Referrer-Policy", "no-referrer"},
			{"X-Frame-Options", "DENY"},
			{"Content-Security-Policy", "default-src 'none'; script-src 'self' 'nonce-N'; connect-src 'self' ws://127.0.0.1:8788; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"},
		}
		if got := headers.Lines(); !reflect.DeepEqual(got, want) {
			t.Fatalf("lines = %v, want %v", got, want)
		}
	})

	t.Run("name the framers (a relay's included) without X-Frame-Options, and add contributed sources", func(t *testing.T) {
		router := headersRouterOf(t, HttpContribution{
			Framers: []string{"https://a.example"},
			Csp: []CspEntry{
				{Directive: "img-src", Sources: []string{"data:"}},
				{Directive: "script-src", Sources: []string{"https://cdn.example"}},
			},
		})
		relayed := headersLocal()
		relayed.Origin = "https://me.relay.example"
		relayed.Framer = "https://relay.example"
		headers := headersDefaults.Of(relayed, router, "N")
		if headers.Has("x-frame-options") {
			t.Fatal("X-Frame-Options is set")
		}
		want := "default-src 'none'; script-src 'self' 'nonce-N' https://cdn.example; connect-src 'self' wss://me.relay.example; base-uri 'none'; form-action 'self'; frame-ancestors https://a.example https://relay.example; img-src data:"
		if got := headersPolicy(t, headers); got != want {
			t.Fatalf("CSP = %q, want %q", got, want)
		}
	})

	t.Run("before admission: no WebSocket origin; a nonce is 16 fresh random bytes each time", func(t *testing.T) {
		if got := headersPolicy(t, headersDefaults.Of(nil, nil, "N")); !strings.Contains(got, "connect-src 'self';") {
			t.Fatalf("CSP = %q, want connect-src 'self';", got)
		}
		nonces := NewNonces()
		made := map[string]bool{}
		for i := 0; i < 1_000; i++ {
			made[nonces.Next()] = true
		}
		if len(made) != 1_000 {
			t.Fatalf("distinct nonces = %d, want 1000", len(made))
		}
		for nonce := range made {
			bytes, err := base64.StdEncoding.DecodeString(nonce)
			if err != nil || len(bytes) != 16 {
				t.Fatalf("nonce %q decodes to %d bytes (%v), want 16", nonce, len(bytes), err)
			}
		}
	})

	t.Run("keep the CSP of local and allowed-host requests per router; a relayed one's never", func(t *testing.T) {
		router := headersRouterOf(t, HttpContribution{})
		first := headersPolicy(t, headersDefaults.Of(headersLocal(), router, "A"))
		if got, want := headersPolicy(t, headersDefaults.Of(headersLocal(), router, "B")), strings.Replace(first, "'nonce-A'", "'nonce-B'", 1); got != want {
			t.Fatalf("CSP = %q, want %q", got, want)
		}
		relayed := headersLocal()
		relayed.Origin = "https://u1.relay.example"
		relayed.Relay = &HttpRelay{Name: "r", Verify: func(*http.Request) (*RelayVerified, error) { return nil, nil }}
		if got := headersPolicy(t, headersDefaults.Of(relayed, router, "C")); !strings.Contains(got, "wss://u1.relay.example") {
			t.Fatalf("CSP = %q, want wss://u1.relay.example", got)
		}
	})

	t.Run("refuse a header name or value HTTP cannot carry; replace same-named ones; a list is one line per value", func(t *testing.T) {
		headers := NewAnswerHeaders()
		for _, c := range []struct {
			name   string
			values []string
		}{
			{"X-Split", []string{"a\r\nSet-Cookie: x=1"}},
			{"X Bad", []string{"1"}},
			{"X-Nul", []string{"a\x00b"}},
			{"Set-Cookie", []string{"a=1", "b=2\n"}},
		} {
			if err := headers.Set(c.name, c.values...); err == nil {
				t.Errorf("Set(%q, %q) accepted, want *HeaderError", c.name, c.values)
			} else if _, ok := err.(*HeaderError); !ok {
				t.Errorf("Set(%q, %q) = %T, want *HeaderError", c.name, c.values, err)
			}
		}
		for _, set := range []struct {
			name   string
			values []string
		}{
			{"content-security-policy", []string{"a"}},
			{"Content-Security-Policy", []string{"b"}},
			{"Set-Cookie", []string{"a=1", "b=2"}},
		} {
			if err := headers.Set(set.name, set.values...); err != nil {
				t.Fatal(err)
			}
		}
		want := []HeaderLine{
			{"Content-Security-Policy", "b"},
			{"Set-Cookie", "a=1"},
			{"Set-Cookie", "b=2"},
		}
		if got := headers.Lines(); !reflect.DeepEqual(got, want) {
			t.Fatalf("lines = %v, want %v", got, want)
		}
	})
}

// headersKeepAPolicy makes a router, has own keep a policy of it and returns only a weak reference: nothing of the router
// stays in the caller's frame.
func headersKeepAPolicy(t *testing.T, own *DefaultHeaders) weak.Pointer[Router] {
	router := headersRouterOf(t, HttpContribution{Framers: []string{"https://a.example"}})
	own.Of(headersLocal(), router, "N")
	return weak.Make(router)
}

func TestDefaultHeadersGC(t *testing.T) {
	t.Run("keeps no policy of a router gone", func(t *testing.T) {
		own := NewDefaultHeaders()
		ref := headersKeepAPolicy(t, own)
		deadline := time.Now().Add(within)
		for i := 0; i < 100 && ref.Value() != nil; i++ {
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
		}
		if ref.Value() != nil {
			t.Fatal("the router was not collected")
		}
		for {
			own.mu.Lock()
			kept := len(own.policies)
			own.mu.Unlock()
			if kept == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("policies kept = %d, want 0", kept)
			}
			runtime.GC()
			time.Sleep(10 * time.Millisecond)
		}
	})
}
