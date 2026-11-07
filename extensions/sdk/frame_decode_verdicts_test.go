package sdk

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// A frame the read loop cannot decode ends the connection with no envelope (the host would otherwise wait for the answer to a request it
// carried); a frame it can decode is delivered whole, whatever the bytes inside its strings. The verdicts are those of the reader before the
// validity pass was made eight bytes at a time: truncated or corrupted JSON, a bad escape, a control character in a string, data after the
// document and a document that is not an envelope are refused, ill-formed UTF-8 and unpaired surrogate escapes are not.
func TestFrameDecodeVerdicts(t *testing.T) {
	for _, test := range []struct {
		name    string
		body    string
		refused bool
	}{
		{"valid", `{"type":"notify","notify":{"method":"x","args":{"a":"b"}}}`, false},
		{"invalid UTF-8 in a string", "{\"type\":\"notify\",\"notify\":{\"method\":\"x\",\"args\":{\"a\":\"\xff\xc3(\"}}}", false},
		{"unpaired surrogate escape", `{"type":"notify","notify":{"method":"x","args":{"a":"\ud800"}}}`, false},
		{"truncated", `{"type":"notify","notify":{"method":"x","args":{"a":"b"}`, true},
		{"truncated inside a string", `{"type":"notify","notify":{"method":"x","args":{"a":"b`, true},
		{"truncated escape", `{"type":"notify","notify":{"method":"x","args":{"a":"b\`, true},
		{"bad escape", `{"type":"notify","notify":{"method":"x","args":{"a":"\q"}}}`, true},
		{"short unicode escape", `{"type":"notify","notify":{"method":"x","args":{"a":"\u00"}}}`, true},
		{"control character in a string", "{\"type\":\"notify\",\"notify\":{\"method\":\"x\",\"args\":{\"a\":\"b\x01\"}}}", true},
		{"data after the document", `{"type":"notify"} x`, true},
		{"two documents", `{"type":"notify"}{"type":"notify"}`, true},
		{"trailing comma", `{"type":"notify","notify":{"method":"x","args":[1,]}}`, true},
		{"bad number", `{"type":"notify","notify":{"method":"x","args":01}}`, true},
		{"array, not an envelope", `[]`, true},
		{"empty", ``, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			host, peer := net.Pipe()
			defer func() { _ = host.Close(); _ = peer.Close() }()
			c := newConn(host)
			c.start()
			frame := binary.BigEndian.AppendUint32(nil, uint32(len(test.body)))
			go func() { _, _ = peer.Write(append(frame, test.body...)) }()
			select {
			case env, open := <-c.incoming:
				// The read loop closes incoming when it ends: a closed channel is the end of the connection, an envelope is a frame taken.
				switch {
				case test.refused && open:
					t.Fatalf("a frame the reader refuses produced %+v", env)
				case !test.refused && !open:
					t.Fatal("the connection ended on a frame the reader takes")
				case !test.refused && (env.Type != msgNotify || env.Notify == nil):
					t.Fatalf("envelope %+v", env)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("neither an envelope nor the end of the connection")
			}
		})
	}
}

// A request's Args are the frame's own bytes, a notification's too: a frame is read into a buffer of its own, so nothing reuses it.
func TestRequestArgsKeepTheirBytesAcrossFrames(t *testing.T) {
	host, peer := net.Pipe()
	defer func() { _ = host.Close(); _ = peer.Close() }()
	c := newConn(host)
	c.start()
	bodies := []string{
		`{"type":"request","id":"1","request":{"method":"event","event":"e","handler_id":1,"args":{"n":"first","pad":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}}}`,
		`{"type":"request","id":"2","request":{"method":"event","event":"e","handler_id":1,"args":{"n":"second","pad":"yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"}}}`,
	}
	go func() {
		for _, body := range bodies {
			frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
			_, _ = peer.Write(append(frame, body...))
		}
	}()
	var got []envelope
	for range bodies {
		select {
		case env, open := <-c.incoming:
			if !open {
				t.Fatal("connection ended")
			}
			got = append(got, env)
		case <-time.After(10 * time.Second):
			t.Fatal("no envelope")
		}
	}
	want := []string{`{"n":"first","pad":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`, `{"n":"second","pad":"yyyyyyyyyyyyyyyyyyyyyyyyyyyyyyyy"}`}
	for i, env := range got {
		if string(env.Request.Args) != want[i] {
			t.Errorf("frame %d: Args %q, want %q", i, env.Request.Args, want[i])
		}
	}
}
