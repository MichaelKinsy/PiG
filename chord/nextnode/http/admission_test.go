package http

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Tests of admission (slice 10 §2, R4; security baseline (1)-(3), "Relays"): local only on a loopback connection with
// a loopback `Host` and its port and no forwarded headers; allowed hosts as https; duplicate `Host`s refused; relays
// verifying forwarded requests; foreign requests and `Origin` rules. Ports test/next-node/http/admission.test.ts.

var admissionLoopback = Connection{LocalAddress: "127.0.0.1", LocalPort: 8788, RemoteAddress: "127.0.0.1"}

var admissionAllowed = NewStringSet("notes.example.com")

type admissionHeader struct{ name, value string }

// admissionRequest is a request with headers (a list: duplicates kept), as the component reads it.
func admissionRequest(headers []admissionHeader) Incoming {
	request, err := http.NewRequest("GET", "http://x/", nil)
	if err != nil {
		panic(err)
	}
	request.Host = ""
	for _, header := range headers {
		request.Header.Add(header.name, header.value)
	}
	return WebIncoming(request)
}

// admissionEntries is a request with the entries as headers.
func admissionEntries(entries map[string]string) Incoming {
	var headers []admissionHeader
	for name, value := range entries {
		headers = append(headers, admissionHeader{name, value})
	}
	return admissionRequest(headers)
}

// admitted is what admission gives a request with headers on connection, with relays.
func admitted(headers []admissionHeader, connection Connection, relays []*HttpRelay) Admission {
	return admit(admissionRequest(headers), connection, admissionAllowed, relays)
}

func admissionRefused(t *testing.T, admission Admission) *Refused {
	t.Helper()
	refused, ok := isRefused(admission)
	if !ok {
		t.Fatalf("admission = %#v, want refused", admission)
	}
	return refused
}

func TestAdmissionProvenance(t *testing.T) {
	t.Run("is local on a loopback connection with a loopback Host and its port (without it on port 80)", func(t *testing.T) {
		for _, host := range []string{"127.0.0.1:8788", "LOCALHOST:8788", "[::1]:8788"} {
			got := admitted([]admissionHeader{{"host", host}}, admissionLoopback, nil)
			want := &Admitted{Provenance: Provenance{Kind: ProvenanceLocal}, Origin: "http://" + strings.ToLower(host)}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("host %q: admission = %#v, want %#v", host, got, want)
			}
		}
		got := admitted([]admissionHeader{{"host", "localhost"}}, Connection{LocalAddress: "::ffff:127.0.0.1", LocalPort: 80}, nil)
		admittedValue, ok := got.(*Admitted)
		if !ok || admittedValue.Provenance.Kind != ProvenanceLocal {
			t.Fatalf("admission = %#v, want local", got)
		}
	})

	// Opus's security audit H0: a loopback `Host` from a LAN address, or forwarded by a local proxy, was local.
	t.Run("refuses a loopback Host on another connection, without a connection, or with forwarded headers", func(t *testing.T) {
		host := admissionHeader{"host", "127.0.0.1:8788"}
		admissionRefused(t, admitted([]admissionHeader{host}, Connection{LocalAddress: "192.168.1.5", LocalPort: 8788}, nil))
		admissionRefused(t, admitted([]admissionHeader{host}, Connection{}, nil))
		admissionRefused(t, admitted([]admissionHeader{host}, Connection{LocalPort: 8788}, nil))
		for _, forwarded := range []string{"x-forwarded-for", "x-forwarded-proto", "forwarded"} {
			admissionRefused(t, admitted([]admissionHeader{host, {forwarded, "1"}}, admissionLoopback, nil))
		}
	})

	t.Run("admits an allowed host as remote https; refuses another, another port, two Hosts, none", func(t *testing.T) {
		got := admitted([]admissionHeader{{"host", "Notes.Example.com"}}, admissionLoopback, nil)
		want := &Admitted{
			Provenance: Provenance{Kind: ProvenanceRemote, Host: "notes.example.com"},
			Origin:     "https://notes.example.com",
			Allowed:    "notes.example.com",
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("admission = %#v, want %#v", got, want)
		}
		for _, headers := range [][]admissionHeader{
			{{"host", "evil.com"}},
			{{"host", "127.0.0.1:1"}},
			{{"host", "localhost"}},
			{{"host", "localhost.:8788"}},
			{{"host", "notes.example.com:443"}},
			{{"host", "127.0.0.1:8788"}, {"host", "notes.example.com"}},
			{{"host", "notes.example.com"}, {"host", "127.0.0.1:8788"}},
			{},
		} {
			admissionRefused(t, admitted(headers, admissionLoopback, nil))
		}
	})

	t.Run("admits a forwarded request on loopback that a relay verifies, with its origin and framer", func(t *testing.T) {
		framer := "https://radius.example"
		radius := &HttpRelay{
			Name: "radius",
			Verify: func(request *http.Request) (*RelayVerified, error) {
				if request.Header.Get("x-relay-secret") == "s" {
					return &RelayVerified{Origin: "https://me.radius.example", Framer: &framer}, nil
				}
				return nil, nil
			},
		}
		broken := &HttpRelay{
			Name:   "broken",
			Verify: func(*http.Request) (*RelayVerified, error) { return nil, errors.New("verify") },
		}
		host := admissionHeader{"host", "127.0.0.1:8788"}
		forwarded := admissionHeader{"x-forwarded-for", "203.0.113.9"}
		got, ok := admitted([]admissionHeader{host, forwarded, {"x-relay-secret", "s"}}, admissionLoopback, []*HttpRelay{radius}).(*Admitted)
		if !ok {
			t.Fatal("relay-verified request was not admitted")
		}
		if want := (Provenance{Kind: ProvenanceRelayed, Relay: "radius", Origin: "https://me.radius.example"}); got.Provenance != want {
			t.Fatalf("provenance = %#v, want %#v", got.Provenance, want)
		}
		if got.Origin != "https://me.radius.example" || got.Framer != "https://radius.example" || got.Allowed != "" || got.Relay != radius {
			t.Fatalf("admitted = %#v", got)
		}
		admissionRefused(t, admitted([]admissionHeader{host, forwarded, {"x-relay-secret", "wrong"}}, admissionLoopback, []*HttpRelay{radius}))
		// Fable's recheck N2: a relay's origin reached the CSP unchecked (`; script-src *`).
		javascript := "javascript:x"
		for _, bad := range []*RelayVerified{
			{Origin: "https://a.example; script-src *"},
			{Origin: "https://a.example", Framer: &javascript},
			{Origin: "https://a.example/path"},
		} {
			bad := bad
			lying := &HttpRelay{Name: "lying", Verify: func(*http.Request) (*RelayVerified, error) { return bad, nil }}
			refused := admissionRefused(t, admitted([]admissionHeader{host, forwarded}, admissionLoopback, []*HttpRelay{lying}))
			if refused.Failed == nil || refused.Failed.Relay != "lying" {
				t.Fatalf("refused = %#v, want failed relay lying", refused)
			}
		}
		refused := admissionRefused(t, admitted([]admissionHeader{host, forwarded}, admissionLoopback, []*HttpRelay{broken}))
		if refused.Failed == nil || refused.Failed.Relay != "broken" || refused.Failed.Error == nil || refused.Failed.Error.Error() != "verify" {
			t.Fatalf("refused = %#v, want failed relay broken: verify", refused)
		}
	})
}

