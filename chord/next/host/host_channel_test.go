package host

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// pi: packages/chord/src/next/link/channel.ts

// Ports the cases of packages/chord/test/link/channel.test.ts that run a host over the channel (link/channel_test.go
// has the channel's own equivalents): "the handshake" (a consumer's messages queue until the peer's hello), "a codec
// mismatch", "the heartbeat" (a silent link pings after 15 s and closes after 30 s without an answer, one timer per
// host only while it has links, the numbers from the host's options), and "answers each ping with a pong at once; two sides
// pinging at once both answer".

// channelThing is the remote type `app.thing` of the handshake case.
var channelThing = mustContract(contracts.Remote("app.thing", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants: []contracts.VariantEntry{
		contracts.Var("only", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}}),
	},
}))

// channelElapse is Pi's `elapse(ms)`: fake time in heartbeat ticks, letting the links deliver between them.
func channelElapse(loop *scopeLoop, ms int64) {
	for step := int64(0); step < ms; step += 1500 {
		loop.AdvanceAsync(min(1500, ms-step))
		loop.barrier()
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTheHandshakeQueuesAConsumersMessagesUntilThePeersHelloAGetOverALinkThatNeverGreetsWaitsForItsDeadline(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		errs := &hostErrors{}
		client := newHost(t, HostOptions{OnError: errs.push})
		mustLoad(t)(client.Load(nil, background))
		a, b := link.MemoryLinkPair()
		must(t, client.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
		peer := newRawPeer(t, b, rawPeerOptions{NoHello: true})
		got := make(chan error, 1)
		go func() {
			_, err := client.Get(background, channelThing, jsonObject(), rvWithin(20*time.Millisecond))
			got <- err
		}()
		scopeEventually(t, "the client greeted first", func() bool { return reflect.DeepEqual(peer.Kinds(), []string{"hello"}) })
		err := <-got
		if err == nil || err.Error() != "app.thing: did not become live within 20 ms" {
			t.Fatalf("get = %v, want app.thing: did not become live within 20 ms", err)
		}
		peer.Send(&link.HelloMessage{Protocol: data.ProtocolVersion, Incarnation: rawPeerIncarnation, Calls: 256})
		scopeEventually(t, "the queued hold and release arrive after the hello", func() bool {
			return reflect.DeepEqual(peer.Kinds(), []string{"hello", "hold", "release"})
		})
	})
}

func TestACodecMismatchFailsTheFirstHelloAsAProtocolErrorNamingTheExpectedCodec(t *testing.T) {
	errs := &hostErrors{}
	server := newHost(t, HostOptions{OnError: errs.push})
	mustLoad(t)(server.Load(nil, background))
	client := newHost(t, HostOptions{})
	mustLoad(t)(client.Load(nil, background))
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: link.BinaryCodec}, background))
	must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: link.TextCodec}, background))
	scopeEventually(t, "the protocol error", func() bool {
		for _, reported := range errs.All() {
			if reported.Kind == ErrProtocolError {
				return true
			}
		}
		return false
	})
	var problems []string
	for _, reported := range errs.All() {
		if reported.Kind == ErrProtocolError {
			problems = append(problems, reported.Error.Error())
		}
	}
	if want := []string{"handshake: the first message is no binary message"}; !reflect.DeepEqual(problems, want) {
		t.Fatalf("protocol errors = %v, want %v", problems, want)
	}
}

func TestTheHeartbeatPingsASilentLinkAfter15sAndClosesItAfter30sWithoutAnAnswerReferencesGoLoading(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		loop := newScopeLoop().fakeIntervals()
		run := rawProvider(t, rawLinkedOptions{Loop: loop})
		notes := mustHold(t, run.Client, Notes, jsonObject("session", "S1"))
		bindNext(t, run.Peer)
		untilKind(t, notes, RefStateLive)
		channelElapse(loop, 13_500)
		for _, kind := range run.Peer.Kinds() {
			if kind == "ping" {
				t.Fatalf("kinds = %v, want no ping before 15 s", run.Peer.Kinds())
			}
		}
		channelElapse(loop, 3_000)
		pings := 0
		for _, kind := range run.Peer.Kinds() {
			if kind == "ping" {
				pings++
			}
		}
		if pings != 1 {
			t.Fatalf("pings = %d, want 1 (kinds %v)", pings, run.Peer.Kinds())
		}
		channelElapse(loop, 15_000)
		scopeEventually(t, "the silent link closed", run.Peer.Closed)
		if kind := refStateOf(notes).Kind; kind != RefStateLoading {
			t.Fatalf("state = %v, want loading", kind)
		}
	})
}

