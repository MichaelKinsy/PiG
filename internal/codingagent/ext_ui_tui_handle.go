package codingagent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The two calls of Pi's `tui` handle a widget factory receives, for an extension that has no factory to receive it
// (Go, Python and Rust extensions run out of process).
var _ extension.TUIDriver = (*ExtUIContext)(nil)

// InvalidateTUI is Pi's TUI.invalidate() (tui.ts:366): every component drops its render cache, as a theme change does. It runs on the owner
// loop, which owns the component tree.
func (u *ExtUIContext) InvalidateTUI() {
	apply := func() {
		u.m.rendererMu.RLock()
		defer u.m.rendererMu.RUnlock()
		switch inst := u.m.tuiInst.(type) {
		case nil:
		case interface{ InvalidateMounted() }:
			// Pi's TUI.invalidate walks every mounted component, as a theme change does; the renderer's own Invalidate marks only its root.
			inst.InvalidateMounted()
		default:
			inst.Invalidate()
		}
	}
	if u.m.runCtx == nil {
		apply()
		return
	}
	u.m.runOnMain(u.m.runCtx, apply)
}

// RequestTUIRender is Pi's TUI.requestRender(force) (tui.ts:990): it coalesces a repaint on the current renderer, a full one when force is set.
func (u *ExtUIContext) RequestTUIRender(force bool) {
	u.m.rendererMu.RLock()
	defer u.m.rendererMu.RUnlock()
	if u.m.tuiInst != nil {
		u.m.tuiInst.RequestRender(force)
	}
}

var _ extension.LinkSchemeRegistrar = (*ExtUIContext)(nil)

// pig additive (D193): RegisterLinkScheme lets an extension take the clicks on the OSC 8 links of a scheme.
func (u *ExtUIContext) RegisterLinkScheme(owner, scheme string, handler func(url string)) (func(), error) {
	return u.m.linkSchemes.register(owner, scheme, handler)
}

// linkSchemes are the OSC 8 schemes extensions claimed, by scheme.
type linkSchemes struct {
	mu      sync.Mutex
	claims  map[string]*linkClaim
	counter uint64
}

type linkClaim struct {
	id      uint64
	owner   string
	handler func(url string)
}

func (s *linkSchemes) register(owner, scheme string, handler func(url string)) (func(), error) {
	if err := extension.ValidateLinkScheme(scheme); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if held, ok := s.claims[scheme]; ok && held.owner != owner {
		return nil, fmt.Errorf("%w: %q is held by %q", extension.ErrLinkSchemeInUse, scheme, held.owner)
	}
	if s.claims == nil {
		s.claims = map[string]*linkClaim{}
	}
	s.counter++
	claim := &linkClaim{id: s.counter, owner: owner, handler: handler}
	s.claims[scheme] = claim
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			// A later claim of the same owner replaced this one and keeps the scheme.
			if s.claims[scheme] == claim {
				delete(s.claims, scheme)
			}
		})
	}, nil
}

// dispatch hands a clicked address to the extension that claimed its scheme and reports whether one did.
func (s *linkSchemes) dispatch(url string) bool {
	scheme := extension.LinkScheme(url)
	if scheme == "" {
		return false
	}
	s.mu.Lock()
	claim := s.claims[scheme]
	s.mu.Unlock()
	if claim == nil {
		return false
	}
	claim.handler(strings.TrimSpace(url))
	return true
}
