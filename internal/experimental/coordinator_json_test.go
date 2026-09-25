package experimental

import (
	"fmt"
	"io"
	"testing"
	"time"
)

// JSON.parse followed by JSON.stringify rounds numbers to binary64, orders integer keys,
// keeps the last duplicate value, and retains lone UTF-16 surrogates as escapes.
func TestCoordinatorPayloadJSONSemantics(t *testing.T) {
	for _, oracle := range []bool{true, false} {
		t.Run(fmt.Sprintf("upstream=%t", oracle), func(t *testing.T) {
			_, control, _ := startTestCoordinator(t, oracle)
			a, b := controlDial(t, control), controlDial(t, control)
			for _, peer := range []struct {
				id string
				s  *controlTestSocket
			}{{"a", a}, {"b", b}} {
				peer.s.send(t, map[string]any{"type": "register_peer", "protocol": CoordinatorProtocolVersion, "peerId": peer.id})
				peer.s.want(t, fmt.Sprintf(`{"type":"peer_registered","peerId":%q}`, peer.id))
			}
			_, err := io.WriteString(a, `{"type":"send","to":"b","payload":{"z":9007199254740993,"negativeZero":-0,"inf":1e400,"escaped":"\u0061","surrogate":"\ud800","pair":"\ud83d\ude00","2":"two","1":"one","dup":1,"dup":2}}`+"\n")
			if err != nil {
				t.Fatal(err)
			}
			if err := b.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			line, err := b.reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			want := "{\"type\":\"message\",\"from\":\"a\",\"payload\":{\"1\":\"one\",\"2\":\"two\",\"z\":9007199254740992,\"negativeZero\":0,\"inf\":null,\"escaped\":\"a\",\"surrogate\":\"\\ud800\",\"pair\":\"😀\",\"dup\":2}}\n"
			if line != want {
				t.Fatalf("got %s want %s", line, want)
			}
		})
	}
}