func TestTheHeartbeatAnswersEachPingWithAPongAtOnceTwoSidesPingingAtOnceBothAnswer(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		loop := newScopeLoop().fakeIntervals()
		run := linked(t, func(FacetHost) error { return nil }, linkedOptions{Loop: loop})
		channelElapse(loop, 60_000)
		// Both sides pinged at 15 s and were answered: neither closed the link.
		notes := mustHold(t, run.Client, Notes, jsonObject("session", "S1"))
		untilKind(t, notes, RefStateFailed)
		raw := rawLinked(t, func(FacetHost) error { return nil }, rawLinkedOptions{})
		raw.Peer.Send(&link.PingMessage{})
		scopeEventually(t, "the pong", func() bool { return slices.Contains(raw.Peer.Kinds(), "pong") })
	})
}

// awaitLinkOpened waits until host's first link has the peer's hello (link.Channel.Opened): its silence is counted from the
// clock's time then, so a test that moves the clock waits for it first (Pi's `rawLinked` awaits `transported()`).
func awaitLinkOpened(t testing.TB, host Host) {
	t.Helper()
	scopeEventually(t, "the peer's hello arrived", func() bool {
		opened := false
		h := host.(*chordHost)
		h.enter(func() { opened = len(h.links) > 0 && h.links[0].channel.Opened() })
		return opened
	})
}

func TestTheHeartbeatCountsNoPauseOfThisProcessAsSilenceASleepingLaptopPingsAtOnceClosesFifteenSecondsLaterUnanswered(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		loop := newScopeLoop().fakeIntervals()
		linked := rawLinked(t, func(FacetHost) error { return nil }, rawLinkedOptions{Loop: loop})
		awaitLinkOpened(t, linked.Server)
		channelElapse(loop, 3_000)
		// The process sleeps 10 minutes: no timer runs meanwhile, the clock jumps.
		loop.JumpClock(600_000)
		channelElapse(loop, 1_500)
		scopeEventually(t, "the ping at once", func() bool { return reflect.DeepEqual(linked.Peer.Kinds(), []string{"hello", "ping"}) })
		if linked.Peer.Closed() {
			t.Fatal("closed at the first tick after the pause")
		}
		channelElapse(loop, 13_500)
		if linked.Peer.Closed() {
			t.Fatal("closed 13.5 s after the pause")
		}
		channelElapse(loop, 1_500)
		scopeEventually(t, "the link closed 15 s after the pause", linked.Peer.Closed)
	})
}

func TestTheHeartbeatKeepsALinkThroughAPauseWhenItsPeerAnswersThePing(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		loop := newScopeLoop().fakeIntervals()
		linked := rawLinked(t, func(FacetHost) error { return nil }, rawLinkedOptions{Loop: loop})
		awaitLinkOpened(t, linked.Server)
		channelElapse(loop, 20_000)
		loop.JumpClock(600_000)
		channelElapse(loop, 1_500)
		linked.Peer.Send(&link.PongMessage{})
		channelElapse(loop, 25_000)
		if linked.Peer.Closed() {
			t.Fatal("closed although the peer answered")
		}
	})
}

func TestTheHeartbeatRunsOneTimerPerHostOnlyWhileItHasLinksNonePerMessage(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		loop := newScopeLoop().fakeIntervals()
		host := newHost(t, HostOptions{loop: loop})
		mustLoad(t)(host.Load(nil, background))
		if got := loop.TimerCount(); got != 0 {
			t.Fatalf("timers before any link = %d, want 0", got)
		}
		a, b := link.MemoryLinkPair()
		must(t, host.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		c, d := link.MemoryLinkPair()
		must(t, host.Accept(c, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		peers := []*RawPeer{newRawPeer(t, b, rawPeerOptions{}), newRawPeer(t, d, rawPeerOptions{})}
		for round := range 50 {
			peers[0].Send(&link.ReleaseMessage{Hold: round})
		}
		scopeEventually(t, "the host has its timer", func() bool { return loop.TimerCount() == 1 })
		time.Sleep(20 * time.Millisecond)
		if got := loop.TimerCount(); got != 1 {
			t.Fatalf("timers with two links and 50 messages = %d, want 1", got)
		}
		for _, peer := range peers {
			peer.Close()
		}
		scopeEventually(t, "the timer stops with the last link", func() bool { return loop.TimerCount() == 0 })
	})
}

func TestTheHeartbeatTakesItsNumbersFromTheHostsOptions(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		loop := newScopeLoop().fakeIntervals()
		ping, closeAfter := time.Second, 2*time.Second
		host := newHost(t, HostOptions{loop: loop, Heartbeat: &HeartbeatOptions{Ping: &ping, Close: &closeAfter}})
		mustLoad(t)(host.Load(nil, background))
		a, b := link.MemoryLinkPair()
		must(t, host.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		peer := newRawPeer(t, b, rawPeerOptions{})
		// The link is open once the host's hello reached the peer and the peer's was handled: the clock moves after.
		scopeEventually(t, "the hello", func() bool { return len(peer.Kinds()) == 1 })
		time.Sleep(20 * time.Millisecond)
		channelElapse(loop, 1_100)
		scopeEventually(t, "the ping", func() bool { return reflect.DeepEqual(peer.Kinds(), []string{"hello", "ping"}) })
		channelElapse(loop, 1_000)
		scopeEventually(t, "the close", peer.Closed)
	})
}
