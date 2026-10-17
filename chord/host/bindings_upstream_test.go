package host

// Ports packages/chord/test/bindings.test.ts (Pi b45e0ebc): one shared binding per host and key; a hold implies the state
// of its variant (units: shape, then snapshots); method-only references; origins of client holds; protocol version 4.
// Pi's TypeScript-only narrowing (ts-expect-error, `assert.ok(ref.variant === ...)`) has no Go form; the runtime reads it
// guards are kept.

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var bindingsNoteSchema = tArray(tString())

func bindingsNotesContract(version int, extra Fields[MethodSpec]) *Contract {
	methods := Fields[MethodSpec]{{"add", Method(tString(), nil)}, {"rename", Method(tString(), nil)}}
	methods = append(methods, extra...)
	return Remote("t.e8-notes", RemoteDef{
		Version: version, Input: tObject("list", tOptional(tString(Schema{"default": "main"}))), Host: NewHostNamed("server"),
		Variants: Fields[VariantDef]{{"main", VariantDef{
			State:   Fields[Schema]{{"notes", bindingsNoteSchema}, {"title", tString()}},
			Methods: methods,
		}}},
	})
}

var bindingsNotes = bindingsNotesContract(1, nil)

func serveBindingsNotes(t *testing.T, host *HostImpl) {
	t.Helper()
	requestsServe(t, host.Serve, bindingsNotes, func(_ any, _ Principal, self Self) (Implementation, error) {
		notes, err := self.State("notes", bindingsNoteSchema, []any{}, nil)
		if err != nil {
			return nil, err
		}
		title, err := self.State("title", tString(), "untitled", nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(
			"variant", "main", "notes", notes, "title", title,
			"add", methodFunc(func(text any, ctx Ctx) (any, error) {
				return nil, notes.Change(ctx, func(draft any) {
					if _, err := draft.(*delta.Array).Push(text); err != nil {
						panic(err)
					}
				})
			}),
			"rename", methodFunc(func(text any, ctx Ctx) (any, error) { return nil, title.Replace(ctx, text, CellReplaceOptions{}) }),
		), nil
	}, ServeOptions{})
}

type bindingsSetup struct {
	server, browser *HostImpl
	link            *countingLink
}

// bindingsSetupOf is a server serving Notes, and a browser whose end of the link counts what crosses it.
func bindingsSetupOf(t *testing.T, linger *LingerOptions) bindingsSetup {
	t.Helper()
	server := requestsHost(t, HostOptions{Name: "server"})
	serveBindingsNotes(t, server)
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}}); err != nil {
		t.Fatal(err)
	}
	link := newCountingLink(up)
	browser := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Linger: linger, Upstream: &UpstreamOptions{Link: link, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	return bindingsSetup{server, browser, link}
}

