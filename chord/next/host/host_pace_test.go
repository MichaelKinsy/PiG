package host

import (
	"encoding/binary"
	"io"
	"net"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Pi reads a frame as one macrotask: the microtasks it queued (an ending's chain, host/object.ts:1625-1633) run to their next
// wait for something outside before the next frame is read. Loop.pace is that checkpoint on a Go link's reader goroutine
// (host_loop_seam.go); the cases here pin it from outside: what a peer that holds and releases faster than a teardown runs
// can leave pending.

// arrivalLink is the host's end of a link that runs arrived on the goroutine that reads it before each message reaches the host.
type arrivalLink struct {
	link.Link
	arrived func(message []byte)
	// sending, when set, runs before each message this end sends (a transport that can block).
	sending func(message []byte)
}

func (l *arrivalLink) Send(message []byte) {
	if l.sending != nil {
		l.sending(message)
	}
	l.Link.Send(message)
}

func (l *arrivalLink) OnMessage(listener func(message []byte)) {
	l.Link.OnMessage(func(message []byte) {
		l.arrived(message)
		listener(message)
	})
}

// pacedLinked is a host serving what setup serves, with a raw peer on a memory link (an off-loop link: its reader is a
// goroutine of the link's queue, the shape of a websocket's or an IPC port's reader) whose host end is wrapped by arrived.
func pacedLinked(t *testing.T, setup func(FacetHost) error, end *arrivalLink) (*chordHost, *RawPeer) {
	t.Helper()
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal()})
	mustLoad(t)(server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return setup(host)
	})}}, background))
	a, b := link.MemoryLinkPair()
	end.Link = a
	must(t, server.Connect(end, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	return server.(*chordHost), newRawPeer(t, b, rawPeerOptions{})
}

// pacing reads the loop's paced-ending counts: running paced tasks, pending endings, the most pending at once, readers waiting.
func pacing(loop *Loop) (running, pending, peak, waiting int) {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return loop.pacing.running, loop.pacing.pending, loop.pacing.peak, loop.pacing.waiting
}

// pacerHolds is how many paced tasks the pacer keeps a record of: none once every paced task ended.
func pacerHolds(loop *Loop) int {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	return len(loop.pacing.tasks)
}

// discard drops what the peer received and nobody took, except a pong: a sampler that clears the peer's inbox while a case
// waits for its pong (syncWithin) must not lose it.
func (p *RawPeer) discard() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.received = slices.DeleteFunc(p.received, func(message link.Message) bool { return message.Kind() != "pong" })
}

// syncWithin is rhSync with a deadline of its own: the peer's ping must come back as a pong within hangGuard (a reader that
// waits for a task that never ends, a pong lost, is a failure that names the pacing counts, not a suite timeout).
func syncWithin(t testing.TB, peer *RawPeer, loop *Loop) { syncWhileReading(t, peer, loop, nil) }

// syncWhileReading is syncWithin for a peer that queued many frames before its ping: the pong comes after the reader read them
// all, which takes as long as the frames need (the race detector makes it 5x longer), so the deadline is on progress: frames,
// which counts the frames the reader took, must advance within each hangGuard, else the reader is not reading on. A nil frames
// is a plain deadline of hangGuard.
func syncWhileReading(t testing.TB, peer *RawPeer, loop *Loop, frames *atomic.Int64) {
	t.Helper()
	peer.Send(&link.PingMessage{})
	var late atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		seen := int64(-1)
		if frames != nil {
			seen = frames.Load()
		}
		for {
			select {
			case <-done:
				return
			case <-time.After(hangGuard):
			}
			if frames != nil {
				if now := frames.Load(); now != seen {
					seen = now
					continue
				}
			}
			late.Store(true)
			// The broadcast under the lock: a waiter between its check of late and its Wait cannot miss it.
			peer.mu.Lock()
			peer.cond.Broadcast()
			peer.mu.Unlock()
			return
		}
	}()
	peer.mu.Lock()
	defer peer.mu.Unlock()
	for {
		for index, message := range peer.received {
			if message.Kind() == "pong" {
				peer.received = append(peer.received[:index:index], peer.received[index+1:]...)
				return
			}
		}
		if peer.closed {
			t.Fatal("the link closed before the pong")
		}
		if late.Load() {
			running, pending, peak, waiting := pacing(loop)
			t.Fatalf("no pong within %v: the reader is not reading on (paced tasks running %d, endings pending %d, peak %d, readers waiting in pace %d)", hangGuard, running, pending, peak, waiting)
		}
		peer.cond.Wait()
	}
}

