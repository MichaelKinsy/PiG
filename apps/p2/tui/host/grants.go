package host

// Launcher grants (p2-tui-port.md section 3.1): permission for exactly one action instance, bound to the worker instance, the server origin, the
// session/conversation the worker named, and an expiry. Results of a grant that was cancelled (worker swap, navigation away, close) are dropped.
//
// pi: apps/p2/src/hosts/tui-grants.ts

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/prefs"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/google/uuid"
)

// GrantTarget is the session/conversation a worker reported.
type GrantTarget struct {
	Session      string
	Conversation int
}

// GrantKind is how a grant came about.
type GrantKind string

// The three kinds: the visitor's own key, a consented request, a request right after the launcher saw the matching gesture.
const (
	GrantK       GrantKind = "K"
	GrantC       GrantKind = "C"
	GrantGesture GrantKind = "gesture"
)

// ErrGrantCancelled is the cause of a cancelled grant's context.
var ErrGrantCancelled = errors.New("Grant cancelled")

// Grant is one granted action instance.
type Grant struct {
	ID      string
	Action  string
	Kind    GrantKind
	Worker  any
	Origin  string
	Target  *GrantTarget
	Expires time.Time
	// Context is cancelled when the grant is cancelled: uploads, downloads and editors stop and clean up.
	Context context.Context
	cancel  context.CancelCauseFunc
}

// ActionContext is what a launcher action may use. Worker data arrives through Ask, bound to the grant.
type ActionContext struct {
	Grant  *Grant
	UI     UI
	Origin *url.URL
	// Ask asks the worker that holds the grant for data (e.g. the text to copy); it fails once the grant is cancelled.
	Ask func(kind string, payload any) (any, error)
	// WriteTerminal writes launcher output (e.g. OSC 52) straight to the terminal, bypassing the worker filter.
	WriteTerminal func(data string)
	// SendInput sends input to the granting worker as if typed (Ctrl+V's text paste).
	SendInput func(data string)
	// ReleaseTerminal lends the real terminal to a local program; worker IO stays suspended until run returns.
	ReleaseTerminal func(run func() error) error
}

// UI is the launcher-drawn UI an action uses (TrustedUi; a test's recorder).
type UI interface {
	// Confirm shows a consent screen and reports whether it was confirmed.
	Confirm(request ConsentRequest) bool
	// Notice shows a one-line summary for d.
	Notice(text string, d time.Duration)
	// Pick is the local file picker.
	Pick(title, start string) (string, bool)
}

// ConsentContext is what a C action's consent sees.
type ConsentContext struct {
	Origin *url.URL
	Target *GrantTarget
}

// LauncherAction is one launcher action (section 3.2): K fires on the visitor's own privileged key (Key), consumed before the worker; Gesture is
// requested by the worker but accepted only within a short time after the launcher itself saw the matching gesture in the raw input (the mouse
// release ending a selection; one use); C is requested by the worker and runs only after local confirmation of exactly what Consent shows.
type LauncherAction struct {
	Kind GrantKind
	// Key is K's privileged key id.
	Key string
	// RunKey is K's body.
	RunKey func(ctx ActionContext) error
	// Accept is Gesture's payload check.
	Accept func(payload any) bool
	// Consent validates a C payload and describes it; nil denies without asking.
	Consent func(payload any, ctx ConsentContext) *ConsentRequest
	// Run is Gesture's and C's body.
	Run func(payload any, ctx ActionContext) (any, error)
}

const grantTTL = 10 * time.Minute

// Grants are the live grants of one launcher.
type Grants struct {
	mu     sync.Mutex
	grants map[string]*Grant
	origin string
}

// NewGrants is the grants of a launcher for origin.
func NewGrants(origin string) *Grants { return &Grants{grants: map[string]*Grant{}, origin: origin} }

// Create makes a grant with the default expiry.
func (g *Grants) Create(action string, kind GrantKind, worker any, target *GrantTarget) *Grant {
	return g.CreateTTL(action, kind, worker, target, grantTTL)
}