func TestAdmissionForeignRequestsAndOrigin(t *testing.T) {
	t.Run("is foreign for another site by Fetch Metadata, or a frame not from this origin; not without the headers", func(t *testing.T) {
		headers := admissionEntries
		for _, c := range []struct {
			entries map[string]string
			want    bool
		}{
			{map[string]string{"sec-fetch-site": "cross-site"}, true},
			{map[string]string{"sec-fetch-site": "same-site"}, true},
			{map[string]string{"sec-fetch-site": "same-origin", "sec-fetch-dest": "iframe"}, false},
			{map[string]string{"sec-fetch-site": "none", "sec-fetch-dest": "iframe"}, true},
			{map[string]string{"sec-fetch-dest": "embed"}, true},
			{map[string]string{"sec-fetch-site": "same-origin"}, false},
			{map[string]string{"sec-fetch-site": "none"}, false},
			{map[string]string{}, false},
		} {
			if got := isForeign(headers(c.entries)); got != c.want {
				t.Errorf("isForeign(%v) = %v, want %v", c.entries, got, c.want)
			}
		}
		for _, c := range []struct {
			method string
			dest   string
			want   bool
		}{
			{"GET", "document", true},
			{"POST", "document", false},
			{"GET", "empty", true},
			{"GET", "iframe", false},
		} {
			got := isNavigation(c.method, headers(map[string]string{"sec-fetch-mode": "navigate", "sec-fetch-dest": c.dest}))
			if got != c.want {
				t.Errorf("isNavigation(%s, %s) = %v, want %v", c.method, c.dest, got, c.want)
			}
		}
	})

	t.Run("accepts an absent or equal Origin; an unsafe method from a browser needs it", func(t *testing.T) {
		origin := "http://127.0.0.1:8788"
		for _, c := range []struct {
			method  string
			entries map[string]string
			want    bool
		}{
			{"GET", map[string]string{}, true},
			{"POST", map[string]string{}, true},
			{"POST", map[string]string{"origin": origin}, true},
			{"GET", map[string]string{"origin": "http://evil.com"}, false},
			{"POST", map[string]string{"origin": "null"}, false},
			{"POST", map[string]string{"sec-fetch-site": "same-origin"}, false},
			{"GET", map[string]string{"sec-fetch-site": "same-origin"}, true},
		} {
			if got := originAccepted(c.method, admissionEntries(c.entries), origin); got != c.want {
				t.Errorf("originAccepted(%s, %v) = %v, want %v", c.method, c.entries, got, c.want)
			}
		}
	})
}