// pendingBound is K: the endings a peer can leave pending in unmarked churn of one identity over an off-loop link. A frame
// that releases the last hold of a Notes object ends it and, releasing its reference, the NoteStore object it held: two
// endings, the most one frame starts (the hold frames start none). The reader does not read the next frame before the
// checkpoint, which returns once both ended or parked: here none parks (no native, no held commit), so each ends before the
// next frame arrives and the pending count falls back to zero between frames. Pi over a real transport, one macrotask per
// frame, peaks at 2 (the probe's `peakPendingTeardowns`, N = 20,000 and 100,000).
const pendingBound = 2

func TestPaceBoundsTheEndingsAPeersUnmarkedChurnLeavesPending(t *testing.T) {
	for _, n := range []int{2000, 20000, 100000} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			spec := specOf(Notes)
			hold := func(origin int) *link.HoldMessage {
				return &link.HoldMessage{
					Origin: link.Some(origin), Hold: 0, Type: spec.Name, Version: spec.Version, Fingerprint: spec.Fingerprint,
					Params: mustJSONPart(t, jsonObject("session", "S")), Address: serverWireAddress,
				}
			}
			var frames atomic.Int64
			h, peer := pacedLinked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, &arrivalLink{arrived: func([]byte) { frames.Add(1) }})
			loop := h.loop().(*Loop)
			for origin := 1; origin <= n; origin++ {
				peer.Send(hold(origin))
				peer.Send(&link.ReleaseMessage{Origin: link.Some(origin), Hold: 0})
			}
			syncWhileReading(t, peer, loop, &frames)
			loop.quiesce()
			if _, pending, peak, _ := pacing(loop); peak > pendingBound || pending != 0 {
				t.Fatalf("a peer's %d unmarked holds and releases left up to %d endings pending at once (%d now), want at most %d: the reader read on while the endings of earlier frames still ran", n, peak, pending, pendingBound)
			}
			if tasks := pacerHolds(loop); tasks != 0 {
				t.Fatalf("the pacer keeps %d paced tasks after a churn of %d holds and releases, want none", tasks, n)
			}
		})
	}
}

// returnsWithin runs fn and fails the test, by its own deadline, when it does not return within hangGuard: a pace that waits
// for a wake-up that never comes is a failure of the case, not a hang of the suite.
func returnsWithin(t testing.TB, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	timer := time.NewTimer(hangGuard)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatalf("%s did not return within %v", what, hangGuard)
	}
}

// waitsInPace is true once a reader is blocked in pace: the case that makes it must have started its wait before it lets the
// task it waits for move on.
func waitsInPace(t testing.TB, loop *Loop, readers int) {
	t.Helper()
	until(t, "a reader waits in pace", func() bool {
		_, _, _, waiting := pacing(loop)
		return waiting == readers
	})
}

// pacedRunning starts a paced task that runs, counted, until hold is closed, then runs body: the task a reader of a link
// waits for.
func pacedRunning(loop *Loop, hold <-chan struct{}, body func()) {
	endingMade(loop)
	loop.pacedTask(body, func() {
		<-hold
		body()
	})
}

func TestPaceReturnsAtOnceWhenNoPacedTaskRuns(t *testing.T) {
	loop := NewLoop()
	returnsWithin(t, "pace with no paced task", func() { loop.pace(nil, nil) })
}

