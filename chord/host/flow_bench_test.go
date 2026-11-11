package host

import (
	"fmt"
	"math"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// silentLink accepts every frame and reports no size: a Connection on it has nowhere to send frames.
type silentLink struct{}

func (silentLink) Closed() bool            { return false }
func (silentLink) Send(Frame) (int, error) { return 0, nil }
func (silentLink) Receive(func(Frame))     {}
func (silentLink) OnClose(func(string))    {}
func (silentLink) Close(...string)         {}

// waitingConnection is a Connection whose peer acknowledges frames, for the flow-control slot map alone.
func waitingConnection(window int) *Connection {
	return &Connection{
		link: silentLink{}, pending: newOrderedMap[string, *slot](), window: window, pendingCap: saturatingMul(window, pendingWindows),
		peerAcks: acksYes, maxCalls: math.MaxInt,
	}
}

// fillWaiting leaves the window full and one slot waiting for each of n holds.
func fillWaiting(connection *Connection, n int) {
	connection.unackedBytes = connection.window
	for hold := range n {
		connection.sendState(StateFrame{Hold: hold, M: "m", As: "ops", Ops: []delta.WireOp{{"s", []any{"p"}, 1.0}}}, overflow{resend: func() {}})
	}
}

// One acknowledgement releases n waiting slots, sent in the order they were created (Pi's #onAck over its #pending Map).
func BenchmarkFlowFlushWaitingSlots(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				connection := waitingConnection(1 << 20)
				fillWaiting(connection, n)
				connection.unackedBytes = 0
				b.StartTimer()
				connection.flush()
				if connection.pending.Len() != 0 {
					b.Fatal("slots still waiting")
				}
			}
		})
	}
}

// A hold ends while n slots wait (Pi's dropHoldState).
func BenchmarkFlowDropHoldState(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				connection := waitingConnection(1 << 20)
				fillWaiting(connection, n)
				b.StartTimer()
				connection.dropHoldState(n / 2)
			}
		})
	}
}

// One more ops frame arrives while n slots wait at the pending cap: the oldest are replaced by resets (makeRoom).
func BenchmarkFlowOverPendingCap(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				connection := waitingConnection(1 << 20)
				fillWaiting(connection, n)
				connection.pendingCap = connection.pendingBytes
				b.StartTimer()
				connection.sendState(StateFrame{Hold: 0, M: "m", As: "ops", Ops: []delta.WireOp{{"s", []any{"p"}, 2.0}}}, overflow{resend: func() {}})
			}
		})
	}
}

// One more ops frame arrives for a slot that waits among n others, under the cap.
func BenchmarkFlowSendStateWaiting(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			connection := waitingConnection(math.MaxInt / 8)
			fillWaiting(connection, n)
			waiting, _ := connection.pending.Get(stateKey(0, "m"))
			b.ReportAllocs()
			for b.Loop() {
				connection.pendingBytes, waiting.ops, waiting.size = 0, nil, 0
				connection.sendState(StateFrame{Hold: 0, M: "m", As: "ops", Ops: []delta.WireOp{{"s", []any{"p"}, 2.0}}}, overflow{resend: func() {}})
			}
		})
	}
}
