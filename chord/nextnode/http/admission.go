package http

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Ports packages/chord/src/next-node/http/admission.ts. Admission (slice 10 §2, R4; decision "The HTTP component's security
// baseline" (1)-(3), "Relays"): where a request comes from, decided before anything else. Local only when the connection
// arrived on a loopback address, its `Host` is a loopback name with the listening port, and it carries no forwarded
// header; a forwarded request on loopback only when a contributed relay verifies it; otherwise only through an allowed
// host (https); else refused. Foreign requests (another site's, by Fetch Metadata) and a foreign `Origin` are classified
// here; the handler applies them per route.
//
// Forced Go differences. An absent optional string is "" (a host, an origin and a framer are never empty once admitted;
// a relay's empty framer is refused as no origin) and an absent port is 0. The union `Admitted | Refused` is the Admission
// interface: a type switch tells them apart. `HeaderSource` is the Incoming interface's header reads.

// ProvenanceKind tells where an admitted request came from.
type ProvenanceKind string

const (
	ProvenanceLocal   ProvenanceKind = "local"
	ProvenanceRemote  ProvenanceKind = "remote"
	ProvenanceRelayed ProvenanceKind = "relayed"
)

// Provenance is where an admitted request came from: Host for a remote one, Relay and Origin for a relayed one.
type Provenance struct {
	Kind   ProvenanceKind
	Host   string
	Relay  string
	Origin string
}

// Connection is the connection a request arrived on, as its listener knows it (a mounted handler passes what it has).
type Connection struct {
	LocalAddress  string
	LocalPort     int
	RemoteAddress string
}

// Admission is what admit decides: *Admitted or *Refused.
type Admission interface{ admission() }

// Admitted is an admitted request: its provenance, its own origin, the origin allowed to frame it (a relay's), how it
// came.
type Admitted struct {
	Provenance Provenance
	Origin     string
	// Framer is the origin allowed to frame it, "" for none.
	Framer string
	// Allowed is the allowed host it came through, "" for none: its connections end when that host leaves.
	Allowed string
	// Relay is the relay that verified it, nil for none: its connections end when that relay leaves.
	Relay *HttpRelay
}

// Refused is what admission refuses, and the relay hook that threw (reported by the caller).
type Refused struct {
	Failed *RelayFailure
}

// RelayFailure is a relay hook that threw (or lied).
type RelayFailure struct {
	Relay string
	Error error
}

func (*Admitted) admission() {}
func (*Refused) admission()  {}

// Admit admits request arriving on connection with the allowed hosts and contributed relays, or refuses it (403): no or
// several `Host`s, a host neither loopback nor allowed, a loopback `Host` on another connection, forwarded headers no relay
// verifies.
func admit(request Incoming, connection Connection, hosts *StringSet, relays []*HttpRelay) Admission {
	value, ok := request.Header("host")
	host := strings.ToLower(value)
	// Several `Host` headers arrive joined with commas.
	if !ok || host == "" || strings.Contains(host, ",") {
		return refusedAdmission
	}
	if isLoopbackName(host, connection.LocalPort) {
		if connection.LocalAddress == "" || !isLoopbackAddress(connection.LocalAddress) {
			return refusedAdmission
		}
		if !request.Forwarded() {
			return &Admitted{Provenance: Provenance{Kind: ProvenanceLocal}, Origin: "http://" + host}
		}
		return relayed(request, relays)
	}
	if !hosts.Has(host) {
		return refusedAdmission
	}
	return &Admitted{
		Provenance: Provenance{Kind: ProvenanceRemote, Host: host},
		Origin:     "https://" + host,
		Allowed:    host,
	}
}

// isRefused is whether admission refused its request.
func isRefused(admission Admission) (*Refused, bool) {
	refused, ok := admission.(*Refused)
	return refused, ok
}

// isForeign is whether a request is another site's (Fetch Metadata, p2's rule): `Sec-Fetch-Site` neither `same-origin` nor
// `none`, or a frame or embed not from this origin. A request without the headers (a script, the TUI) is not.
func isForeign(headers Incoming) bool {
	site, hasSite := headers.Header("sec-fetch-site")
	if hasSite && site != "same-origin" && site != "none" {
		return true
	}
	dest, _ := headers.Header("sec-fetch-dest")
	return site != "same-origin" && frames[dest]
}

// isNavigation is whether a foreign request is a top-level navigation (a link followed to a page): `GET`, `navigate`, a
// `document` (or `empty`: Chrome on Android launching an installed app).
func isNavigation(method string, headers Incoming) bool {
	dest, _ := headers.Header("sec-fetch-dest")
	mode, _ := headers.Header("sec-fetch-mode")
	return method == "GET" && mode == "navigate" && (dest == "document" || dest == "empty")
}

// originAccepted is whether headers' `Origin` is acceptable for a request whose own origin is origin: absent or equal, and
// for an unsafe method from a browser (one sending Fetch Metadata) present and equal.
func originAccepted(method string, headers Incoming, origin string) bool {
	if sent, ok := headers.Header("origin"); ok {
		return sent == origin
	}
	if safeMethods[method] {
		return true
	}
	_, browser := headers.Header("sec-fetch-site")
	return !browser
}

var refusedAdmission Admission = &Refused{}

var loopbackNames = []string{"127.0.0.1", "localhost", "[::1]"}

// isLoopbackName is whether host (lowercased) names this server on loopback with port (without it on port 80).
func isLoopbackName(host string, port int) bool {
	if port == 0 {
		return false
	}
	for _, name := range loopbackNames {
		if host == name+":"+strconv.Itoa(port) {
			return true
		}
		if port == 80 && host == name {
			return true
		}
	}
	return false
}

// isLoopbackAddress is whether address is a loopback address: 127.0.0.0/8, ::1, or 127.x mapped into IPv6.
func isLoopbackAddress(address string) bool {
	return address == "::1" || strings.HasPrefix(address, "127.") || strings.HasPrefix(address, "::ffff:127.")
}

// relayed is a forwarded request on loopback: admitted as relayed when a relay verifies it, else refused.
func relayed(request Incoming, relays []*HttpRelay) Admission {
	for _, relay := range relays {
		verified, err := tryVerify(relay, request)
		if err != nil {
			return &Refused{Failed: &RelayFailure{Relay: relay.Name, Error: err}}
		}
		if verified == nil {
			continue
		}
		// Its origins reach the CSP: each must be one origin and nothing else.
		origins := []*string{&verified.Origin, verified.Framer}
		for _, origin := range origins {
			if origin == nil || isOrigin(*origin) {
				continue
			}
			err := fmt.Errorf("relay %s: %s is no origin", relay.Name, jsonString(*origin))
			return &Refused{Failed: &RelayFailure{Relay: relay.Name, Error: err}}
		}
		framer := ""
		if verified.Framer != nil {
			framer = *verified.Framer
		}
		return &Admitted{
			Provenance: Provenance{Kind: ProvenanceRelayed, Relay: relay.Name, Origin: verified.Origin},
			Origin:     verified.Origin,
			Framer:     framer,
			Relay:      relay,
		}
	}
	return refusedAdmission
}

// tryVerify calls a relay's hook; a panic is the error its throw would be.
func tryVerify(relay *HttpRelay, request Incoming) (verified *RelayVerified, err error) {
	defer recoverAs(&err)
	return relay.Verify(request.Request())
}

// isOrigin is whether text is exactly an origin (`https://host[:port]`): what the URL parser serializes it to.
func isOrigin(text string) bool {
	var scheme, rest string
	switch {
	case strings.HasPrefix(text, "https://"):
		scheme, rest = "https", text[len("https://"):]
	case strings.HasPrefix(text, "http://"):
		scheme, rest = "http", text[len("http://"):]
	default:
		return false
	}
	if rest == "" || strings.ContainsAny(rest, "/?#\\@ \t\r\n") {
		return false
	}
	host, port := rest, ""
	if strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return false
		}
		host, port = rest[:end+1], rest[end+1:]
		if port != "" && port[0] != ':' {
			return false
		}
		if port != "" {
			port = port[1:]
			if port == "" {
				return false
			}
		}
		address, err := netip.ParseAddr(host[1 : len(host)-1])
		if err != nil || !address.Is6() || address.Is4In6() || address.String() != host[1:len(host)-1] {
			return false
		}
	} else {
		if at := strings.IndexByte(rest, ':'); at >= 0 {
			host, port = rest[:at], rest[at+1:]
			if port == "" {
				return false
			}
		}
		if host == "" || !canonicalDomain(host) {
			return false
		}
	}
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 0 || value > 65535 || strconv.Itoa(value) != port {
			return false
		}
		if (scheme == "https" && value == 443) || (scheme == "http" && value == 80) {
			return false
		}
	}
	return true
}