func TestPaceReturnsAtOnceInsideASection(t *testing.T) {
	loop := NewLoop()
	hold := make(chan struct{})
	pacedRunning(loop, hold, func() {})
	defer func() { close(hold); loop.quiesce() }()
	returnsWithin(t, "pace inside a section", func() { loop.Run(func() { loop.pace(nil, nil) }) })
}

// The last running paced task ends: the reader waiting in pace wakes.
func TestPaceWakesWhenTheLastRunningPacedTaskEnds(t *testing.T) {
	loop := NewLoop()
	hold := make(chan struct{})
	pacedRunning(loop, hold, func() {})
	returned := make(chan struct{})
	go func() { loop.pace(nil, nil); close(returned) }()
	waitsInPace(t, loop, 1)
	close(hold)
	returnsWithin(t, "the reader waiting for the last running paced task, which ended,", func() { <-returned })
}

// The last running paced task parks on a real wait: the reader waiting in pace wakes (no paced ending can run). The wake-up
// is under the lock that counts the park, so a park between the reader's look at the count and its wait is not lost; a pace
// that is not woken by a park hangs here into returnsWithin's deadline.
func TestPaceWakesWhenTheLastRunningPacedTaskParks(t *testing.T) {
	loop := NewLoop()
	hold, wait := make(chan struct{}), make(chan struct{})
	pacedRunning(loop, hold, func() { _, _ = chordctx.Receive(chordctx.Background(), wait) })
	returned := make(chan struct{})
	go func() { loop.pace(nil, nil); close(returned) }()
	waitsInPace(t, loop, 1)
	close(hold)
	returnsWithin(t, "the reader waiting for the last running paced task, which parked,", func() { <-returned })
	close(wait)
	loop.quiesce()
}

// A task that parks and whose wait ends runs again: the reader that comes then waits for it.
func TestPaceWaitsAgainForAParkedTaskThatResumed(t *testing.T) {
	loop := NewLoop()
	hold, wait, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	pacedRunning(loop, hold, func() {
		_, _ = chordctx.Receive(chordctx.Background(), wait)
		<-release
	})
	close(hold)
	until(t, "the task parks", func() bool { running, _, _, _ := pacing(loop); return running == 0 })
	returnsWithin(t, "pace while its only paced task is parked", func() { loop.pace(nil, nil) })
	close(wait)
	until(t, "the task runs again", func() bool { running, _, _, _ := pacing(loop); return running == 1 })
	returned := make(chan struct{})
	go func() { loop.pace(nil, nil); close(returned) }()
	waitsInPace(t, loop, 1)
	close(release)
	returnsWithin(t, "the reader waiting for the task that ran again", func() { <-returned })
}

func TestPaceReturnsWhenTheLinkOrTheHostCloses(t *testing.T) {
	for _, which := range []string{"link", "host"} {
		t.Run(which, func(t *testing.T) {
			loop := NewLoop()
			hold := make(chan struct{})
			pacedRunning(loop, hold, func() {})
			defer func() { close(hold); loop.quiesce() }()
			linkDone, hostDone := make(chan struct{}), make(chan struct{})
			returned := make(chan struct{})
			go func() { loop.pace(linkDone, hostDone); close(returned) }()
			waitsInPace(t, loop, 1)
			if which == "link" {
				close(linkDone)
			} else {
				close(hostDone)
			}
			returnsWithin(t, "the reader waiting for a running paced task when its "+which+" closed,", func() { <-returned })
			if _, _, _, waiting := pacing(loop); waiting != 0 {
				t.Fatalf("readers waiting in pace after it returned = %d", waiting)
			}
		})
	}
}

