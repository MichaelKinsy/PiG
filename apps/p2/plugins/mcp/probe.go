package mcp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Ports apps/p2/plugins/mcp/probe.ts: the loopback probe, a native of the manager: a listener on the p2 host's 127.0.0.1
// that tells a sign-in page whether its browser runs on the p2 host. The page (on p2's Radius or public origin) fetches
// `http://127.0.0.1:<port>/<nonce>`: only a browser on this machine reaches this listener, and only this p2 knows the
// nonce, so another program or p2 on the browser's machine cannot answer for it. A wrong answer costs nothing more than a
// sign-in to paste.

// nonceTTL is how long a nonce answers.
const nonceTTL = 5 * time.Minute

type nonceEntry struct {
	origin  string
	expires time.Time
}

// LoopbackProbe is the probe listener, started on first use.
type LoopbackProbe struct {
	mu     sync.Mutex
	nonces map[string]nonceEntry
	server *http.Server
	port   int
	closed bool
}

// NewLoopbackProbe is a probe that has not started listening.
func NewLoopbackProbe() *LoopbackProbe { return &LoopbackProbe{nonces: map[string]nonceEntry{}} }

// URL is a probe URL for a page on origin: it answers that origin's requests, with CORS, for a few minutes.
func (p *LoopbackProbe) URL(origin string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", errors.New("The loopback probe is closed")
	}
	now := time.Now()
	for nonce, entry := range p.nonces {
		if !entry.expires.After(now) {
			delete(p.nonces, nonce)
		}
	}
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(random)
	p.nonces[nonce] = nonceEntry{origin: origin, expires: now.Add(nonceTTL)}
	if p.server == nil {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", err
		}
		p.port = listener.Addr().(*net.TCPAddr).Port
		p.server = &http.Server{Handler: http.HandlerFunc(p.handle), ReadHeaderTimeout: 10 * time.Second}
		go func(server *http.Server) { _ = server.Serve(listener) }(p.server)
	}
	return "http://127.0.0.1:" + strconv.Itoa(p.port) + "/" + nonce, nil
}

// Close stops listening.
func (p *LoopbackProbe) Close() error {
	p.mu.Lock()
	p.closed = true
	server := p.server
	p.mu.Unlock()
	if server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second) //portlint:allow ctxbackground closing a listener outlives every request context
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		return server.Close()
	}
	return nil
}

// handle answers 204 with CORS for the nonce's origin, else 404 without: other pages learn nothing. Preflights (Chrome's
// private network access asked for one) get the same answer, allowing the private network request.
func (p *LoopbackProbe) handle(response http.ResponseWriter, request *http.Request) {
	nonce := strings.TrimPrefix(request.URL.Path, "/")
	p.mu.Lock()
	entry, known := p.nonces[nonce]
	p.mu.Unlock()
	origin := request.Header.Get("Origin")
	if !known || !entry.expires.After(time.Now()) || origin != entry.origin {
		response.Header().Set("Cache-Control", "no-store")
		response.WriteHeader(http.StatusNotFound)
		return
	}
	header := response.Header()
	header.Set("Access-Control-Allow-Origin", origin)
	header.Set("Vary", "Origin")
	header.Set("Cache-Control", "no-store")
	switch request.Method {
	case http.MethodOptions:
		header.Set("Access-Control-Allow-Methods", "GET")
		header.Set("Access-Control-Allow-Private-Network", "true")
		response.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		response.WriteHeader(http.StatusNoContent)
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}
