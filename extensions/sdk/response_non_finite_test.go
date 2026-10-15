package sdk

import (
	"encoding/binary"
	"io"
	"math"
	"net"
	"strings"
	"testing"
	"time"
)

// A handler result that is not a ToolResult (a map from an event or command handler) is encoded by respond itself. JSON.stringify writes NaN and the infinities as null, so the response carries null, not an
// encoding error that fails the request.
func TestRespondWritesNonFiniteNumbersAsNull(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = host.Close(); _ = peer.Close() }()
	c := &conn{nc: host}
	errc := make(chan error, 1)
	go func() {
		errc <- c.respond("r1", map[string]any{"n": math.NaN(), "l": []any{math.Inf(1), 2}}, nil)
	}()
	var frames []string
	for range 2 {
		var header [4]byte
		if _, err := io.ReadFull(peer, header[:]); err != nil {
			t.Fatal(err)
		}
		body := make([]byte, binary.BigEndian.Uint32(header[:]))
		if _, err := io.ReadFull(peer, body); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, string(body))
	}
	if err := <-errc; err != nil {
		t.Fatalf("respond with NaN: %v", err)
	}
	if !strings.Contains(frames[1], `"response":{"result":{"l":[null,2],"n":null}}`) {
		t.Fatalf("response frame = %s, want the non-finite numbers as null", frames[1])
	}
}

func frameAfter(t *testing.T, peer net.Conn) string {
	t.Helper()
	_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
	var header [4]byte
	if _, err := io.ReadFull(peer, header[:]); err != nil {
		t.Fatalf("no frame was written: %v", err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(peer, body); err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// A notification's and a host call's arguments are the extension's values (an overlay size, a widget option, an appended entry): JSON.stringify writes their non-finite numbers as null.
func TestNotifyAndCallArgumentsWriteNonFiniteNumbersAsNull(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = host.Close(); _ = peer.Close() }()
	c := newConn(host)
	errc := make(chan error, 2)
	go func() { errc <- c.notify("x", map[string]any{"n": math.NaN()}) }()
	if frame := frameAfter(t, peer); !strings.Contains(frame, `"args":{"n":null}`) {
		t.Fatalf("notify frame = %s, want the NaN as null", frame)
	}
	if err := <-errc; err != nil {
		t.Fatalf("notify with NaN: %v", err)
	}
	go func() {
		_, err := c.beginCallFor("", "y", map[string]any{"m": math.Inf(-1)})
		errc <- err
	}()
	if frame := frameAfter(t, peer); !strings.Contains(frame, `"args":{"m":null}`) {
		t.Fatalf("call frame = %s, want the infinity as null", frame)
	}
	if err := <-errc; err != nil {
		t.Fatalf("call with an infinity: %v", err)
	}
}