// The same stops through a real hostLink: the host's close aborts its routing context (the host's abort signal; Accept / Connect
// watch no context of theirs, so no other abort reaches a link), and the host closes a link of its own (a heartbeat that stays
// silent, a protocol error). Each ends the wait of a reader in pace at once, while the paced task it waits for still runs.
func TestPaceInTheLinkReaderReturnsAtOnceWhenTheHostOrTheLinkCloses(t *testing.T) {
	for _, which := range []string{"the host closes", "the host closes the link"} {
		t.Run(which, func(t *testing.T) {
			server, peer := pacedLinked(t, func(FacetHost) error { return nil }, &arrivalLink{arrived: func([]byte) {}})
			loop := server.loop().(*Loop)
			hold := make(chan struct{})
			pacedRunning(loop, hold, func() {})
			// A frame the host handles, whose checkpoint then waits for the running paced task.
			peer.Send(&link.ReleaseMessage{Origin: link.Some(1), Hold: 0})
			waitsInPace(t, loop, 1)
			closed := make(chan error, 1)
			if which == "the host closes" {
				go func() { closed <- server.Close(background) }()
			} else {
				server.enter(func() { server.links[0].close() })
				closed <- nil
			}
			until(t, "the reader returns from pace while the paced task still runs", func() bool {
				running, _, _, waiting := pacing(loop)
				return waiting == 0 && running == 1
			})
			close(hold)
			must(t, <-closed)
			loop.quiesce()
		})
	}
}

// C5: a teardown parked on a real wait that only a LATER frame of the same link ends. The checkpoint waits for endings that
// can run, not for one that waits for the peer: the reader goes on reading, the later frame arrives, the chain finishes. The
// endings behind the parked head pile up meanwhile, unbounded, as in Pi (object.ts:1625-1633: `await teardown.previous`
// behind an ending that awaits a native's close); the case asserts that they do and bounds nothing there.
func TestPaceDoesNotHoldTheReaderForATeardownParkedOnALaterFrame(t *testing.T) {
	const behind = 500
	gate := make(chan struct{})
	var open sync.Once
	letGo := func() { open.Do(func() { close(gate) }) }
	defer letGo()
	// The head's native closes once the peer's ping arrives: the last frame, after the churn that queues behind the head.
	h, peer := pacedLinked(t, func(h FacetHost) error { return serveGatedNotes(h, gate) }, &arrivalLink{arrived: func(bytes []byte) {
		if decoded, err := testCodec().Decode(bytes); err == nil && decoded.Kind() == "ping" {
			letGo()
		}
	}})
	loop := h.loop().(*Loop)
	spec := specOf(Notes)
	hold := func(origin int) *link.HoldMessage {
		return &link.HoldMessage{
			Origin: link.Some(origin), Hold: 0, Type: spec.Name, Version: spec.Version, Fingerprint: spec.Fingerprint,
			Params: mustJSONPart(t, jsonObject("session", "S")), Address: serverWireAddress,
		}
	}
	peer.Send(hold(1))
	peer.Next("bound")
	peer.Send(&link.ReleaseMessage{Origin: link.Some(1), Hold: 0})
	for origin := 2; origin < 2+behind; origin++ {
		peer.Send(hold(origin))
		peer.Send(&link.ReleaseMessage{Origin: link.Some(origin), Hold: 0})
	}
	// The ping is the last frame: its arrival opens the gate. A reader that waited for the parked head would never read it.
	syncWithin(t, peer, loop)
	loop.quiesce()
	running, pending, peak, _ := pacing(loop)
	if running != 0 || pending != 0 {
		t.Fatalf("once the later frame released the head: paced tasks running %d, endings pending %d, want 0 and 0", running, pending)
	}
	if peak <= behind {
		t.Fatalf("most endings pending behind the parked head = %d, want more than %d: the reader waited for it", peak, behind)
	}
	var chain int
	h.enter(func() { chain = len(h.endings) })
	if chain != 0 {
		t.Fatalf("the host still records %d endings once the chain finished", chain)
	}
}

// blockingPort is an IpcPort over one end of a net.Pipe, a transport with no buffer: Send writes under a lock and returns
// when the peer's reader read the frame, the shape of a process's socket whose kernel buffer is full (framedPort.Send,
// chord/nextnode/plugins/plugins_node.go: conn.Write under writeMu). Frames are a length and the message.
type blockingPort struct {
	conn    net.Conn
	writing sync.Mutex
	mu      sync.Mutex
	message []func(any)
	gone    []func()
	live    bool
}