func bindingsHold(t *testing.T, host *HostImpl, typ *Contract, input any, options *GetOptions) Ref {
	t.Helper()
	ref, err := host.Hold(typ, input, options)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func bindingsGet(t *testing.T, host *HostImpl, typ *Contract, input any, options *GetOptions) Ref {
	t.Helper()
	ref, err := host.Get(typ, input, options)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func bindingsNotesLen(t *testing.T, ref Ref) int {
	t.Helper()
	return len(requestsReadable(t, ref, "notes").Value().([]any))
}

func ready(t *testing.T, refs ...Ref) {
	t.Helper()
	for _, ref := range refs {
		if err := readyErr(t, ref); err != nil {
			t.Fatal(err)
		}
	}
}

// bindings.test.ts:88-131 "N references to one object: one hold, one snapshot per member, one frame per change, one
// release"
func TestBindingsNReferencesToOneObjectOneHoldOneSnapshotPerMemberOneFramePerChangeOneRelease(t *testing.T) {
	s := bindingsSetupOf(t, nil)
	panel := Local("t.e8-panel", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"count", tNumber()}}})
	requestsServe(t, s.browser.Serve, panel, func(_ any, _ Principal, self Self) (Implementation, error) {
		held, err := self.Get(bindingsNotes, map[string]any{}, nil)
		if err != nil {
			return nil, err
		}
		count, err := self.Derive(Sources{held.Member("notes").(Readable[any])}, func(values ...any) any { return float64(len(values[0].([]any))) })
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("count", count), nil
	}, ServeOptions{})
	a := bindingsHold(t, s.browser, bindingsNotes, map[string]any{}, nil)
	b := bindingsHold(t, s.browser, bindingsNotes, map[string]any{"list": "main"}, nil)
	panelRef := bindingsHold(t, s.browser, panel, map[string]any{}, nil)
	ready(t, a, b, panelRef)
	viaCtx, err := s.browser.Root(func(ctx Ctx) (any, error) {
		ref, err := ctx.Get(bindingsNotes, map[string]any{}, nil)
		if err != nil {
			return nil, err
		}
		return requestsReadable(t, ref, "title").Value(), nil
	}, RootOptions{})
	if err != nil || viaCtx != "untitled" {
		t.Fatalf("via ctx = %v, %v", viaCtx, err)
	}
	if got := s.link.countSent("hold"); got != 1 {
		t.Fatalf("holds = %d, want one for every reference", got)
	}
	var snaps []string
	for _, frame := range stateFrames(s.link.receivedFrames()) {
		if frame.As == "snap" {
			snaps = append(snaps, frame.M)
		}
	}
	slices.Sort(snaps)
	if !slices.Equal(snaps, []string{"notes", "title"}) {
		t.Fatalf("snapshots %v, want one per member", snaps)
	}

	before := len(stateFrames(s.link.receivedFrames()))
	mustCall(t, a, "add", "milk", nil)
	waitFor(t, "every reference to see the change", func() bool {
		return bindingsNotesLen(t, a) == 1 && bindingsNotesLen(t, b) == 1 && requestsReadable(t, panelRef, "count").Value() == float64(1)
	})
	time.Sleep(5 * time.Millisecond)
	if got := len(stateFrames(s.link.receivedFrames())) - before; got != 1 {
		t.Fatalf("%d state frames for one change, want one whatever N", got)
	}

	sent := len(traffic(s.link.sentFrames()))
	a.Release()
	panelRef.Release()
	time.Sleep(5 * time.Millisecond)
	if got := len(traffic(s.link.sentFrames())); got != sent {
		t.Fatalf("releasing all but one reference sent %d frames", got-sent)
	}
	b.Release()
	waitFor(t, "the release", func() bool { return s.link.countSent("release") == 1 })
}

// bindings.test.ts:133-144 "a held reference stays subscribed without listeners; values are readable at once after
// ready"
func TestBindingsAHeldReferenceStaysSubscribedWithoutListenersValuesAreReadableAtOnceAfterReady(t *testing.T) {
	s := bindingsSetupOf(t, nil)
	notes := bindingsHold(t, s.browser, bindingsNotes, map[string]any{}, nil)
	ready(t, notes)
	if got := bindingsNotesLen(t, notes); got != 0 {
		t.Fatalf("notes = %d", got)
	}
	mustCall(t, notes, "add", "bread", nil)
	waitFor(t, "the note", func() bool { return bindingsNotesLen(t, notes) == 1 })
	if got := requestsReadable(t, notes, "title").Value(); got != "untitled" {
		t.Fatalf("title = %v", got)
	}
}

// lingerTimer is one linger timer armed through HostImpl.lingerAfter: its delay, its callback, and whether it was stopped.
type lingerTimer struct {
	delay   time.Duration
	fire    func()
	stopped bool
}

// manualLinger replaces host's linger timers with ones the test fires, so the linger window does not depend on how
// the scheduler runs the test (a Windows runner once ran the 10 ms sleep past the 40 ms linger).
func manualLinger(host *HostImpl) (live func() []*lingerTimer) {
	var mu sync.Mutex
	var timers []*lingerTimer
	host.lingerAfter = func(delay time.Duration, fn func()) func() {
		timer := &lingerTimer{delay: delay}
		timer.fire = func() { host.post(fn) }
		mu.Lock()
		timers = append(timers, timer)
		mu.Unlock()
		return func() {
			mu.Lock()
			timer.stopped = true
			mu.Unlock()
		}
	}
	return func() []*lingerTimer {
		mu.Lock()
		defer mu.Unlock()
		var out []*lingerTimer
		for _, timer := range timers {
			if !timer.stopped {
				out = append(out, timer)
			}
		}
		return out
	}
}