// canonicalDomain is whether host is what the URL parser serializes a host of that text to: ASCII lowercase, no forbidden
// code point, and an IPv4 address only in its dotted-decimal form.
func canonicalDomain(host string) bool {
	for i := 0; i < len(host); i++ {
		b := host[i]
		if b >= 0x80 || (b >= 'A' && b <= 'Z') || forbiddenHostByte(b) {
			return false
		}
	}
	labels := strings.Split(strings.TrimSuffix(host, "."), ".")
	last := labels[len(labels)-1]
	if looksNumeric(last) {
		address, err := netip.ParseAddr(host)
		if err != nil || !address.Is4() || address.String() != host {
			return false
		}
	}
	return true
}

// looksNumeric is whether a label ends a host as an IPv4 number (decimal, or `0x` hexadecimal).
func looksNumeric(label string) bool {
	if label == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(label), "0x") {
		return true
	}
	for i := 0; i < len(label); i++ {
		if label[i] < '0' || label[i] > '9' {
			return false
		}
	}
	return true
}

// frames is the `Sec-Fetch-Dest` of a frame or an embed.
var frames = map[string]bool{"iframe": true, "frame": true, "embed": true, "object": true, "fencedframe": true}

// safeMethods are the methods that change nothing.
var safeMethods = map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true}
