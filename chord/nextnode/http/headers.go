package http

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"
	"weak"
)

// Ports packages/chord/src/next-node/http/headers.ts. The default headers (decision "The HTTP component's security
// baseline" (4), "A CSP nonce per response"): on every response, the component's own included: no caching, no sniffing, no
// referrer, no framing but by the contributed framers, and a CSP with the response's own script nonce. A response's own
// headers replace same-named ones (a route widens its own CSP); they are validated as they are set, so a CR or LF never
// reaches the wire.
//
// Forced Go differences. `TypeError` for an invalid name or value is a returned *HeaderError. A JavaScript string is UTF-16
// code units: a header value's characters above U+00FF are refused as Pi's `[\x80-\xff]` class does, so a value is
// Latin-1 text, written to the wire as Latin-1 bytes (node.go). `WeakMap<Router, ...>` is a map keyed by weak.Pointer with a
// cleanup that drops the entry when its router is collected.

// HeaderError is Pi's `TypeError` for a header name or value HTTP cannot carry.
type HeaderError struct{ Message string }

func (e *HeaderError) Error() string { return e.Message }

type headerEntry struct {
	name   string
	values []string
}

// AnswerHeaders are a response's headers as the component builds them: by lowercased name, each value checked when set.
type AnswerHeaders struct {
	// index is the position in entries of each lowercased name; a replaced header keeps its position.
	index   map[string]int
	entries []headerEntry
}

// NewAnswerHeaders is an empty set of headers.
func NewAnswerHeaders() *AnswerHeaders { return &AnswerHeaders{index: map[string]int{}} }

// Get is the values of name joined with ", ", false without it.
func (h *AnswerHeaders) Get(name string) (string, bool) {
	at, ok := h.index[strings.ToLower(name)]
	if !ok {
		return "", false
	}
	return strings.Join(h.entries[at].values, ", "), true
}

// Has is whether name is set.
func (h *AnswerHeaders) Has(name string) bool {
	_, ok := h.index[strings.ToLower(name)]
	return ok
}

// Set sets name to values (several values: one header line each, as `Set-Cookie` needs), replacing a same-named one; it
// returns a *HeaderError for a name or value HTTP cannot carry.
func (h *AnswerHeaders) Set(name string, values ...string) error {
	if !validToken(name) {
		return &HeaderError{Message: fmt.Sprintf("header: invalid name %s", jsonString(name))}
	}
	for _, each := range values {
		if !validFieldValue(each) {
			return &HeaderError{Message: fmt.Sprintf("header %s: invalid value %s", name, jsonString(each))}
		}
	}
	entry := headerEntry{name: name, values: append([]string(nil), values...)}
	key := strings.ToLower(name)
	if at, ok := h.index[key]; ok {
		h.entries[at] = entry
		return nil
	}
	h.index[key] = len(h.entries)
	h.entries = append(h.entries, entry)
	return nil
}

// mustSet sets a header whose name and value the component wrote itself.
func (h *AnswerHeaders) mustSet(name string, values ...string) {
	if err := h.Set(name, values...); err != nil {
		panic(err)
	}
}

// Delete removes name.
func (h *AnswerHeaders) Delete(name string) {
	key := strings.ToLower(name)
	at, ok := h.index[key]
	if !ok {
		return
	}
	h.entries = append(h.entries[:at], h.entries[at+1:]...)
	delete(h.index, key)
	for i := at; i < len(h.entries); i++ {
		h.index[strings.ToLower(h.entries[i].name)] = i
	}
}

// HeaderLine is one header line: the name as set and a value.
type HeaderLine struct{ Name, Value string }

// Lines is each header line, in order.
func (h *AnswerHeaders) Lines() []HeaderLine {
	var lines []HeaderLine
	for _, entry := range h.entries {
		for _, value := range entry.values {
			lines = append(lines, HeaderLine{Name: entry.name, Value: value})
		}
	}
	return lines
}

// validToken is whether name is an HTTP token.
func validToken(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || strings.IndexByte("!#$%&'*+-.^_`|~", b) >= 0 {
			continue
		}
		return false
	}
	return true
}

// validFieldValue is whether value is visible Latin-1, spaces and tabs; no CR, LF or NUL.
func validFieldValue(value string) bool {
	for _, r := range value {
		switch {
		case r == '\t' || (r >= 0x20 && r <= 0x7e) || (r >= 0x80 && r <= 0xff):
		default:
			return false
		}
	}
	return utf8.ValidString(value)
}

// Nonces are fresh script nonces, one per response: 16 random bytes each, base64, cut from a pool filled 4 KiB at a time.
type Nonces struct {
	mu   sync.Mutex
	pool [4096]byte
	at   int
}

// NewNonces is a nonce source.
func NewNonces() *Nonces { return &Nonces{at: 4096} }