// bindings.test.ts:147-166 "a lingering binding keeps its hold and state; a reference taken meanwhile costs nothing on
// the link". Pi sleeps 10 ms inside the 40 ms linger; here the linger timer fires only when the test fires it, so the
// window holds however the test is scheduled.
func TestBindingsLingerAKeepsItsHoldAndStateAReferenceTakenMeanwhileCostsNothingOnTheLink(t *testing.T) {
	s := bindingsSetupOf(t, &LingerOptions{Types: map[string]time.Duration{bindingsNotes.Name: 40 * time.Millisecond}})
	live := manualLinger(s.browser)
	first := bindingsGet(t, s.browser, bindingsNotes, map[string]any{}, nil)
	frames := len(traffic(s.link.receivedFrames()))
	first.Release()
	if timers := live(); len(timers) != 1 || timers[0].delay != 40*time.Millisecond {
		t.Fatalf("linger timers after the release: %d, want one of 40ms", len(timers))
	}
	if got := s.link.countSent("release"); got != 0 {
		t.Fatalf("released %d times before the linger", got)
	}
	// A hook mounting and unmounting: no frames either way.
	for range 3 {
		again := bindingsGet(t, s.browser, bindingsNotes, map[string]any{}, nil)
		if got := bindingsNotesLen(t, again); got != 0 {
			t.Fatalf("notes = %d", got)
		}
		again.Release()
	}
	if got := s.link.countSent("hold"); got != 1 {
		t.Fatalf("holds = %d", got)
	}
	if got := len(traffic(s.link.receivedFrames())); got != frames {
		t.Fatalf("%d new frames crossed the link, want none", got-frames)
	}
	if got := s.link.countSent("release"); got != 0 {
		t.Fatalf("released %d times before the linger", got)
	}
	// Each re-get stopped the pending linger and each release armed a new one: one runs now. The linger ends.
	timers := live()
	if len(timers) != 1 || timers[0].delay != 40*time.Millisecond {
		t.Fatalf("live linger timers: %d, want one of 40ms", len(timers))
	}
	timers[0].fire()
	waitFor(t, "the release after the linger", func() bool { return s.link.countSent("release") == 1 })
}

// bindings.test.ts:168-181 "at most `cap` bindings linger: the least recently released goes first"
func TestBindingsLingerAtMostCapBindingsLingerTheLeastRecentlyReleasedGoesFirst(t *testing.T) {
	capOne := 1
	s := bindingsSetupOf(t, &LingerOptions{Types: map[string]time.Duration{bindingsNotes.Name: 10 * time.Second}, Cap: &capOne})
	a := bindingsGet(t, s.browser, bindingsNotes, map[string]any{"list": "a"}, nil)
	b := bindingsGet(t, s.browser, bindingsNotes, map[string]any{"list": "b"}, nil)
	a.Release()
	time.Sleep(5 * time.Millisecond)
	if got := s.link.countSent("release"); got != 0 {
		t.Fatalf("releases = %d", got)
	}
	b.Release()
	waitFor(t, "the release", func() bool { return s.link.countSent("release") == 1 })
	var release ReleaseFrame
	var holdOfA HoldFrame
	for _, frame := range s.link.sentFrames() {
		switch frame := frame.(type) {
		case ReleaseFrame:
			release = frame
		case HoldFrame:
			if inputField(frame.Input, "list") == "a" {
				holdOfA = frame
			}
		}
	}
	if release.ID == 0 || release.ID != holdOfA.ID {
		t.Fatalf("released %d, the hold of a is %d", release.ID, holdOfA.ID)
	}
}

