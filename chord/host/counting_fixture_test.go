package host

// Ports packages/chord/test/fixtures/counting.ts (Pi b45e0ebc): a link wrapper that counts what crosses it, per
// direction: frames by type and bytes (JSON text length). It can also hold back the acknowledgements its own side sends
// (paused), to fill the peer's flow-control window.

import (
	"slices"
	"sync"
)

type countingLink struct {
	inner Link
	// sendMu keeps frames in the order they were decided: Send and release hold it through the inner link's Send.
	sendMu sync.Mutex
	mu     sync.Mutex
	// sent are the frames this side sent, in order; received those it received.
	sent, received           []Frame
	sentBytes, receivedBytes int
	// paused holds back the acknowledgements (the peer's window fills up).
	paused bool
	held   Frame
}

func newCountingLink(inner Link) *countingLink { return &countingLink{inner: inner} }

func (link *countingLink) Closed() bool { return link.inner.Closed() }

func (link *countingLink) Send(frame Frame) (int, error) {
	link.sendMu.Lock()
	defer link.sendMu.Unlock()
	link.mu.Lock()
	if _, isAck := frame.(AckFrame); isAck && link.paused {
		link.held = frame
		link.mu.Unlock()
		return 0, nil
	}
	link.sent = append(link.sent, frame)
	link.sentBytes += frameLength(frame)
	link.mu.Unlock()
	return link.inner.Send(frame)
}

// release lets the held acknowledgement through and stops holding them. The held acknowledgement is older than any frame
// sent after the release, and a peer takes acknowledgements as cumulative counts (a smaller one after a larger one is a
// protocol violation that closes the link), so it goes out before them: Send and release do not interleave.
func (link *countingLink) release() {
	link.sendMu.Lock()
	defer link.sendMu.Unlock()
	link.mu.Lock()
	link.paused = false
	held := link.held
	link.held = nil
	link.mu.Unlock()
	if held != nil {
		_, _ = link.inner.Send(held)
	}
}

func (link *countingLink) pause() {
	link.mu.Lock()
	link.paused = true
	link.mu.Unlock()
}

func (link *countingLink) Receive(handler func(frame Frame)) {
	link.inner.Receive(func(frame Frame) {
		link.mu.Lock()
		link.received = append(link.received, frame)
		link.receivedBytes += frameLength(frame)
		link.mu.Unlock()
		handler(frame)
	})
}

func (link *countingLink) OnClose(handler func(reason string)) { link.inner.OnClose(handler) }
func (link *countingLink) Close(reason ...string)              { link.inner.Close(reason...) }

// frameLength is JSON.stringify(frame).length.
func frameLength(frame Frame) int {
	data, err := EncodeFrame(frame)
	if err != nil {
		return 0
	}
	return utf16Length(data)
}

// sentFrames and receivedFrames are copies of what crossed so far.
func (link *countingLink) sentFrames() []Frame {
	link.mu.Lock()
	defer link.mu.Unlock()
	return slices.Clone(link.sent)
}

func (link *countingLink) receivedFrames() []Frame {
	link.mu.Lock()
	defer link.mu.Unlock()
	return slices.Clone(link.received)
}

// countSent is the frames of type t this side sent.
func (link *countingLink) countSent(t string) int { return countType(link.sentFrames(), t) }

func countType(frames []Frame, t string) int {
	return len(slices.DeleteFunc(slices.Clone(frames), func(frame Frame) bool { return frame.T() != t }))
}

// traffic is what this side sent or received, except pings, pongs and acknowledgements.
func traffic(frames []Frame) []Frame {
	return slices.DeleteFunc(slices.Clone(frames), func(frame Frame) bool {
		return frame.T() == "ping" || frame.T() == "pong" || frame.T() == "ack"
	})
}
