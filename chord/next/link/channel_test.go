package link_test

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/link/channel.test.ts at the channel's own level: the handshake, checks, the
// heartbeat, trusted links, and in-order handling while a message waits for an asynchronous inflate. The cases that
// run a host over the channel ("queues a consumer's messages until the peer's hello", the heartbeat cases that watch
// references go loading, "runs one timer per host", "takes its numbers from the host's options", and the codec
// mismatch between two hosts) are pending on lane D5; the channel-level equivalents here call `Tick` with a clock.

// recordingHandler is a ChannelHandler recording what happens to it.
type recordingHandler struct {
	mu      sync.Mutex
	opened  int
	closed  int
	handled []string
	message func(link.Message) (*link.Handling, error)
}

func (handler *recordingHandler) Opened() {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.opened++
}

func (handler *recordingHandler) Closed() {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.closed++
}

func (handler *recordingHandler) Message(message link.Message) (*link.Handling, error) {
	if handler.message != nil {
		return handler.message(message)
	}
	handler.record(message.Kind())
	return nil, nil
}

func (handler *recordingHandler) record(name string) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	handler.handled = append(handler.handled, name)
}

func (handler *recordingHandler) names() []string {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return slices.Clone(handler.handled)
}

func (handler *recordingHandler) isClosed() bool {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.closed > 0
}

type reports struct {
	recorder[error]
}

func (r *reports) messages() []string {
	messages := []string{}
	for _, err := range r.all() {
		messages = append(messages, err.Error())
	}
	return messages
}

// clock is a settable time source for the heartbeat.
type clock struct {
	mu  sync.Mutex
	now int64
}