// bindings.test.ts:184-229 "normalized-equal inputs and equal principal keys share; other principals and fingerprints do
// not"
func TestBindingsIdentityNormalizedEqualInputsAndEqualPrincipalKeysShareOtherPrincipalsAndFingerprintsDoNot(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	notesV2 := bindingsNotesContract(2, Fields[MethodSpec]{{"clear", Method(nil, nil)}})
	requestsServe(t, server.Serve, notesV2, func(_ any, _ Principal, self Self) (Implementation, error) {
		notes, err := self.State("notes", bindingsNoteSchema, []any{}, nil)
		if err != nil {
			return nil, err
		}
		title, err := self.State("title", tString(), "untitled", nil)
		if err != nil {
			return nil, err
		}
		noop := methodFunc(func(any, Ctx) (any, error) { return nil, nil })
		return chordjson.ObjectOf("variant", "main", "notes", notes, "title", title, "add", noop, "rename", noop, "clear", noop), nil
	}, ServeOptions{Accepts: Accepts{1: func(current Implementation) Implementation { return current }}})
	// A worker holds the server's objects; the principals it forwards key its bindings.
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"worker": "w"}, Kind: KindHost, Name: new("session:w")}); err != nil {
		t.Fatal(err)
	}
	link := newCountingLink(up)
	worker := requestsHost(t, HostOptions{Name: "session:w", Upstream: &UpstreamOptions{Link: link, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	as := func(principal any) *GetOptions { return &GetOptions{Principal: principal, HasPrincipal: true} }
	refs := []Ref{
		bindingsHold(t, worker, bindingsNotes, map[string]any{}, nil),
		bindingsHold(t, worker, bindingsNotes, map[string]any{"list": "main"}, nil),
		bindingsHold(t, worker, bindingsNotes, map[string]any{}, as(map[string]any{"user": "a", "grant": 1.0})),
		bindingsHold(t, worker, bindingsNotes, map[string]any{}, as(map[string]any{"grant": 1.0, "user": "a"})),
		bindingsHold(t, worker, bindingsNotes, map[string]any{}, as(map[string]any{"user": "a", "grant": 2.0})),
		bindingsHold(t, worker, notesV2, map[string]any{}, nil),
	}
	ready(t, refs...)
	// host (shared by two inputs), user a grant 1 (shared by two key orders), user a grant 2, the v2 contract.
	if got := link.countSent("hold"); got != 4 {
		t.Fatalf("holds = %d, want 4", got)
	}
}

var bindingsMatrix = Remote("t.e8-matrix", RemoteDef{
	Version: 1, Input: tObject("variant", tString()), Host: NewHostNamed("server"),
	Variants: Fields[VariantDef]{
		{"values", VariantDef{State: Fields[Schema]{{"none", tNull()}, {"empty", tArray(tString())}, {"maybe", tUnknown()}}}},
		{"bare", VariantDef{Methods: Fields[MethodSpec]{{"ping", Method(nil, tString())}}}},
		{"broken", VariantDef{State: Fields[Schema]{{"n", tNumber()}}}},
	},
})

func serveBindingsMatrix(t *testing.T, host *HostImpl) {
	t.Helper()
	requestsServe(t, host.Serve, bindingsMatrix, func(input any, _ Principal, self Self) (Implementation, error) {
		switch inputField(input, "variant") {
		case "bare":
			return chordjson.ObjectOf("variant", "bare", "ping", methodFunc(func(any, Ctx) (any, error) { return "pong", nil })), nil
		case "broken":
			source, err := self.State("source", tString(), "not a number", nil)
			if err != nil {
				return nil, err
			}
			// A derivation exposed under a number schema: its snapshot fails validation.
			n, err := self.Derive(Sources{source}, func(values ...any) any { return values[0] })
			if err != nil {
				return nil, err
			}
			return chordjson.ObjectOf("variant", "broken", "n", n), nil
		}
		none, err := self.State("none", tNull(), nil, nil)
		if err != nil {
			return nil, err
		}
		empty, err := self.State("empty", tArray(tString()), []any{}, nil)
		if err != nil {
			return nil, err
		}
		maybe, err := self.Derive(Sources{none}, func(...any) any { return nil })
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "values", "none", none, "empty", empty, "maybe", maybe), nil
	}, ServeOptions{})
}

// bindings.test.ts:249-277 "null, empty and undefined values hydrate; a state-free variant is ready; a failed snapshot
// rejects". Go has no undefined: a derivation's nil reads as nil.
func TestHydrationOverALinkNullEmptyAndUndefinedValuesHydrateAStateFreeVariantIsReadyAFailedSnapshotRejects(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	serveBindingsMatrix(t, server)
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}}); err != nil {
		t.Fatal(err)
	}
	browser := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	values := bindingsGet(t, browser, bindingsMatrix, map[string]any{"variant": "values"}, nil)
	if variant := values.Variant(); variant == nil || *variant != "values" {
		t.Fatalf("variant %v", variant)
	}
	if got := requestsReadable(t, values, "none").Value(); got != nil {
		t.Fatalf("none = %v, want null", got)
	}
	if got := jsonText(t, requestsReadable(t, values, "empty").Value()); got != "[]" {
		t.Fatalf("empty = %s", got)
	}
	if got := requestsReadable(t, values, "maybe").Value(); got != nil {
		t.Fatalf("maybe = %v, want undefined", got)
	}
	bare := bindingsGet(t, browser, bindingsMatrix, map[string]any{"variant": "bare"}, nil)
	if got := mustCall(t, bare, "ping", nil, nil); got != "pong" {
		t.Fatalf("ping = %v", got)
	}
	// A narrowed reference does not show a member of another variant: it is not a member of the bare variant.
	if member := bare.Member("none"); member != nil {
		readable := member.(Readable[any])
		if thrown := chordPanic(func() { _ = readable.Value() }); thrown == nil || thrown.Code != CodeNotInVariant {
			t.Fatalf("none of the bare variant threw %v, want not_in_variant", thrown)
		}
	}
	broken := bindingsHold(t, browser, bindingsMatrix, map[string]any{"variant": "broken"}, nil)
	if err := readyErr(t, broken); !IsChordError(err, CodeInvalid) {
		t.Fatalf("broken ready: %v", err)
	}
	if broken.Status() != RefGone {
		t.Fatalf("broken status %s", broken.Status())
	}
}