func newBlockingPort(conn net.Conn) *blockingPort { return &blockingPort{conn: conn, live: true} }

func (p *blockingPort) Send(message []byte, callback func(err error)) (bool, error) {
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(message)))
	frame = append(frame, message...)
	p.writing.Lock()
	_, err := p.conn.Write(frame)
	p.writing.Unlock()
	callback(err)
	return err == nil, nil
}

func (p *blockingPort) OnMessage(listener func(any)) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.message = append(p.message, listener)
	return func() {}
}

func (p *blockingPort) OnDisconnect(listener func()) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gone = append(p.gone, listener)
	return func() {}
}

func (p *blockingPort) Disconnect() { p.end() }

func (p *blockingPort) Connected() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.live
}

func (p *blockingPort) end() {
	p.mu.Lock()
	was := p.live
	p.live = false
	gone := p.gone
	p.mu.Unlock()
	_ = p.conn.Close()
	if was {
		for _, each := range gone {
			each()
		}
	}
}

// read delivers each frame to the listeners, on the goroutine that reads: as framedPort.read does.
func (p *blockingPort) read() {
	for {
		var size [4]byte
		if _, err := io.ReadFull(p.conn, size[:]); err != nil {
			p.end()
			return
		}
		message := make([]byte, binary.BigEndian.Uint32(size[:]))
		if _, err := io.ReadFull(p.conn, message); err != nil {
			p.end()
			return
		}
		p.mu.Lock()
		listeners := slices.Clone(p.message)
		p.mu.Unlock()
		for _, each := range listeners {
			each(message)
		}
	}
}

// Two hosts that churn to each other over a transport whose sends block (no buffer), each reader paced on its host's ending,
// which is blocked in a send to the peer: each ending waits for the other host's reader, which waits for that host's
// ending. A paced ending inside a send counts as parked (Loop.sending, hostLink.send), so the reader reads on and both
// finish; without the mark each reader waits in pace for a send only the other's reader can end: the case ends by its own
// deadline. The endings send outside a section here: a send inside one, blocked, holds the host's loop, and a reader waits for
// the loop to handle its frame, which is a stall of the blocking transport with or without pace.
func TestPaceTwoHostsChurningToEachOtherOverABlockingTransportBothFinish(t *testing.T) {
	const sends = 200
	connA, connB := net.Pipe()
	portA, portB := newBlockingPort(connA), newBlockingPort(connB)
	// Before the hosts exist, so a failed case unblocks the writes first (the hosts' close, a cleanup, waits for the endings).
	defer func() { _ = connA.Close(); _ = connB.Close() }()
	linkA, linkB := link.IpcLink(portA), link.IpcLink(portB)
	go portA.read()
	go portB.read()
	newSide := func(options HostOptions) *chordHost {
		host := newHost(t, options).(*chordHost)
		mustLoad(t)(host.Load(nil, background))
		return host
	}
	a := newSide(HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal()})
	b := newSide(HostOptions{})
	must(t, a.Accept(linkA, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	must(t, b.Connect(linkB, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	var finished sync.WaitGroup
	for _, side := range []*chordHost{a, b} {
		var end *hostLink
		side.enter(func() { end = side.links[0] })
		finished.Add(1)
		side.enter(func() {
			awaitEnding(side, func() (struct{}, error) {
				defer finished.Done()
				for range sends {
					end.send(&link.AckMessage{})
				}
				return struct{}{}, nil
			})
		})
	}
	done := make(chan struct{})
	go func() { finished.Wait(); close(done) }()
	timer := time.NewTimer(hangGuard)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatalf("the endings of two hosts that send to each other did not finish within %v: each reader waits in pace for an ending blocked in a send only the other reader can end", hangGuard)
	}
}
