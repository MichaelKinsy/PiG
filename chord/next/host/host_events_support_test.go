package host

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
)

// Ports the event helpers of packages/chord/test/support.ts (Pi 8d30e6306, decision "Events: fire-and-forget members"):
// Sample, Match/MatchV2/serveMatch, Ticker/NamedTicker/TickerView/serveTickers/ticking, recorder, until/waitLive,
// flushed. Pi's single JS turn is a host section here (`turn`): code that emits and subscribes "in one turn" runs in
// one; `flushed` is a section after it (the queued flush runs at the end of the section that queued it).

// Sample is an event's value in the tests: a paddle's position.
var Sample = contracts.Obj(contracts.Prop("y", contracts.Num()))

func sample(y float64) *delta.JsonObject { return jsonObject("y", y) }

// Match is a server type with events: a player has the score, two event members (`ball`, `paddle`) and a method; a
// watcher (a guest) the score only.
var Match = mustContract(contracts.Remote("app.match", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("id", contracts.Int())},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants: []contracts.VariantEntry{
		contracts.Var("player", contracts.VariantDeclaration{
			State:   []contracts.Property{contracts.Prop("score", contracts.Int())},
			Events:  []contracts.Property{contracts.Prop("paddle", Sample), contracts.Prop("ball", Sample)},
			Methods: []contracts.MethodEntry{contracts.Meth("bump", contracts.NewMethod())},
		}),
		contracts.Var("watcher", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("score", contracts.Int())}}),
	},
}))

// tickerCounter is the state the ticker counts in.
var tickerCounter = func() *documents.StateDefinition {
	definition, err := documents.DefineState("ticker-counter", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// Ticker is a host-lifetime ticker: an event member, a count, and a method emitting inside a transaction that fails.
var Ticker = mustContract(contracts.Local("app.ticker", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("ticks", contracts.Event(Sample)),
		contracts.Mem("count", contracts.Int()),
		contracts.Mem("failing", contracts.NewMethod(Sample)),
	},
}))

// NamedTicker is a ticker per name (lives while referenced): what a test makes and drops references to.
var NamedTicker = mustContract(contracts.Local("app.named-ticker", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("name", contracts.Str())},
	Members: []contracts.LocalEntry{contracts.Mem("ticks", contracts.Event(Sample))},
}))

// TickerView re-exposes the ticker's member (a view of it).
var TickerView = mustContract(contracts.Local("app.ticker-view", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("name", contracts.Str())},
	Members: []contracts.LocalEntry{contracts.Mem("ticks", contracts.Event(Sample))},
}))

// tickerProbe is what a test reaches of its objects: each attempt's sources and `self`.
type tickerProbe struct {
	mu          sync.Mutex
	sources     []contracts.EventSource[delta.JsonValue]
	selves      []Self
	named       []contracts.EventSource[delta.JsonValue]
	namedSelves []Self
}

func (p *tickerProbe) lastSource() contracts.EventSource[delta.JsonValue] {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sources[len(p.sources)-1]
}

func (p *tickerProbe) lastSelf() Self {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.selves[len(p.selves)-1]
}

func (p *tickerProbe) lastNamed() contracts.EventSource[delta.JsonValue] {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.named[len(p.named)-1]
}

func (p *tickerProbe) lastNamedSelf() Self {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.namedSelves[len(p.namedSelves)-1]
}

// serveTickers serves the ticker (its sources and selves recorded in probe), named tickers and their views.
func serveTickers(host FacetHost, probe *tickerProbe) error {
	err := host.Serve(Ticker, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		ticks := self.Events()
		counter := stateOf(self, tickerCounter)
		probe.mu.Lock()
		probe.sources = append(probe.sources, ticks)
		probe.selves = append(probe.selves, self)
		probe.mu.Unlock()
		return contracts.Implementation{Members: contracts.VariantImplementation{
			Events: map[string]contracts.EventMember[delta.JsonValue]{"ticks": ticks},
			State: map[string]chord.Watchable[delta.JsonValue]{
				"count": self.Derive([]any{counter.s}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("n"), nil
				}),
			},
			Methods: map[string]contracts.MethodFunction{
				"failing": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					_, err := self.Tx(func(tx documents.Tx) (any, error) {
						draft, err := tx.Draft(counter.s)
						if err != nil {
							return nil, err
						}
						_ = draft.Set("n", 1.0)
						if err := ticks.Emit(args); err != nil {
							return nil, err
						}
						return nil, errors.New("refused")
					}, ctx)
					return nil, err
				},
			},
		}}, nil
	}, ServeOptions{})
	if err != nil {
		return err
	}
	err = host.Serve(NamedTicker, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		ticks := self.Events()
		probe.mu.Lock()
		probe.named = append(probe.named, ticks)
		probe.namedSelves = append(probe.namedSelves, self)
		probe.mu.Unlock()
		return contracts.Implementation{Members: contracts.VariantImplementation{
			Events: map[string]contracts.EventMember[delta.JsonValue]{"ticks": ticks},
		}}, nil
	}, ServeOptions{})
	if err != nil {
		return err
	}
	return host.Serve(TickerView, func(params *delta.JsonObject, self Self) (contracts.Implementation, error) {
		name, _ := params.Value("name").(string)
		return contracts.Implementation{Members: contracts.VariantImplementation{
			Events: map[string]contracts.EventMember[delta.JsonValue]{
				"ticks": self.Get(NamedTicker, jsonObject("name", name)).Events("ticks"),
			},
		}}, nil
	}, ServeOptions{})
}

// tickingHost is what `ticking` returns: Pi's `{ host, errors, probe, source }`.
type tickingHost struct {
	Host   Host
	Errors *hostErrors
	Probe  *tickerProbe
}

// Source is the ticker's current source.
func (k *tickingHost) Source() contracts.EventSource[delta.JsonValue] { return k.Probe.lastSource() }

// ticking is a loaded host serving the ticker, with its probe and the ticker's current source (the first Get makes it).
func ticking(t testing.TB, options loadedOptions) *tickingHost {
	t.Helper()
	probe := &tickerProbe{}
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error { return serveTickers(h, probe) }, options)
	return &tickingHost{Host: host, Errors: errs, Probe: probe}
}

// turn runs work as one section of host: Pi's one JS turn (work does not yield; the microtasks it queued run after it).
func hostTurn(host Host, work func()) { host.(*chordHost).enter(work) }

// flushed waits until what the sections so far queued ran: Pi's `await flushed()`.
func flushed(host Host) { hostTurn(host, func() {}) }

// recorder is a listener recording `y` of every event it gets, in seen (tagged `tag` + y when tag is not empty).
func recorder(seen *[]any, tag string) func(contracts.EventDelivery[delta.JsonValue]) {
	return func(e contracts.EventDelivery[delta.JsonValue]) {
		y := e.Value.(*delta.JsonObject).Value("y")
		if tag == "" {
			*seen = append(*seen, y)
			return
		}
		*seen = append(*seen, tag+strconv.FormatFloat(y.(float64), 'f', -1, 64))
	}
}

// on subscribes listener to ref's event member `member` (Pi's `await ref.member.on(listener, context)`).
func on(t testing.TB, ref *Reference, member string, listener func(contracts.EventDelivery[delta.JsonValue]), ctx context.Context) contracts.EventSubscription {
	t.Helper()
	subscription, err := ref.Events(member).On(listener, ctx)
	must(t, err)
	return subscription
}

// waitLive waits until ref is live.
func waitLive(t testing.TB, ref *Reference) { t.Helper(); untilKind(t, ref, RefStateLive) }