// bindings.test.ts:279-291 "reading before the first hydration throws not_ready"
func TestHydrationOverALinkReadingBeforeTheFirstHydrationThrowsNotReady(t *testing.T) {
	s := bindingsSetupOf(t, nil)
	notes := bindingsHold(t, s.browser, bindingsNotes, map[string]any{}, nil)
	requireNotReady(t, "notes before the first hydration", func() { _ = requestsReadable(t, notes, "notes").Value() })
	ready(t, notes)
	if got := bindingsNotesLen(t, notes); got != 0 {
		t.Fatalf("notes = %d", got)
	}
}

// bindings.test.ts:294-319 "transfer no state alone; a full reference joining starts it once; its release stops it"
func TestMethodOnlyReferencesTransferNoStateAloneAFullReferenceJoiningStartsItOnceItsReleaseStopsIt(t *testing.T) {
	s := bindingsSetupOf(t, nil)
	caller := bindingsHold(t, s.browser, bindingsNotes, map[string]any{}, &GetOptions{State: new(false)})
	ready(t, caller)
	mustCall(t, caller, "add", "x", nil)
	var hold HoldFrame
	for _, frame := range s.link.sentFrames() {
		if h, ok := frame.(HoldFrame); ok {
			hold = h
			break
		}
	}
	if !hold.NoState {
		t.Fatalf("the hold %+v wants state", hold)
	}
	if got := stateFrames(s.link.receivedFrames()); len(got) != 0 {
		t.Fatalf("state frames %v for a method-only reference", got)
	}
	requireNotReady(t, "notes of a method-only reference", func() { _ = requestsReadable(t, caller, "notes").Value() })

	viewer := bindingsHold(t, s.browser, bindingsNotes, map[string]any{}, nil)
	ready(t, viewer)
	if got := jsonText(t, requestsReadable(t, viewer, "notes").Value()); got != `["x"]` {
		t.Fatalf("notes = %s", got)
	}
	if got := s.link.countSent("sub"); got != 1 {
		t.Fatalf("subs = %d, want 1", got)
	}
	if got := s.link.countSent("hold"); got != 1 {
		t.Fatalf("holds = %d, want the same hold", got)
	}
	viewer.Release()
	waitFor(t, "the unsub", func() bool { return s.link.countSent("unsub") == 1 })
	after := len(stateFrames(s.link.receivedFrames()))
	mustCall(t, caller, "add", "y", nil)
	time.Sleep(5 * time.Millisecond)
	if got := len(stateFrames(s.link.receivedFrames())); got != after {
		t.Fatalf("%d state frames once only method-only references remain", got-after)
	}
}