// Next is the next nonce (never one handed out before).
func (n *Nonces) Next() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	// One read per 256 nonces.
	if n.at == len(n.pool) {
		if _, err := rand.Read(n.pool[:]); err != nil {
			panic(err)
		}
		n.at = 0
	}
	nonce := base64.StdEncoding.EncodeToString(n.pool[n.at : n.at+16])
	n.at += 16
	return nonce
}

// DefaultHeaders are the default headers of one component's responses. The CSP around the nonce is kept per router and
// origin for local and allowed-host requests (a bounded set: the hosts contributions name); a relayed request's origins
// come from its relay and are never kept.
type DefaultHeaders struct {
	mu sync.Mutex
	// policies are by router (its change makes new ones; held weakly), then by origin: the CSP's text before and after the
	// nonce.
	policies map[weak.Pointer[Router]]map[string][2]string
}

// NewDefaultHeaders is an empty set of policies.
func NewDefaultHeaders() *DefaultHeaders {
	return &DefaultHeaders{policies: map[weak.Pointer[Router]]map[string][2]string{}}
}

// Of is the default headers of a response to a request admitted as admitted (nil: refused before admission), with router's
// framers and CSP sources (nil before the router is resolved), its CSP allowing scripts with nonce.
func (d *DefaultHeaders) Of(admitted *Admitted, router *Router, nonce string) *AnswerHeaders {
	headers := NewAnswerHeaders()
	headers.mustSet("Cache-Control", "no-store")
	headers.mustSet("X-Content-Type-Options", "nosniff")
	headers.mustSet("Referrer-Policy", "no-referrer")
	// `X-Frame-Options` names no origin: with framers, only `frame-ancestors` speaks.
	framed := router != nil && router.Framers.Size() > 0
	if !framed && (admitted == nil || admitted.Framer == "") {
		headers.mustSet("X-Frame-Options", "DENY")
	}
	policy := d.policy(admitted, router)
	headers.mustSet("Content-Security-Policy", policy[0]+nonce+policy[1])
	return headers
}

// policy is the CSP's text around the nonce for admitted under router.
func (d *DefaultHeaders) policy(admitted *Admitted, router *Router) [2]string {
	if router == nil || admitted == nil || admitted.Relay != nil {
		return csp(admitted, router)
	}
	key := weak.Make(router)
	d.mu.Lock()
	defer d.mu.Unlock()
	policies := d.policies[key]
	if policies == nil {
		policies = map[string][2]string{}
		d.policies[key] = policies
		runtime.AddCleanup(router, func(key weak.Pointer[Router]) {
			d.mu.Lock()
			delete(d.policies, key)
			d.mu.Unlock()
		}, key)
	}
	policy, ok := policies[admitted.Origin]
	if !ok {
		policy = csp(admitted, router)
		policies[admitted.Origin] = policy
	}
	return policy
}

// nonceMark is where the nonce goes in the CSP's text while it is made.
const nonceMark = "\x00nonce\x00"

// csp is the CSP's text before and after the nonce: nothing by default, scripts from this origin or with the nonce,
// connections to it and its WebSocket, framing by the framers, plus contributed sources.
func csp(admitted *Admitted, router *Router) [2]string {
	var framers []string
	if router != nil {
		framers = router.Framers.Values()
	}
	if admitted != nil && admitted.Framer != "" {
		framers = append(framers, admitted.Framer)
	}
	connect := []string{"'self'"}
	if admitted != nil {
		connect = append(connect, strings.Replace(admitted.Origin, "http", "ws", 1))
	}
	ancestors := []string{"'none'"}
	if len(framers) > 0 {
		ancestors = framers
	}
	type directive struct {
		name    string
		sources []string
	}
	directives := []*directive{
		{"default-src", []string{"'none'"}},
		{"script-src", []string{"'self'", nonceMark}},
		{"connect-src", connect},
		{"base-uri", []string{"'none'"}},
		{"form-action", []string{"'self'"}},
		{"frame-ancestors", ancestors},
	}
	if router != nil {
		for _, extra := range router.Csp {
			var current *directive
			for _, each := range directives {
				if each.name == extra.Directive {
					current = each
					break
				}
			}
			switch {
			case current == nil:
				directives = append(directives, &directive{extra.Directive, append([]string(nil), extra.Sources...)})
			// A directive the baseline sets to 'none' is replaced by the sources given for it.
			case current.sources[0] == "'none'":
				current.sources = append([]string(nil), extra.Sources...)
			default:
				current.sources = append(append([]string(nil), current.sources...), extra.Sources...)
			}
		}
	}
	parts := make([]string, len(directives))
	for i, each := range directives {
		parts[i] = each.name + " " + strings.Join(each.sources, " ")
	}
	text := strings.Join(parts, "; ")
	at := strings.Index(text, nonceMark)
	return [2]string{text[:at] + "'nonce-", "'" + text[at+len(nonceMark):]}
}
