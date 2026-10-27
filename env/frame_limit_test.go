package env

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// mustFrame is encodeFrame for a frame the test knows fits.
func mustFrame(kind byte, id uint32, value Json, payload []byte) []byte {
	frame, err := encodeFrame(kind, id, value, payload)
	if err != nil {
		panic(err)
	}
	return frame
}

func TestEncodeFrameRejectsWhatPassesTheFrameLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		value   Json
		payload []byte
		wantErr bool
	}{
		{"payload over the limit", Json{}, make([]byte, maxFrame+1), true},
		{"json over the limit", Json{"text": strings.Repeat("x", maxFrame)}, nil, true},
		{"json and payload each fit, together they do not", Json{"text": strings.Repeat("x", maxFrame/2)}, make([]byte, maxFrame/2), true},
		{"frame of exactly the limit", Json{}, make([]byte, maxFrame-9-len("{}")), false},
		{"one byte over the limit", Json{}, make([]byte, maxFrame-9-len("{}")+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			frame, err := encodeFrame(frameRequest, 1, tc.value, tc.payload)
			if tc.wantErr {
				if !errors.Is(err, errFrameTooLarge) || frame != nil {
					t.Fatalf("encodeFrame() = %d bytes, %v; want errFrameTooLarge", len(frame), err)
				}
				return
			}
			if err != nil || len(frame) != 4+maxFrame {
				t.Fatalf("encodeFrame() = %d bytes, %v; want one frame of %d bytes", len(frame), err, 4+maxFrame)
			}
		})
	}
}

func TestRequestOverTheFrameLimitFailsWithoutReachingTheDaemon(t *testing.T) {
	t.Parallel()
	connection := scriptedConnection(t, syncLine+helloResult+`exec sleep 30`, nil)
	for _, tc := range []struct {
		name    string
		request Json
		payload []byte
	}{
		{"payload", Json{"path": "/x"}, make([]byte, maxFrame+1)},
		{"json", Json{"path": strings.Repeat("x", maxFrame)}, nil},
	} {
		_, err := connection.Request(context.Background(), "write", tc.request, RequestOptions{Payload: tc.payload})
		remote, ok := errors.AsType[*RemoteError](err)
		if !ok || remote.Code != "EINVAL" || IsConnectionLost(err) {
			t.Fatalf("%s: Request() error = %v, want an EINVAL *RemoteError that is not a lost connection", tc.name, err)
		}
	}
}

func TestEncodeRequestRejectsABodyOverTheFrameLimit(t *testing.T) {
	t.Parallel()
	if _, err := encodeRequest(1, "x", Json{"a": 1}, nil); err != nil {
		t.Fatalf("encodeRequest() of a small request: %v", err)
	}
	if _, err := encodeRequest(1, "x", Json{"a": strings.Repeat("y", maxFrame)}, nil); err == nil {
		t.Fatal("encodeRequest() of a body over the frame limit succeeded")
	}
}