var bindingsCounter = Remote("t.e8-counter", RemoteDef{
	Version: 1, Input: tObject("session", tString()), Host: NewHostFamily("session:", "session"),
	Variants: Fields[VariantDef]{{"main", VariantDef{
		State:   Fields[Schema]{{"n", tNumber()}},
		Methods: Fields[MethodSpec]{{"add", Method(tNumber(), nil)}},
	}}},
})

type bindingsCluster struct {
	server   *HostImpl
	mu       sync.Mutex
	worker   *HostImpl
	toWorker *countingLink
}

func newBindingsCluster(t *testing.T) *bindingsCluster {
	t.Helper()
	c := &bindingsCluster{server: requestsHost(t, HostOptions{Name: "server"})}
	c.server.Route("session:", Route{
		Principal: func(name string) Principal { return map[string]any{"host": name} },
		Authorize: func(Principal, string) (bool, error) { return true, nil },
		Resolve: func(name string) (Link, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.toWorker != nil {
				return c.toWorker, nil
			}
			down, up := MemoryLinkPair()
			worker := requestsHost(t, HostOptions{Name: name, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
			requestsServe(t, worker.Serve, bindingsCounter, func(_ any, _ Principal, self Self) (Implementation, error) {
				n, err := self.State("n", tNumber(), float64(0), nil)
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("variant", "main", "n", n, "add", methodFunc(func(amount any, ctx Ctx) (any, error) {
					return nil, n.ChangeValue(ctx, func(value any) any { return value.(float64) + amount.(float64) })
				})), nil
			}, ServeOptions{})
			c.worker, c.toWorker = worker, newCountingLink(down)
			return c.toWorker, nil
		},
	})
	return c
}

func (c *bindingsCluster) browser(t *testing.T) (*HostImpl, Link) {
	t.Helper()
	down, up := MemoryLinkPair()
	if err := c.server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	return requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}}), up
}

// origins are the origins of the holds the worker serves for its parent ("" for a host's own).
func (c *bindingsCluster) origins() []string {
	c.mu.Lock()
	worker := c.worker
	c.mu.Unlock()
	var origins []string
	if worker == nil {
		return nil
	}
	worker.run(func() {
		if worker.upstream == nil {
			return
		}
		for _, entry := range worker.upstream.served.Values() {
			if entry.local {
				origins = append(origins, entry.origin)
			}
		}
	})
	return origins
}

func (c *bindingsCluster) sentToWorker() []Frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.toWorker == nil {
		return nil
	}
	return c.toWorker.sentFrames()
}

func nonEmpty(values []string) []string {
	return slices.DeleteFunc(slices.Clone(values), func(value string) bool { return value == "" })
}

