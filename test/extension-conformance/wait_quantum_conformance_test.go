package extensionconformance

import (
	"bufio"
	"context"
	"net"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// ioWaitPeer is the TCP peer of the io_wait commands of the SDK fixtures: a command or handler of theirs dials it, announces itself with a "waiting" line and returns when the peer writes a line back.
type ioWaitPeer struct {
	t  *testing.T
	ln net.Listener
}

func newIOWaitPeer(t *testing.T) *ioWaitPeer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return &ioWaitPeer{t: t, ln: ln}
}

func (p *ioWaitPeer) addr() string { return p.ln.Addr().String() }

// accept returns the connection of the next waiting handler once it announced itself, so the handler is known to be inside its wait.
func (p *ioWaitPeer) accept() net.Conn {
	p.t.Helper()
	deadline := time.Now().Add(testbudget.Wait(p.t))
	_ = p.ln.(*net.TCPListener).SetDeadline(deadline)
	conn, err := p.ln.Accept()
	if err != nil {
		p.t.Fatalf("no handler dialed the peer: %v", err)
	}
	p.t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(deadline)
	if line, err := bufio.NewReader(conn).ReadString('\n'); err != nil || line != "waiting\n" {
		p.t.Fatalf("handler announced %q, %v; want \"waiting\"", line, err)
	}
	return conn
}

// TestConformance_WaitingHandlerIsSuspended pins what Pi's event loop does while an extension awaits I/O, in every SDK that has no event loop of its own and in the isolated, packed and fused realizations. Pi reads stdin, and so answers a command, while a command handler or an agent_settled handler awaits a timer or I/O (rpc-mode.ts:808-810; agent-session.ts:1094-1105 awaits the handler). The Go, Rust, Python and Lisp runtimes block in the language's own call, which only its age tells from computation, so the host takes the handler to be waiting once it outlives HandlerWaitQuantum (D86). The command and the settle-tail handler here wait on a plain TCP read that no SDK call mediates; each is suspended while it waits, and neither is cancelled or answered early.
func TestConformance_WaitingHandlerIsSuspended(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	cases := append(sdkHarnessCases(), packedSDKHarnessCases()...)
	for _, tc := range cases {
		if tc.name == "subprocess-node" || tc.name == "subprocess-node-packed" {
			// A Node runtime reports the end of its event-loop window in RPC mode only; its rows are TestRPCCommandDuringSettleHandlerNetworkWaitAnswersAcrossSDKs and TestRPCCommandDuringCommandNetworkWaitAnswersAcrossSDKs, which compare it with Pi.
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				h.host.Shutdown("test done")
			})
			budget := testbudget.Wait(t)
			peer := newIOWaitPeer(t)

			t.Run("command", func(t *testing.T) {
				cmd, ok := findCommand(h.runner, "io_wait")
				if !ok {
					t.Fatal("io_wait command not registered")
				}
				acknowledged := make(chan struct{})
				ctx := invocation.WithSuspensionAcknowledgment(t.Context(), func() { close(acknowledged) })
				done := make(chan error, 1)
				go func() { done <- cmd.Handler(ctx, peer.addr()) }()
				conn := peer.accept()
				select {
				case <-acknowledged:
				case err := <-done:
					t.Fatalf("the command returned before the peer answered: %v", err)
				case <-time.After(budget):
					t.Fatal("a command waiting on a socket read was never acknowledged as suspended")
				}
				select {
				case err := <-done:
					t.Fatalf("the acknowledgment ended the command: %v", err)
				default:
				}
				if _, err := conn.Write([]byte("go\n")); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(budget):
					t.Fatal("the command did not return after the peer answered")
				}
			})

			t.Run("agent_settled", func(t *testing.T) {
				runConformanceCommandArgs(t, h, "io_wait_settled", peer.addr())
				waiting := make(chan int, 16)
				h.host.SetSettleTailHandler(func(n int) { waiting <- n })
				done := make(chan error, 1)
				go func() {
					_, err := h.runner.Emit(context.Background(), extension.AgentSettledEvent{Type: "agent_settled"})
					done <- err
				}()
				conn := peer.accept()
				deadline := time.After(budget)
				for got := 0; got != 1; {
					select {
					case got = <-waiting:
					case err := <-done:
						t.Fatalf("agent_settled returned before the peer answered: %v", err)
					case <-deadline:
						t.Fatal("an agent_settled handler waiting on a socket read was never counted as waiting")
					}
				}
				select {
				case err := <-done:
					t.Fatalf("counting the handler as waiting ended it: %v", err)
				default:
				}
				if _, err := conn.Write([]byte("go\n")); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(budget):
					t.Fatal("agent_settled did not return after the peer answered")
				}
				for got := 1; got != 0; {
					select {
					case got = <-waiting:
					case <-time.After(budget):
						t.Fatal("the answered handler still counts as waiting")
					}
				}
			})
		})
	}
}
