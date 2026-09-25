package experimental

import (
	"bufio"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkCoordinatorRoundTrip(b *testing.B) {
	dir := b.TempDir()
	public, control := filepath.Join(dir, "p"), filepath.Join(dir, "c")
	c, err := startCoordinator(public, control)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		c.shutdown()
		<-c.done
		if c.err != nil {
			b.Error(c.err)
		}
	})
	peer, err := net.Dial("unix", control)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = peer.Close() })
	if err := writeControlLine(peer, struct {
		Type     string `json:"type"`
		Protocol int    `json:"protocol"`
		PeerID   string `json:"peerId"`
	}{"register_peer", CoordinatorProtocolVersion, "worker"}); err != nil {
		b.Fatal(err)
	}
	reader := bufio.NewReader(peer)
	if _, err := reader.ReadString('\n'); err != nil {
		b.Fatal(err)
	}
	server := NewCoordinatorConnection(CoordinatorConnectionOptions{ControlPath: control, Endpoint: public + ".backend"})
	b.Cleanup(server.Close)
	received := make(chan struct{}, 1)
	server.OnEvent(NewCoordinatorConnectionListener(func(event CoordinatorConnectionEvent) {
		if event.Type == "message" {
			received <- struct{}{}
		}
	}))
	if err := server.Connect(b.Context()); err != nil {
		b.Fatal(err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		b.Fatal(err)
	}
	payload := strings.Repeat("x", 1024)
	response, err := EncodeControlLine(struct {
		Type    string `json:"type"`
		To      string `json:"to"`
		Payload string `json:"payload"`
	}{"send", "server", payload})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(2 * len(payload)))
	for b.Loop() {
		if err := server.Send("worker", payload); err != nil {
			b.Fatal(err)
		}
		if _, err := reader.ReadString('\n'); err != nil {
			b.Fatal(err)
		}
		if _, err := io.WriteString(peer, response); err != nil {
			b.Fatal(err)
		}
		<-received
	}
}
