package host

import (
	"context"
	"errors"
	"slices"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/host/route-admission.ts. A route's admission (slice 14 §2.3): the decision a route asks before
// it resolves an address or forwards a hold, so no host starts for a caller who will be denied. One decision per principal
// key and address with holds on the route, asked after `authorize`, shared by those holds and released with the last. It
// gates the address's path (pathGate): a hold is held back until its decision allows it, released upstream by a denial
// (its calls rejected `forbidden/revoked`), and forwarded again by the next allow (behind the path's fence). A decision
// lives with its holds on the routing host and takes no fate in a reload; its references follow the reference rules.
//
// Forced Go differences. Pi's `#settle` is an async loop started from the section that declared the decision (or whose
// flush found it waiting): it is an owned task (Loop.Go) here. `decision.settle` BLOCKS (its keys load, its live queries
// open: it is not called inside a section, and takes the sections it needs itself, as the query reader's Open does), and
// everything Pi writes after the `await` runs as one section (host.enter), where the verdict is checked and applied. A
// plugin's `admit` that panics is the declaration's error, as a `throw` is (the decision recovers it).

// RouteAdmit is a route's `admit` as its route calls it: plugin-free base code, but what it returns is checked as any
// `admit`'s (decision.declare). It returns the error Pi's throws.
type RouteAdmit func(principal data.Principal, address contracts.Address, self AdmitSelf) (any, error)

// routeAdmitting is Pi's `ADMITTING`: the route whose admission binds the references made under a context. A hold of a
// type placed behind that very route would wait on itself, so the route refuses it `invalid/cycle` (route.path).
var routeAdmitting = chordctx.NewKey[*route]("chord.route.admitting")

// askedKind is where a decision stands.
type askedKind uint8

const (
	// askedSettling: its verdict is being settled (its pinned keys load, its live queries open), and moved says something
	// it reads changed meanwhile, so what the settle read is of the past.
	askedSettling askedKind = iota
	// askedWaiting: the last settle found it not decided (a source loads or failed): the next change settles again.
	askedWaiting
	// askedSettled: it reached an allow or a denial once, so it is read synchronously from then on, in the flush of the
	// batch that changed it.
	askedSettled
	// askedReleased: its last hold left.
	askedReleased
)

// askedState is an asked's state. The states without fields are shared, and compared by identity (a settle that finds its
// asked in another state than its own ended or was replaced).
type askedState struct {
	kind  askedKind
	moved bool
}

var (
	// stateWaiting: not decided by its last settle.
	stateWaiting = &askedState{kind: askedWaiting}
	// stateSettled: decided once, read synchronously from then on.
	stateSettled = &askedState{kind: askedSettled}
	// stateReleased: its last hold left.
	stateReleased = &askedState{kind: askedReleased}
)

// asked is one decision and the holds it is asked for. verdict is what its holds were last told: nil before its first check
// (they are `loading`: nothing is resolved or forwarded for them). forwarded are the holds its last allow forwarded: a
// decision that cannot be read (a failed source) leaves them alone, however often it fails; only a denial takes them back.
// Holders and forwarded keep their order (Pi's `Set`).
type asked struct {
	decision  *Decision
	path      *path
	holders   []upstream
	forwarded []upstream
	verdict   *verdict
	state     *askedState
}

// noReport is what a route's decision reports its waits to: nobody (`parked_long` is an object's; its holds show what it
// waits on).
var noReport blocked = discardBlocked{}

type discardBlocked struct{}

func (discardBlocked) blockedBy(*blocker) {}

// routeAdmission is the decisions of one route, by address key and principal key, in the order they were asked.
type routeAdmission struct {
	host *chordHost
	// route is what a hold behind this very route is refused by: the route itself, the value of routeAdmitting.
	route *route
	admit RouteAdmit
	// context is the route's context, with the route named as the one admitting.
	context context.Context
	keys    []string
	asked   map[string]*asked
}

// newRouteAdmission makes the admission of route, which asks admit; ctx is the route's context.
func newRouteAdmission(host *chordHost, route *route, admit RouteAdmit, ctx context.Context) *routeAdmission {
	return &routeAdmission{
		host: host, route: route, admit: admit, context: chordctx.WithValue(ctx, routeAdmitting, route), asked: map[string]*asked{},
	}
}

// admitting is whether ctx binds a reference of this route's own admission (which may hold nothing behind the route).
func (a *routeAdmission) admitting(ctx context.Context) bool {
	route, found := chordctx.Value(ctx, routeAdmitting)
	return found && route == a.route
}

// addressGate is the gate of one address's path (routeAdmission.gate).
type addressGate struct {
	admission *routeAdmission
	address   contracts.Address
	path      func() *path
}

// gate is the gate of address's path; path gives it (the path is made with its gate).
func (a *routeAdmission) gate(address contracts.Address, path func() *path) pathGate {
	return &addressGate{admission: a, address: address, path: path}
}

func (g *addressGate) joined(holder upstream) bool {
	return g.admission.joined(g.address, g.path(), holder)
}

func (g *addressGate) left(holder upstream) { g.admission.left(g.address, holder) }

func (g *addressGate) unlinked() { g.admission.unlinked(g.path()) }

// unlinked: path lost its link. A hold is forwarded on a new link only under an allow that can be read now (fail closed: a
// new link is new work at the address's host, perhaps its start): the holds forwarded under a last allow whose source has
// failed since are taken back, `failed { waiting }` on it, until the source recovers. A source that only loads keeps its
// allow.
func (a *routeAdmission) unlinked(path *path) {
	for _, key := range slices.Clone(a.keys) {
		asked := a.asked[key]
		if asked == nil || asked.path != path || (asked.verdict != nil && asked.verdict.kind == verdictAllow) {
			continue
		}
		for _, holder := range slices.Clone(asked.forwarded) {
			asked.forwarded = remove(asked.forwarded, holder)
			a.tell(asked, holder)
		}
	}
}

// joined: holder joined address's path: its principal's decision is asked (once, however many holds share it); whether it
// allows now, so the hold is forwarded at once. A decision that cannot be declared (a throwing `admit`, a refused request,
// a hold behind this route) fails the hold `broken`.
func (a *routeAdmission) joined(address contracts.Address, path *path, holder upstream) bool {
	// A hold routed over a route acts for a principal (`route.path` refused one without).
	principal := holder.principal()
	key := a.key(address, principal)
	if existing := a.asked[key]; existing != nil {
		if !slices.Contains(existing.holders, holder) {
			existing.holders = append(existing.holders, holder)
		}
		if existing.verdict != nil && existing.verdict.kind == verdictAllow {
			if !slices.Contains(existing.forwarded, holder) {
				existing.forwarded = append(existing.forwarded, holder)
			}
			return true
		}
		a.tell(existing, holder)
		return false
	}
	settling := &askedState{kind: askedSettling}
	var made *asked
	made = &asked{
		decision: newDecision(decisionOwner{
			host:  a.host,
			label: "route " + address.Key,
			// The admission's references act as the host.
			caller:    caller{principal: a.host.principal, incarnation: a.host.incarnation},
			dependent: nil,
			moved:     func() { a.moved(made) },
		}),
		path:    path,
		holders: []upstream{holder},
		// Settling from its declaration on: what its requests tell while they are bound is a change to settle.
		state: settling,
	}
	a.keys = append(a.keys, key)
	a.asked[key] = made
	err := made.decision.declare(func(self AdmitSelf) (any, error) { return a.admit(principal, address, self) }, a.context)
	if err != nil {
		made.state = stateWaiting
		made.verdict = &verdict{kind: verdictBroken, err: data.AsChordError(err, address.Key)}
		a.tell(made, holder)
		return false
	}
	a.settle(made, settling)
	return false
}

// left: holder left: its decision is released with its last hold.
func (a *routeAdmission) left(address contracts.Address, holder upstream) {
	key := a.key(address, holder.principal())
	existing := a.asked[key]
	if existing == nil || !slices.Contains(existing.holders, holder) {
		return
	}
	existing.holders = remove(existing.holders, holder)
	existing.forwarded = remove(existing.forwarded, holder)
	if len(existing.holders) > 0 {
		return
	}
	existing.state = stateReleased
	delete(a.asked, key)
	a.keys = remove(a.keys, key)
	existing.decision.release()
}

// key is the key of the decision for principal at address.
func (a *routeAdmission) key(address contracts.Address, principal data.Principal) string {
	return address.Key + "\n" + a.host.principalKeyOf(principal, a.context)
}

// moved: something asked reads changed: read again at once when it decided before; remembered while it settles (what that
// settle read is of the past); else its verdict is settled again.
func (a *routeAdmission) moved(asked *asked) {
	state := asked.state
	switch state.kind {
	case askedSettled:
		a.apply(asked, asked.decision.now(a.context))
	case askedSettling:
		state.moved = true
	case askedWaiting:
		settling := &askedState{kind: askedSettling}
		asked.state = settling
		a.settle(asked, settling)
	case askedReleased:
		// Its last hold left while a source still told it: nobody is left to tell.
	}
}

// settle brings asked to a verdict (its pinned keys loaded, its live queries open) as the settle settling, on an owned task
// the loop counts (goTask, as awaitTask does: Pi's #settle is a chain of microtasks that `transported()` runs to its end, and
// `deliveredOn` waits for the task; what a settle waits for is in the host, the loads of what the decision reads, never a
// plugin's gate or a link, and a wait outside the loop is parked through chordctx.Await / Receive).
// **A verdict is told only when nothing it reads changed while it was settled:** a settle is asynchronous, and a denial
// arriving between its read and its continuation must not be answered with the allow it read before (that would resolve the
// address, start its host and leave the hold live with nobody left to correct it). It settles again until one is stable.
func (a *routeAdmission) settle(asked *asked, settling *askedState) {
	run := func() {
		for {
			// Not in a section: it blocks on the loads of what the decision reads, and ends (a broken verdict) when its decision
			// is released or this route's context ends.
			verdict := asked.decision.settleWait(noReport, a.context)
			ended := false
			a.host.enter(func() {
				// Released meanwhile (`left` marks the state before it releases), or the route's context ended with the host: the
				// verdict of a released or cancelled decision is no decision, so nobody is told (Pi's settle of a released
				// decision never settles, and the host's close ends the route's holds). A released decision is known by its
				// verdict's cause (ErrDecisionReleased), not by its code: a genuinely broken one (an ended source) carries the same
				// `released` code, and that one IS told (`held`).
				if asked.state != settling || (verdict.kind == verdictBroken && errors.Is(verdict.err, ErrDecisionReleased)) || a.context.Err() != nil {
					ended = true
					return
				}
				if settling.moved {
					// Something it reads changed: that settle read the past. Read again.
					settling.moved = false
					return
				}
				if verdict.kind == verdictAllow || verdict.kind == verdictDeny {
					asked.state = stateSettled
				} else {
					asked.state = stateWaiting
				}
				a.apply(asked, verdict)
				ended = true
			})
			if ended {
				return
			}
		}
	}
	goTask(a.host.loop(), run, run)
}

// apply: asked says verdict now. An allow forwards every hold not forwarded yet; a denial takes every hold back (released
// upstream, its calls rejected `forbidden/revoked`). A decision that cannot be made (a source failed or ended, a broken
// one) fails the holds held back and leaves the ones bound on the path's link alone, at its first failure and at every
// later one; without a link (it dropped, its next one is on its way) the forwarded ones are taken back too, since nothing
// is forwarded on a new link without an allow that can be read. A source that only loads tells nobody (the last decision
// stands; before the first, the holds stay `loading`).
func (a *routeAdmission) apply(asked *asked, verdict *verdict) {
	before := asked.verdict
	if verdict == before {
		return
	}
	asked.verdict = verdict
	path := asked.path
	if verdict.kind == verdictAllow || verdict.kind == verdictDeny {
		outcome := "allow"
		if verdict.kind == verdictDeny {
			outcome = "deny"
		}
		for _, holder := range asked.holders {
			if span := holder.span(); span != nil {
				span.AddEvent("chord.admit", telemetry.SpanAttributes{telemetry.Attr("outcome", outcome)})
			}
		}
	}
	if verdict.kind == verdictAllow {
		for _, holder := range slices.Clone(asked.holders) {
			if slices.Contains(asked.forwarded, holder) {
				continue
			}
			asked.forwarded = append(asked.forwarded, holder)
			path.forward(holder)
		}
		return
	}
	if verdict.kind == verdictDeny {
		asked.forwarded = nil
	}
	cause := held(verdict)
	if cause == nil {
		return
	}
	// Not bound anywhere: the holds forwarded under the last allow would be forwarded on the next link.
	if path.link() == nil {
		asked.forwarded = nil
	}
	told := held(before)
	// The same failure again (its source changed and still fails so): nothing new to tell the holds held back.
	again := told != nil && data.SameCause(*told, *cause)
	for _, holder := range slices.Clone(asked.holders) {
		if slices.Contains(asked.forwarded, holder) || (again && path.withholds(holder)) {
			continue
		}
		path.withhold(holder, *cause)
	}
}

// tell tells holder, held back, what holds it back now; nothing while its decision only loads.
func (a *routeAdmission) tell(asked *asked, holder upstream) {
	if cause := held(asked.verdict); cause != nil {
		asked.path.withhold(holder, *cause)
	}
}

// held is what holds a hold back under verdict: a denial, a source it waits on, a broken decision; nothing while it loads.
func held(verdict *verdict) *data.FailureCause {
	switch {
	case verdict == nil || verdict.kind == verdictAllow:
		return nil
	case verdict.kind == verdictDeny:
		return &data.FailureCause{Kind: data.FailureDenied, Error: verdict.err}
	case verdict.kind == verdictBroken:
		return &data.FailureCause{Kind: data.FailureBroken, Error: verdict.err}
	case verdict.shown != nil && verdict.shown.Kind == RefStateFailed:
		cause := verdict.shown.Cause
		return &cause
	}
	return nil
}

// remove is items without the first element equal to item (a new slice: callers iterate clones).
func remove[T comparable](items []T, item T) []T {
	at := slices.Index(items, item)
	if at < 0 {
		return items
	}
	return slices.Delete(slices.Clone(items), at, at+1)
}
