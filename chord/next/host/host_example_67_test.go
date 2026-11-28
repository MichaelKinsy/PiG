package host

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Twin of packages/chord/test/examples/67-events.ts: events, fire-and-forget members (decision "Events: fire-and-forget
// members"). A ticking server object emits a paddle sample every 10 ms and commits its tick count every 100 ms; two
// clients hold it over links and subscribe. An event goes once to each reference bound before the emit, never folded,
// never kept: the fast client gets every sample; the slow one (its link delivers 100 ms late) fills its event window and
// the server drops what would wait for it (nothing queues for a slow consumer), while its state still arrives whole.
//
// The script runs for one second and prints what each client saw and the server's `chord.events.*` counters; it asserts
// nothing. Here the ticking ends after the 100 samples of that second (Pi's routine is endless and the process exits),
// so the printed lines become checks that hold whatever the scheduling: the script's steps 1 to 4 in order.

// ex67Samples is the samples of the script's one second at one per 10 ms.
const ex67Samples = 100

// ex67Late is Pi's `late(link, ms)`: link with every message it receives delivered ms late, in order (a slow consumer
// behind a slow network). It counts the `event` messages it holds back: the most at once is what the server left
// unacknowledged on this link (an ack leaves only after the delivery).
type ex67Late struct {
	link.Link
	delay time.Duration
	queue chan ex67Held
	done  chan struct{}
	end   sync.Once

	mu         sync.Mutex
	held, most int
}

type ex67Held struct {
	message []byte
	due     time.Time
	event   bool
}

func newEx67Late(t testing.TB, inner link.Link, delay time.Duration) *ex67Late {
	late := &ex67Late{Link: inner, delay: delay, queue: make(chan ex67Held, 4*ex67Samples), done: make(chan struct{})}
	t.Cleanup(late.stop)
	return late
}

func (l *ex67Late) stop() { l.end.Do(func() { close(l.done) }) }

func (l *ex67Late) OnMessage(listener func(message []byte)) {
	l.Link.OnMessage(func(message []byte) {
		decoded, err := link.BinaryCodec.Decode(message)
		event := false
		if err == nil {
			if checked, checkErr := link.CheckMessage(decoded, background); checkErr == nil {
				event = checked.Kind() == "event"
			}
		}
		if event {
			l.mu.Lock()
			l.held++
			l.most = max(l.most, l.held)
			l.mu.Unlock()
		}
		select {
		case l.queue <- ex67Held{message: message, due: time.Now().Add(l.delay), event: event}:
		case <-l.done:
		}
	})
	go func() {
		for {
			select {
			case <-l.done:
				return
			case held := <-l.queue:
				select {
				case <-l.done:
					return
				case <-time.After(time.Until(held.due)):
				}
				listener(held.message)
				if held.event {
					l.mu.Lock()
					l.held--
					l.mu.Unlock()
				}
			}
		}
	}()
}

func (l *ex67Late) OnClose(listener func()) {
	l.Link.OnClose(func() {
		l.stop()
		listener()
	})
}

// counts is the event messages held back now and the most at once.
func (l *ex67Late) counts() (held, most int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held, l.most
}

// ex67Seen is what a client counted: the script's `seen` (`samples` is len(ts), `lastY` the last of ys).
type ex67Seen struct {
	mu  sync.Mutex
	ts  []float64
	ys  []float64
	lag int64
}

func (s *ex67Seen) samples() ([]float64, []float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]float64(nil), s.ts...), append([]float64(nil), s.ys...)
}