// bindings.test.ts:386-415 "a relayed hold carries its client connection's id; the server's own holds carry none"
func TestOriginsARelayedHoldCarriesItsClientConnectionsIdTheServersOwnHoldsCarryNone(t *testing.T) {
	c := newBindingsCluster(t)
	one, oneUp := c.browser(t)
	two, _ := c.browser(t)
	ready(t, bindingsHold(t, one, bindingsCounter, map[string]any{"session": "S1"}, nil))
	ready(t, bindingsHold(t, two, bindingsCounter, map[string]any{"session": "S1"}, nil))
	ready(t, bindingsHold(t, c.server, bindingsCounter, map[string]any{"session": "S1"}, nil))
	var holds []HoldFrame
	for _, frame := range c.sentToWorker() {
		if hold, ok := frame.(HoldFrame); ok {
			holds = append(holds, hold)
		}
	}
	if len(holds) != 3 {
		t.Fatalf("holds = %d, want 3", len(holds))
	}
	var relayed []HoldFrame
	own := 0
	for _, hold := range holds {
		if hold.Origin != "" {
			relayed = append(relayed, hold)
		} else {
			own++
		}
	}
	if len(relayed) != 2 || own != 1 {
		t.Fatalf("relayed %d, own %d: want 2 and 1", len(relayed), own)
	}
	if relayed[0].Origin == relayed[1].Origin {
		t.Fatal("two tabs, two origins")
	}

	// Disconnect is departure: the worker loses that origin at once; a reconnect holds under a new id.
	before := map[string]bool{}
	for _, origin := range c.origins() {
		before[origin] = true
	}
	oneUp.Close()
	waitFor(t, "the worker to lose the origin", func() bool { return len(nonEmpty(c.origins())) == 1 })
	three, _ := c.browser(t)
	ready(t, bindingsHold(t, three, bindingsCounter, map[string]any{"session": "S1"}, nil))
	after := nonEmpty(c.origins())
	if len(after) != 2 {
		t.Fatalf("origins %v, want 2", after)
	}
	if !slices.ContainsFunc(after, func(origin string) bool { return !before[origin] }) {
		t.Fatal("a new connection gets a new id")
	}
}

// bindings.test.ts:417-436 "an origin a client sends is ignored: the server stamps its own"
func TestOriginsAnOriginAClientSendsIsIgnoredTheServerStampsItsOwn(t *testing.T) {
	c := newBindingsCluster(t)
	down, raw := MemoryLinkPair()
	if err := c.server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	raw.Receive(func(Frame) {})
	_, _ = raw.Send(HelloFrame{Protocol: ProtocolVersion, Name: "raw", Kind: KindClient, CodeVersion: 0})
	_, _ = raw.Send(HoldFrame{ID: 1, At: "session:S1", Token: bindingsCounter.Description, Input: map[string]any{"session": "S1"}, Origin: "spoofed"})
	waitFor(t, "the hold to reach the worker", func() bool { return countType(c.sentToWorker(), "hold") == 1 })
	var hold HoldFrame
	for _, frame := range c.sentToWorker() {
		if h, ok := frame.(HoldFrame); ok {
			hold = h
		}
	}
	if hold.Origin == "" || hold.Origin == "spoofed" {
		t.Fatalf("origin %q, want the server's own", hold.Origin)
	}
}

// bindings.test.ts:440-460 "a version 3 peer is refused; shapes carry no code version"
func TestProtocolVersion4AVersion3PeerIsRefusedShapesCarryNoCodeVersion(t *testing.T) {
	s := bindingsSetupOf(t, nil)
	ready(t, bindingsHold(t, s.browser, bindingsNotes, map[string]any{}, nil))
	var shape *ShapeFrame
	for _, frame := range s.link.receivedFrames() {
		if f, ok := frame.(ShapeFrame); ok {
			shape = &f
			break
		}
	}
	if shape == nil {
		t.Fatal("no shape frame")
	}
	data, err := EncodeFrame(*shape)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "codeVersion") {
		t.Fatalf("shape %s carries a code version", data)
	}
	state := slices.Clone(shape.State)
	slices.Sort(state)
	if !slices.Equal(state, []string{"notes", "title"}) {
		t.Fatalf("shape state %v", state)
	}

	down, raw := MemoryLinkPair()
	if err := s.server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "old"}}); err != nil {
		t.Fatal(err)
	}
	closed := make(chan string, 1)
	raw.OnClose(func(reason string) { closed <- reason })
	raw.Receive(func(Frame) {})
	_, _ = raw.Send(HelloFrame{Protocol: 3, Name: "old", Kind: KindClient, CodeVersion: 0})
	select {
	case reason := <-closed:
		if want := "protocol-version 4"; reason != want {
			t.Fatalf("close reason %q, want %q", reason, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the version 3 peer was not refused")
	}
	if ProtocolVersion != 4 {
		t.Fatalf("ProtocolVersion = %d, want 4", ProtocolVersion)
	}
}