func (c *clock) read() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) set(now int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

type channelSetup struct {
	channel *link.Channel
	peer    *rawPeer
	handler *recordingHandler
	reports *reports
	clock   *clock
	ends    *link.MemoryLink
	peerEnd *link.MemoryLink
}

// openChannel is a channel over a memory link, with a raw peer at the other end; hello false leaves the peer silent.
func openChannel(t *testing.T, codec link.Codec, trusted bool, hello bool) *channelSetup {
	t.Helper()
	a, b := link.MemoryLinkPair()
	setup := &channelSetup{handler: &recordingHandler{}, reports: &reports{}, clock: &clock{}, ends: a, peerEnd: b}
	options := link.ChannelOptions{Codec: codec, Incarnation: incarnation, Calls: 256, Trusted: trusted, Report: setup.reports.add, Now: setup.clock.read}
	channel, err := link.NewChannel(a, setup.handler, options, background)
	requireNoError(t, err)
	channel.Start()
	setup.channel = channel
	setup.peer = newRawPeer(t, b, codec)
	if hello {
		setup.peer.hello()
	}
	transported(a, b)
	return setup
}

func TestTheHandshake(t *testing.T) {
	eachCodec(t, func(t *testing.T, codec link.Codec) {
		t.Run("sends hello with the protocol version and the host's incarnation first", func(t *testing.T) {
			setup := openChannel(t, codec, false, false)
			if kinds := setup.peer.kinds(); !slices.Equal(kinds, []string{"hello"}) {
				t.Fatalf("kinds = %v", kinds)
			}
			hello := setup.peer.received[0].(*link.HelloMessage)
			if hello.Protocol != data.ProtocolVersion || hello.Incarnation != incarnation || hello.Calls != 256 {
				t.Fatalf("hello = %+v", hello)
			}
		})

		t.Run("closes the link on traffic before hello", func(t *testing.T) {
			setup := openChannel(t, codec, false, false)
			setup.peer.send(&link.HoldMessage{Hold: 0, Type: "app.thing", Version: 1, Fingerprint: "0000000000000000", Params: link.Text("{}"), Address: server()})
			transported(setup.ends, setup.peerEnd)
			if !setup.peer.isClosed() || !slices.Equal(setup.reports.messages(), []string{"handshake: hold before hello"}) {
				t.Fatalf("closed = %v, reports = %v", setup.peer.isClosed(), setup.reports.messages())
			}
		})

		t.Run("closes the link on a second hello", func(t *testing.T) {
			setup := openChannel(t, codec, false, true)
			setup.peer.hello()
			transported(setup.ends, setup.peerEnd)
			if !setup.peer.isClosed() || !slices.Equal(setup.reports.messages(), []string{"handshake: a second hello"}) {
				t.Fatalf("closed = %v, reports = %v", setup.peer.isClosed(), setup.reports.messages())
			}
		})

		t.Run("closes the link on another protocol version", func(t *testing.T) {
			setup := openChannel(t, codec, false, false)
			setup.peer.send(&link.HelloMessage{Protocol: data.ProtocolVersion + 1, Incarnation: rawPeerIncarnation, Calls: 256})
			transported(setup.ends, setup.peerEnd)
			want := "handshake: protocol " + strconv.Itoa(data.ProtocolVersion+1) + ", this host speaks " + strconv.Itoa(data.ProtocolVersion)
			if !setup.peer.isClosed() || !slices.Equal(setup.reports.messages(), []string{want}) {
				t.Fatalf("closed = %v, reports = %v", setup.peer.isClosed(), setup.reports.messages())
			}
		})

		t.Run("closes the link on a message failing its checks", func(t *testing.T) {
			setup := openChannel(t, codec, false, true)
			setup.peer.sendBytes(encode(t, codec, &link.ErrorMessage{Call: 0, Error: object("kind", "nope")}))
			transported(setup.ends, setup.peerEnd)
			if !setup.peer.isClosed() || !slices.Equal(setup.reports.messages(), []string{"message: fails the message schema"}) {
				t.Fatalf("closed = %v, reports = %v", setup.peer.isClosed(), setup.reports.messages())
			}
		})

		t.Run("opens on the peer's hello, which carries its incarnation and call window", func(t *testing.T) {
			setup := openChannel(t, codec, false, true)
			if !setup.channel.Opened() || setup.channel.Peer() != rawPeerIncarnation || setup.channel.PeerCalls() != 256 || setup.handler.opened != 1 {
				t.Fatalf("opened = %v, peer = %q, calls = %d", setup.channel.Opened(), setup.channel.Peer(), setup.channel.PeerCalls())
			}
		})

		t.Run("queues what a side sends until the peer's hello", func(t *testing.T) {
			setup := openChannel(t, codec, false, false)
			requireNoError(t, setup.channel.Send(&link.ReleaseMessage{Hold: 1}))
			transported(setup.ends, setup.peerEnd)
			if kinds := setup.peer.kinds(); !slices.Equal(kinds, []string{"hello"}) {
				t.Fatalf("before the hello = %v", kinds)
			}
			setup.peer.hello()
			transported(setup.ends, setup.peerEnd)
			if kinds := setup.peer.kinds(); !slices.Equal(kinds, []string{"hello", "release"}) {
				t.Fatalf("after the hello = %v", kinds)
			}
		})
	})
}

func TestACodecMismatchOfChannels(t *testing.T) {
	a, b := link.MemoryLinkPair()
	first, second := &reports{}, &reports{}
	left, err := link.NewChannel(a, &recordingHandler{}, link.ChannelOptions{Codec: link.BinaryCodec, Incarnation: incarnation, Calls: 1, Report: first.add}, background)
	requireNoError(t, err)
	right, err := link.NewChannel(b, &recordingHandler{}, link.ChannelOptions{Codec: link.TextCodec, Incarnation: rawPeerIncarnation, Calls: 1, Report: second.add}, background)
	requireNoError(t, err)
	left.Start()
	right.Start()
	transported(a, b)
	if !slices.Equal(first.messages(), []string{"handshake: the first message is no binary message"}) {
		t.Fatalf("binary side = %v", first.messages())
	}
	if len(second.messages()) != 1 || second.messages()[0] != "handshake: the first message is no text message" {
		t.Fatalf("text side = %v", second.messages())
	}
}

func TestTheHeartbeat(t *testing.T) {
	eachCodec(t, func(t *testing.T, codec link.Codec) {
		t.Run("closes a link whose peer never sends hello after 30 s (race 30)", func(t *testing.T) {
			setup := openChannel(t, codec, false, false)
			setup.channel.Tick(29_000, 15_000, 30_000)
			transported(setup.ends, setup.peerEnd)
			if setup.peer.isClosed() {
				t.Fatal("closed at 29 s")
			}
			setup.channel.Tick(30_000, 15_000, 30_000)
			transported(setup.ends, setup.peerEnd)
			if !setup.peer.isClosed() || !setup.handler.isClosed() {
				t.Fatal("not closed at 30 s")
			}
		})

		t.Run("pings a silent open link after 15 s once, and closes it after 30 s without an answer", func(t *testing.T) {
			setup := openChannel(t, codec, false, true)
			setup.channel.Tick(13_500, 15_000, 30_000)
			transported(setup.ends, setup.peerEnd)
			if slices.Contains(setup.peer.kinds(), "ping") {
				t.Fatal("pinged at 13.5 s")
			}
			setup.channel.Tick(16_500, 15_000, 30_000)
			setup.channel.Tick(18_000, 15_000, 30_000)
			transported(setup.ends, setup.peerEnd)
			pings := 0
			for _, kind := range setup.peer.kinds() {
				if kind == "ping" {
					pings++
				}
			}
			if pings != 1 {
				t.Fatalf("pings = %d", pings)
			}
			setup.channel.Tick(31_500, 15_000, 30_000)
			transported(setup.ends, setup.peerEnd)
			if !setup.peer.isClosed() {
				t.Fatal("not closed after 30 s of silence")
			}
		})

		t.Run("sends no pings on a busy link", func(t *testing.T) {
			setup := openChannel(t, codec, false, true)
			for second := int64(0); second < 60; second += 5 {
				setup.clock.set(second * 1000)
				setup.peer.send(&link.ReleaseMessage{Hold: 99})
				transported(setup.ends, setup.peerEnd)
				setup.channel.Tick(second*1000+4_999, 15_000, 30_000)
			}
			if kinds := setup.peer.kinds(); !slices.Equal(kinds, []string{"hello"}) || setup.peer.isClosed() {
				t.Fatalf("kinds = %v, closed = %v", kinds, setup.peer.isClosed())
			}
		})

		t.Run("answers each ping with a pong at once", func(t *testing.T) {
			setup := openChannel(t, codec, false, true)
			setup.peer.send(&link.PingMessage{})
			transported(setup.ends, setup.peerEnd)
			if kinds := setup.peer.kinds(); !slices.Equal(kinds, []string{"hello", "pong"}) {
				t.Fatalf("kinds = %v", kinds)
			}
		})
	})
}

func TestTrustedLinks(t *testing.T) {
	run := func(trusted bool) (checks float64, closed bool) {
		meter := nexttest.Metered(t)
		a, b := link.MemoryLinkPair()
		reportsLog := &reports{}
		options := link.ChannelOptions{Codec: link.BinaryCodec, Incarnation: incarnation, Calls: 256, Trusted: trusted, Report: reportsLog.add}
		channel, err := link.NewChannel(a, &recordingHandler{}, options, meter.Context)
		requireNoError(t, err)
		channel.Start()
		peer := newRawPeer(t, b, link.BinaryCodec)
		peer.hello()
		peer.send(&link.ReleaseMessage{Hold: 1})
		peer.sendBytes([]byte{0xff})
		transported(a, b)
		return meter.Counts()["chord.link.checks"], peer.isClosed()
	}
	t.Run("skip the message schema check: no chord.link.checks, the codec still runs", func(t *testing.T) {
		if checks, closed := run(false); checks != 2 || !closed {
			t.Fatalf("untrusted: checks = %v, closed = %v", checks, closed)
		}
		if checks, closed := run(true); checks != 0 || !closed {
			t.Fatalf("trusted: checks = %v, closed = %v", checks, closed)
		}
	})
}

// holding is a channel whose handler handles `release` of hold 0 asynchronously (until finished), every other message
// at once.
type holding struct {
	setup    *channelSetup
	mu       sync.Mutex
	finishes []*link.Handling
}

func newHolding(t *testing.T, codec link.Codec) *holding {
	t.Helper()
	h := &holding{}
	a, b := link.MemoryLinkPair()
	setup := &channelSetup{handler: &recordingHandler{}, reports: &reports{}, ends: a, peerEnd: b}
	setup.handler.message = func(message link.Message) (*link.Handling, error) {
		name := message.Kind()
		release, isRelease := message.(*link.ReleaseMessage)
		if isRelease {
			name = "release " + strconv.Itoa(release.Hold)
		}
		if !isRelease || release.Hold != 0 {
			setup.handler.record(name)
			return nil, nil
		}
		handling := link.NewHandling()
		h.mu.Lock()
		h.finishes = append(h.finishes, handling)
		h.mu.Unlock()
		return handling.Then(func() { setup.handler.record(name) }), nil
	}
	options := link.ChannelOptions{Codec: codec, Incarnation: rawPeerIncarnation, Calls: 1, Trusted: true, Report: setup.reports.add}
	channel, err := link.NewChannel(a, setup.handler, options, background)
	requireNoError(t, err)
	channel.Start()
	setup.channel = channel
	setup.peer = newRawPeer(t, b, codec)
	setup.peer.hello()
	transported(a, b)
	h.setup = setup
	return h
}

// finish is the pending asynchronous handling the nth time a hold 0 release was held.
func (h *holding) finish(index int) *link.Handling {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.finishes[index]
}

func (h *holding) settle() { transported(h.setup.ends, h.setup.peerEnd) }

func TestInOrderHandling(t *testing.T) {
	eachCodec(t, func(t *testing.T, codec link.Codec) {
		t.Run("holds every later message, a ping included, until the asynchronous one was handled, then handles them in order", func(t *testing.T) {
			h := newHolding(t, codec)
			peer := h.setup.peer
			peer.send(&link.ReleaseMessage{Hold: 0})
			peer.send(&link.ReleaseMessage{Hold: 1})
			peer.send(&link.PingMessage{})
			peer.send(&link.ReleaseMessage{Hold: 2})
			h.settle()
			if len(h.setup.handler.names()) != 0 || !slices.Equal(peer.kinds(), []string{"hello"}) {
				t.Fatalf("handled = %v, peer = %v", h.setup.handler.names(), peer.kinds())
			}
			h.finish(0).Resolve()
			h.settle()
			if want := []string{"release 0", "release 1", "release 2"}; !slices.Equal(h.setup.handler.names(), want) {
				t.Fatalf("handled = %v", h.setup.handler.names())
			}
			if !slices.Equal(peer.kinds(), []string{"hello", "pong"}) {
				t.Fatalf("peer = %v", peer.kinds())
			}
		})

		t.Run("keeps the rest behind a held message that holds again, in order, a long queue included", func(t *testing.T) {
			h := newHolding(t, codec)
			peer := h.setup.peer
			release := func(hold int) []byte { return encode(t, codec, &link.ReleaseMessage{Hold: hold}) }
			peer.sendBytes(release(0))
			peer.sendBytes(release(1))
			peer.sendBytes(release(0))
			many := release(2)
			for range 70_000 {
				peer.sendBytes(many)
			}
			h.settle()
			if len(h.setup.handler.names()) != 0 {
				t.Fatalf("handled = %d", len(h.setup.handler.names()))
			}
			h.finish(0).Resolve()
			h.settle()
			if want := []string{"release 0", "release 1"}; !slices.Equal(h.setup.handler.names(), want) {
				t.Fatalf("handled = %v", h.setup.handler.names())
			}
			h.finish(1).Resolve()
			h.settle()
			names := h.setup.handler.names()
			if len(names) != 70_003 || !slices.Equal(names[:4], []string{"release 0", "release 1", "release 0", "release 2"}) {
				t.Fatalf("handled = %d, first = %v", len(names), names[:min(4, len(names))])
			}
		})

		t.Run("drops the held message's successors when the link closes meanwhile (race 13)", func(t *testing.T) {
			h := newHolding(t, codec)
			peer := h.setup.peer
			peer.send(&link.ReleaseMessage{Hold: 0})
			peer.send(&link.ReleaseMessage{Hold: 1})
			h.settle()
			peer.link.Close()
			h.settle()
			if !h.setup.handler.isClosed() {
				t.Fatal("the handler was not told of the close")
			}
			h.finish(0).Resolve()
			h.settle()
			if want := []string{"release 0"}; !slices.Equal(h.setup.handler.names(), want) {
				t.Fatalf("handled = %v", h.setup.handler.names())
			}
		})

		t.Run("keeps no held message once the link closed (GC)", func(t *testing.T) {
			h := newHolding(t, codec)
			ref := func() weak.Pointer[byte] {
				h.setup.peer.send(&link.ReleaseMessage{Hold: 0})
				held := encode(t, codec, &link.ReleaseMessage{Hold: 1})
				h.setup.peer.sendBytes(held)
				h.settle()
				h.setup.peer.link.Close()
				h.settle()
				return weak.Make(&held[0])
			}()
			nexttest.ExpectCollected(t, ref)
			h.finish(0).Resolve()
			if !h.setup.peer.isClosed() {
				t.Fatal("the peer's end is open")
			}
		})

		t.Run("treats a rejected asynchronous handling as a protocol error: reported, the link closes", func(t *testing.T) {
			h := newHolding(t, codec)
			peer := h.setup.peer
			peer.send(&link.ReleaseMessage{Hold: 0})
			peer.send(&link.ReleaseMessage{Hold: 1})
			h.settle()
			corrupt := errors.New("corrupt")
			h.finish(0).Reject(corrupt)
			h.settle()
			if !peer.isClosed() || len(h.setup.reports.all()) != 1 || !errors.Is(h.setup.reports.all()[0], corrupt) || len(h.setup.handler.names()) != 0 {
				t.Fatalf("closed = %v, reports = %v, handled = %v", peer.isClosed(), h.setup.reports.all(), h.setup.handler.names())
			}
		})
	})
}