func TestExample67Events(t *testing.T) {
	// A paddle sample: `t` the sender's sample time, `y` the position.
	sampleSchema := exObj(exProp("t", contracts.Num()), exProp("y", contracts.Num()))
	// The server's clock: its tick count (state) and its paddle (an event member, the local marker `event(...)`).
	clock := exLocal("example.clock", contracts.Params{},
		contracts.Mem("tick", exInt()), contracts.Mem("paddle", contracts.Event(sampleSchema)))
	// What a client holds: the clock's tick and paddle, re-exposed (an event member forwarded unchanged).
	game := exRemote("example.game", contracts.Params{}, exServerAddress, contracts.RemoteDeclaration{
		State:  []contracts.RemoteStateEntry{contracts.StateEntry("tick", exInt())},
		Events: []contracts.RemoteEventEntry{contracts.EventEntry("paddle", sampleSchema)},
	})
	ticks := exDefineState("ticks", documents.StateOptions{
		Schema: exObj(exProp("n", exInt())), Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})

	// What the routine emitted, in order, and the end of its ticking.
	var emittedMu sync.Mutex
	var emittedT, emittedY []float64
	ticked := make(chan struct{})
	serverFacet := DefineFacet(func(host FacetHost, _ FacetSelf) error {
		if err := host.Serve(clock, func(_ *delta.JsonObject, self Self) (contracts.MembersImplementation, error) {
			counter := stateOf(self, ticks)
			paddle := self.Events()
			// The ticking: a routine emits a sample every 10 ms; every tenth also commits the tick (emit is no transaction
			// work: commit first, then emit).
			self.OnLive(func(ctx Ctx) *Handled {
				return Awaiting(exLoop(ctx), func() error {
					defer close(ticked)
					for index := 1; index <= ex67Samples; index++ {
						if err := Sleep(10*time.Millisecond, ctx); err != nil {
							return nil
						}
						if index%10 == 0 {
							// `draft.n++` from 0: the tenth sample's commit is tick 1.
							if err := counter.Change(func(draft *delta.Object) { _ = draft.Set("n", float64(index/10)) }, ctx); err != nil {
								return nil
							}
						}
						at := float64(time.Now().UnixMilli())
						y := math.Round(math.Sin(float64(index)/10)*100) / 100
						emittedMu.Lock()
						emittedT, emittedY = append(emittedT, at), append(emittedY, y)
						emittedMu.Unlock()
						if err := paddle.Emit(jsonObject("t", at, "y", y)); err != nil {
							return nil
						}
					}
					return nil
				})
			})
			return contracts.MembersImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"tick": self.Derive([]any{counter.s}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("n"), nil
					}),
				},
				Events: map[string]contracts.EventMember[delta.JsonValue]{"paddle": paddle},
			}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return host.Serve(game, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.MembersImplementation, error) {
			held := self.Get(clock, jsonObject())
			return contracts.MembersImplementation{
				State:  map[string]chord.Watchable[delta.JsonValue]{"tick": Expose(held.Member("tick"))},
				Events: map[string]contracts.EventMember[delta.JsonValue]{"paddle": held.Events("paddle")},
			}, nil
		}, ServeOptions{})
	})

	// 2. The server: at most 4 of its own event messages unacknowledged per link (`eventWindow`), its counters recorded.
	recorder := telemetry.NewInMemoryTelemetryContext()
	errs := &hostErrors{}
	server := newHost(t, HostOptions{
		Address: new(Server.Address(nil)), Principal: serverPrincipal(), EventWindow: new(4), Telemetry: recorder, OnError: errs.push,
	})
	mustLoad(t)(server.Load([]HostFacet{{Name: "@example/server", Facet: serverFacet}}, background))

	// A client connected to the server (its end delivering delay late when given), counting what it receives.
	client := func(delay time.Duration) (*Reference, *ex67Seen, *ex67Late) {
		host := newHost(t, HostOptions{OnError: errs.push})
		mustLoad(t)(host.Load(nil, background))
		a, b := link.MemoryLinkPair()
		must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: link.BinaryCodec}, background))
		var end link.Link = b
		var late *ex67Late
		if delay > 0 {
			late = newEx67Late(t, b, delay)
			end = late
		}
		must(t, host.Connect(end, LinkOptions{Principal: serverPrincipal(), Codec: link.BinaryCodec}, background))
		held := exGet(t, host, game, jsonObject())
		seen := &ex67Seen{}
		// 1. Subscribe: from now on every sample delivered to this reference calls the listener (`at`: the emit's time).
		on(t, held, "paddle", func(e contracts.EventDelivery[delta.JsonValue]) {
			value := e.Value.(*delta.JsonObject)
			seen.mu.Lock()
			seen.ts = append(seen.ts, value.Value("t").(float64))
			seen.ys = append(seen.ys, value.Value("y").(float64))
			seen.lag = time.Now().UnixMilli() - e.At
			seen.mu.Unlock()
		}, background)
		return held, seen, late
	}

	// 3. Two clients: one fast, one whose link delivers 100 ms late.
	fastGame, fast, _ := client(0)
	slowGame, slow, late := client(100 * time.Millisecond)
	select {
	case <-ticked:
	case <-time.After(30 * time.Second):
		t.Fatal("the ticking never ended")
	}
	emittedMu.Lock()
	wantT, wantY := append([]float64(nil), emittedT...), append([]float64(nil), emittedY...)
	emittedMu.Unlock()
	if len(wantT) != ex67Samples {
		t.Fatalf("emitted %d samples, want %d", len(wantT), ex67Samples)
	}
	tick := func(ref *Reference) delta.JsonValue { return exValue(t, ref, "tick") }
	// What is on its way arrives: the fast client's last sample, the slow link's held messages, both ticks.
	settled := func() bool {
		ts, _ := fast.samples()
		held, _ := late.counts()
		return len(ts) > 0 && ts[len(ts)-1] == wantT[len(wantT)-1] && held == 0 && tick(fastGame) == 10.0 && tick(slowGame) == 10.0
	}
	for deadline := time.Now().Add(30 * time.Second); !settled(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("never settled: fast tick %v, slow tick %v", tick(fastGame), tick(slowGame))
		}
	}
	flushed(server)

	// 4. The fast client got every sample from its subscription on (about one per 10 ms): the last ones the server
	//    emitted, none missing, in order.
	fastT, fastY := fast.samples()
	if len(fastT) == 0 || len(fastT) > ex67Samples {
		t.Fatalf("fast: %d samples, want up to %d", len(fastT), ex67Samples)
	}
	from := ex67Samples - len(fastT)
	for index := range fastT {
		if fastT[index] != wantT[from+index] || fastY[index] != wantY[from+index] {
			t.Fatalf("fast: sample %d = {t: %v, y: %v}, want the server's sample %d {t: %v, y: %v}: one is missing",
				index, fastT[index], fastY[index], from+index, wantT[from+index], wantY[from+index])
		}
	}
	//    The slow one got far fewer (dropped at the server, never queued): its link never held more than the window's 4
	//    event messages, and it filled the window.
	slowT, _ := slow.samples()
	if _, most := late.counts(); most != 4 {
		t.Fatalf("slow: at most %d event messages on their way at once, want the event window's 4", most)
	}
	if len(slowT) >= len(fastT) {
		t.Fatalf("slow: %d samples, fast: %d, want far fewer for the slow client", len(slowT), len(fastT))
	}
	//    Yet its state (the tick) is whole: both clients are at the tenth tick.
	exEqual(t, "fast tick", tick(fastGame), 10.0)
	exEqual(t, "slow tick", tick(slowGame), 10.0)

	// The server's counters: every emit counted and encoded once for both links; what the slow link's full window did
	// not take is counted dropped (reason window), never sent later.
	counts := map[string]float64{}
	for _, measurement := range recorder.GetMetrics() {
		key := measurement.Name
		if reason, ok := measurement.Attributes.Get("reason"); ok {
			key += " (" + fmt.Sprint(reason) + ")"
		}
		counts[key] += measurement.Value
	}
	emitted, sent, dropped := counts["chord.events.emitted"], counts["chord.events.sent"], counts["chord.events.dropped (window)"]
	if emitted != ex67Samples || counts["chord.events.encodes"] != ex67Samples {
		t.Fatalf("server: emitted %v, encodes %v, want %d each (%v)", emitted, counts["chord.events.encodes"], ex67Samples, counts)
	}
	if dropped < 1 || sent < ex67Samples || sent+dropped > 2*ex67Samples {
		t.Fatalf("server: sent %v, dropped (window) %v of %v emits to two links (%v)", sent, dropped, emitted, counts)
	}
	if float64(len(slowT)) > sent-ex67Samples {
		t.Fatalf("slow: %d samples, more than the %v the server sent it", len(slowT), sent-ex67Samples)
	}
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