// CreateTTL makes a grant that expires after ttl.
func (g *Grants) CreateTTL(action string, kind GrantKind, worker any, target *GrantTarget, ttl time.Duration) *Grant {
	ctx, cancel := context.WithCancelCause(context.Background())
	grant := &Grant{ID: uuid.NewString(), Action: action, Kind: kind, Worker: worker, Origin: g.origin, Target: target, Expires: time.Now().Add(ttl), Context: ctx, cancel: cancel}
	g.mu.Lock()
	g.grants[grant.ID] = grant
	g.mu.Unlock()
	return grant
}

// Valid is the grant, if it still holds for this worker (and action, when not empty); expired grants are cancelled on lookup.
func (g *Grants) Valid(id string, worker any, action string) *Grant {
	g.mu.Lock()
	grant := g.grants[id]
	g.mu.Unlock()
	if grant == nil {
		return nil
	}
	if grant.Expires.Before(time.Now()) {
		g.Cancel(id)
		return nil
	}
	if grant.Worker == worker && (action == "" || grant.Action == action) {
		return grant
	}
	return nil
}

// Complete means the grant cannot be used again.
func (g *Grants) Complete(id string) {
	g.mu.Lock()
	delete(g.grants, id)
	g.mu.Unlock()
}

// Cancel ends the grant and aborts its context.
func (g *Grants) Cancel(id string) {
	g.mu.Lock()
	grant := g.grants[id]
	delete(g.grants, id)
	g.mu.Unlock()
	if grant != nil {
		grant.cancel(ErrGrantCancelled)
	}
}

func (g *Grants) cancelWhere(match func(*Grant) bool) {
	g.mu.Lock()
	var ids []string
	for id, grant := range g.grants {
		if match(grant) {
			ids = append(ids, id)
		}
	}
	g.mu.Unlock()
	for _, id := range ids {
		g.Cancel(id)
	}
}

// CancelWorker: worker swap or crash; nothing it was granted survives.
func (g *Grants) CancelWorker(worker any) {
	g.cancelWhere(func(grant *Grant) bool { return grant.Worker == worker })
}

// Navigate: the worker reports another session/conversation; grants for the old target end.
func (g *Grants) Navigate(worker any, target *GrantTarget) {
	g.cancelWhere(func(grant *Grant) bool {
		return grant.Worker == worker && grant.Target != nil && (target == nil || *grant.Target != *target)
	})
}

// CancelAll ends every grant.
func (g *Grants) CancelAll() { g.cancelWhere(func(*Grant) bool { return true }) }

// Size is the number of live grants.
func (g *Grants) Size() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.grants)
}

var sessionName = lazyregexp.New(`^[\w-]{1,128}$`)

// ParseTarget validates a worker's `navigation` report.
func ParseTarget(session string, conversation int) *GrantTarget {
	if sessionName.MatchString(session) && conversation > 0 {
		return &GrantTarget{Session: session, Conversation: conversation}
	}
	return nil
}

// PrivilegedKeys is the launcher's own keymap for privileged ids (section 3.1 rules 1-3), from the visitor's local bindings only: never from the
// server or the worker. A key is intercepted only when an action for its id is registered.
type PrivilegedKeys struct{ keys []privilegedKey }

type privilegedKey struct {
	id   string
	keys []string
}

// NewPrivilegedKeys resolves the keys of the registered action ids.
func NewPrivilegedKeys(local pitui.KeybindingsConfig, platform prefs.KeyPlatform, actions []string) *PrivilegedKeys {
	resolved := prefs.ResolvePrivilegedKeys(prefs.BuiltinKeybindings(platform), local).Keys
	ids := make([]string, 0, len(resolved))
	for id := range resolved {
		if prefs.IsPrivileged(id) && contains(actions, id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	keys := &PrivilegedKeys{}
	for _, id := range ids {
		keys.keys = append(keys.keys, privilegedKey{id, resolved[id]})
	}
	return keys
}

func contains(list []string, item string) bool {
	for _, entry := range list {
		if entry == item {
			return true
		}
	}
	return false
}

// Match is the privileged id this input is a key of (press or repeat; releases match too, so they are consumed).
func (k *PrivilegedKeys) Match(data string) (id string, release, ok bool) {
	for _, entry := range k.keys {
		for _, key := range entry.keys {
			if pitui.MatchesKeyID(data, key) {
				return entry.id, pitui.IsKeyRelease(data), true
			}
		}
	}
	return "", false, false
}
