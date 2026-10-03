package subprocess

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	wirejson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func TestFrameTooLargeError_Message(t *testing.T) {
	e := &FrameTooLargeError{Size: 200, Max: 128}
	if got := e.Error(); !strings.Contains(got, "200") || !strings.Contains(got, "128") {
		t.Fatalf("error should name both sizes: %q", got)
	}
}

// TestConn_Send_RejectsOversizedFrame proves the host refuses to write a frame
// no compliant extension could read, returning a typed error instead of
// queueing an undeliverable frame.
func TestConn_Send_RejectsOversizedFrame(t *testing.T) {
	t.Parallel()
	hostEnd, extEnd := net.Pipe()
	c := NewConn("t", hostEnd)
	c.Start(t.Context())
	go func() { _, _ = io.Copy(io.Discard, extEnd) }()

	big := oversizedJSONString()
	for _, tc := range []struct {
		name  string
		env   *Envelope
		shell string
	}{
		// checkResultFrameSize sizes only call results, so a notify frame proves the general marshal-then-check rejection.
		{"notify", &Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "m", Args: big}}, `{"type":"notify","notify":{"method":"m","args":}}`},
		{"call result", &Envelope{Type: MsgCallResult, ID: "1", CallResult: &CallResultPayload{Result: big}}, `{"type":"call_result","id":"1","call_result":{"result":}}`},
	} {
		err := c.Send(tc.env)
		var tooLarge *FrameTooLargeError
		if !errors.As(err, &tooLarge) {
			t.Fatalf("%s: oversized send should return *FrameTooLargeError, got %v", tc.name, err)
		}
		if want := len(big) + len(tc.shell); tooLarge.Size != want || tooLarge.Max != MaxFrameSize {
			t.Fatalf("%s: reported size %d max %d, want %d max %d", tc.name, tooLarge.Size, tooLarge.Max, want, MaxFrameSize)
		}
	}

	// A normal frame still sends.
	if err := c.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{}}); err != nil {
		t.Fatalf("small send should succeed, got %v", err)
	}
}

// oversizedJSONString is a compact JSON string literal that alone exceeds MaxFrameSize, built without encoding it byte by byte.
func oversizedJSONString() json.RawMessage {
	raw := make([]byte, 0, MaxFrameSize+2)
	raw = append(raw, '"')
	raw = append(raw, bytes.Repeat([]byte{'x'}, MaxFrameSize)...)
	return append(raw, '"')
}

// TestMarshalEnvelope_OversizedCompactResultSkipsEncoding proves an oversized compact call result is rejected from its length alone: the exact frame size is reported without validating and copying the payload a second time.
func TestMarshalEnvelope_OversizedCompactResultSkipsEncoding(t *testing.T) {
	big := oversizedJSONString()
	env := &Envelope{Type: MsgCallResult, ID: "1", CallResult: &CallResultPayload{Result: big}}
	small, err := json.Marshal(&Envelope{Type: MsgCallResult, ID: "1", CallResult: &CallResultPayload{Result: json.RawMessage("0")}})
	if err != nil {
		t.Fatal(err)
	}
	want := len(small) - 1 + len(big)
	c := &Conn{}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err = c.marshalEnvelope(env)
	runtime.ReadMemStats(&after)
	tooLarge, ok := errors.AsType[*FrameTooLargeError](err)
	if !ok || tooLarge.Size != want || tooLarge.Max != MaxFrameSize {
		t.Fatalf("want FrameTooLargeError size %d, got %v", want, err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("rejecting an oversized result allocated %d bytes; it must not copy the payload", allocated)
	}
}

// resultAllocation runs marshalEnvelope on a call result and returns its error and the bytes it allocated.
func resultAllocation(t *testing.T, raw json.RawMessage) (uint64, error) {
	t.Helper()
	env := &Envelope{Type: MsgCallResult, ID: "1", CallResult: &CallResultPayload{Result: raw}}
	c := &Conn{}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := c.marshalEnvelope(env)
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc, err
}

// TestMarshalEnvelope_OversizedResultRejectedWithoutCopy proves the rejection of an oversized call result costs no memory proportional to the payload for ordinary valid JSON: whitespace inside a string does not disable it, and a raw value of exactly MaxFrameSize bytes is rejected because the envelope around it pushes the frame over the limit.
func TestMarshalEnvelope_OversizedResultRejectedWithoutCopy(t *testing.T) {
	spaced := oversizedJSONString()
	spaced[1] = ' '
	exact := oversizedJSONString()[:MaxFrameSize]
	exact[MaxFrameSize-1] = '"' // keeps a valid string literal of exactly MaxFrameSize bytes
	// Whitespace outside a string, an escaped quote and a trailing backslash pair must not confuse the string tracking.
	outside := append(append([]byte(` [ "a\\\"" , `), oversizedJSONString()...), []byte(" ]\n")...)
	for name, raw := range map[string]json.RawMessage{"space inside string": spaced, "exactly MaxFrameSize": exact, "whitespace outside strings": outside} {
		allocated, err := resultAllocation(t, raw)
		tooLarge, ok := errors.AsType[*FrameTooLargeError](err)
		if !ok || tooLarge.Max != MaxFrameSize {
			t.Fatalf("%s: want FrameTooLargeError, got %v", name, err)
		}
		if allocated > 1<<20 {
			t.Fatalf("%s: rejecting an oversized result allocated %d bytes; it must not copy the payload", name, allocated)
		}
	}
}

// TestMarshalEnvelope_OversizedInvalidResultKeepsMarshalError proves a malformed oversized raw result still reports the encoder's validation error rather than a size error.
func TestMarshalEnvelope_OversizedInvalidResultKeepsMarshalError(t *testing.T) {
	_, err := resultAllocation(t, bytes.Repeat([]byte{'x'}, MaxFrameSize+1))
	if _, ok := errors.AsType[*FrameTooLargeError](err); ok || err == nil || !strings.Contains(err.Error(), "marshal envelope") {
		t.Fatalf("want a marshal envelope error for invalid raw JSON, got %v", err)
	}
}

// TestMarshalEnvelope_OversizedRawThatCompactsWithinLimitIsSent proves the size is that of the encoded frame: padding whitespace outside strings does not make a small value oversized.
func TestMarshalEnvelope_OversizedRawThatCompactsWithinLimitIsSent(t *testing.T) {
	raw := append(bytes.Repeat([]byte{' '}, MaxFrameSize+16), '1')
	if _, err := resultAllocation(t, raw); err != nil {
		t.Fatalf("a result that encodes to a few bytes must be accepted, got %v", err)
	}
}

// TestEncodedRawSize_MatchesEncoder proves encodedRawSize equals the length the wire codec that marshalEnvelope uses emits for valid raw JSON.
func TestEncodedRawSize_MatchesEncoder(t *testing.T) {
	for _, raw := range []string{
		`0`, " [ 1 , 2 ]\n", "\"a b\\t\"", `"say \"hi\" "`, `"back\\"`, `"back\\" `,
		"{\"k <&> \":[ \"\\u2028\", \"\u2028\u2029\", \"e\\\\\\\"<\" ], \"x\" : null}",
		"\t\r\n{ \"a\" :\t\"&\" }\r\n", "\"\u00e2\u0080\"", "\"\xe2\x80\"",
	} {
		framed, err := wirejson.Marshal(&Envelope{Type: MsgCallResult, CallResult: &CallResultPayload{Result: json.RawMessage(raw)}})
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		shell, _ := wirejson.Marshal(&Envelope{Type: MsgCallResult, CallResult: &CallResultPayload{Result: json.RawMessage("0")}})
		if got, want := int64(len(shell)-1)+encodedRawSize([]byte(raw)), int64(len(framed)); got != want {
			t.Errorf("%q: size %d, encoder %d", raw, got, want)
		}
	}
}

// TestMarshalEnvelope_OversizedCompactResultReportsEscapedSize proves the size of a rejected compact result counts the bytes the encoder adds: it rewrites <, > and & as six-byte escapes and U+2028/U+2029 as six-byte escapes inside a raw message.
func TestMarshalEnvelope_OversizedCompactResultReportsEscapedSize(t *testing.T) {
	t.Parallel()
	raw := oversizedJSONString()
	tail := []byte("<>&\u2028\u2029\"")
	raw = append(raw[:len(raw)-1], tail...)
	env := &Envelope{Type: MsgCallResult, ID: "1", CallResult: &CallResultPayload{Result: raw}}
	full, err := wirejson.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) <= len(raw) {
		t.Fatalf("encoder should have expanded the payload: %d <= %d", len(full), len(raw))
	}
	c := &Conn{}
	_, err = c.marshalEnvelope(env)
	tooLarge, ok := errors.AsType[*FrameTooLargeError](err)
	if !ok || tooLarge.Size != len(full) || tooLarge.Max != MaxFrameSize {
		t.Fatalf("want FrameTooLargeError size %d (len of json.Marshal), got %v", len(full), err)
	}
}

// escapeHeavyString is a compact JSON string of n '<' bytes, which the encoder writes as six bytes each.
func escapeHeavyString(n int) json.RawMessage {
	raw := make([]byte, 0, n+2)
	raw = append(raw, '"')
	raw = append(raw, bytes.Repeat([]byte{'<'}, n)...)
	return append(raw, '"')
}

// TestMarshalEnvelope_OversizedEscapeHeavyResultRejectedWithoutCopy proves a result whose raw length is far below the limit but whose escaped frame is above it is rejected without building the frame: 22,369,619 '<' bytes in a raw value of MaxFrameSize/6 bytes encode to 134,217,761 bytes, and encoding them first allocated about a gigabyte.
func TestMarshalEnvelope_OversizedEscapeHeavyResultRejectedWithoutCopy(t *testing.T) {
	const n = 22369619 // a raw value of exactly MaxFrameSize/6 bytes
	shell, err := wirejson.Marshal(&Envelope{Type: MsgCallResult, ID: "1", CallResult: &CallResultPayload{Result: json.RawMessage("0")}})
	if err != nil {
		t.Fatal(err)
	}
	want := len(shell) - 1 + 6*n + 2
	if want <= MaxFrameSize {
		t.Fatalf("test premise: %d escaped bytes must exceed the limit, frame is %d", n, want)
	}
	allocated, err := resultAllocation(t, escapeHeavyString(n))
	tooLarge, ok := errors.AsType[*FrameTooLargeError](err)
	if !ok || tooLarge.Size != want || tooLarge.Max != MaxFrameSize {
		t.Fatalf("want FrameTooLargeError size %d, got %v", want, err)
	}
	if allocated > 1<<20 {
		t.Fatalf("rejecting an escape-heavy result allocated %d bytes; it must not build the frame", allocated)
	}
}

// TestMarshalEnvelope_LargeErrorShellIsMeasured proves the text around a result counts toward the bound that skips measurement: a result whose own escaped size fits the limit is still rejected without building the frame when an escaped error message pushes the frame over it.
func TestMarshalEnvelope_LargeErrorShellIsMeasured(t *testing.T) {
	const n = 22_300_000
	msg := strings.Repeat("<", 200_000)
	raw := escapeHeavyString(n)
	if 6*len(raw) > MaxFrameSize {
		t.Fatalf("test premise: the result alone must fit, 6*%d > %d", len(raw), MaxFrameSize)
	}
	env := &Envelope{Type: MsgCallResult, ID: "1", CallResult: &CallResultPayload{Result: raw, Error: &ErrorInfo{Message: msg}}}
	full, err := wirejson.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) <= MaxFrameSize {
		t.Fatalf("test premise: frame is %d", len(full))
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err = (&Conn{}).marshalEnvelope(env)
	runtime.ReadMemStats(&after)
	if tooLarge, ok := errors.AsType[*FrameTooLargeError](err); !ok || tooLarge.Size != len(full) {
		t.Fatalf("want FrameTooLargeError size %d, got %v", len(full), err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
		t.Fatalf("rejecting the result allocated %d bytes; it must not build the frame", allocated)
	}
}

// TestMarshalEnvelope_OverflowingResultSizeIsOversized proves a valid result whose encoded size exceeds the largest int of a 32-bit build is measured as oversized: 360,000,000 '<' bytes encode to 2,160,000,002 bytes, which wrapped to a negative size and passed the limit check.
func TestMarshalEnvelope_OverflowingResultSizeIsOversized(t *testing.T) {
	const n = 360_000_000
	raw := escapeHeavyString(n)
	if got, want := encodedRawSize(raw), int64(6*n+2); got != want {
		t.Fatalf("encodedRawSize = %d, want %d", got, want)
	}
	_, err := resultAllocation(t, raw)
	if tooLarge, ok := errors.AsType[*FrameTooLargeError](err); !ok || tooLarge.Size <= MaxFrameSize || tooLarge.Max != MaxFrameSize {
		t.Fatalf("want FrameTooLargeError above the limit, got %v", err)
	}
}

// TestOversizedFrameError_SaturatesReportedSize proves a size beyond the largest int is reported as that maximum, not a wrapped value.
func TestOversizedFrameError_SaturatesReportedSize(t *testing.T) {
	for _, size := range []int64{MaxFrameSize + 1, 1 << 40, math.MaxInt64} {
		err := oversizedFrameError(size)
		if want := int(min(size, math.MaxInt)); err.Size != want || err.Size <= MaxFrameSize || err.Max != MaxFrameSize {
			t.Fatalf("size %d reported as %d, want %d", size, err.Size, want)
		}
	}
}

// TestHandleIncoming_OversizedResultReturnsError drives the production call
// path: when a call result is too large for the IPC frame limit, the extension
// must receive a clean error result rather than nothing (a silent death).
func TestHandleIncoming_OversizedResultReturnsError(t *testing.T) {
	hostEnd, extEnd := net.Pipe()
	c := NewConn("ctx", hostEnd)
	c.Start(t.Context())

	h := NewHost(t.TempDir())
	h.SetCallHandler(func(extName string, call *CallPayload) (*CallResultPayload, error) {
		return &CallResultPayload{Result: oversizedJSONString()}, nil
	})
	me := withConn(&managedExt{
		config:     ExtConfig{Name: "ctx"},
		host:       h,
		supervisor: NewSupervisor(DefaultSupervisorConfig()),
	}, c)
	go h.handleIncoming(me, me.connection())

	// Extension issues a call that yields an oversized result.
	callFrame, _ := json.Marshal(&Envelope{Type: MsgCall, ID: "1", Call: &CallPayload{Method: "sessionRead"}})
	go func() {
		var lb [4]byte
		binary.BigEndian.PutUint32(lb[:], uint32(len(callFrame)))
		_, _ = extEnd.Write(lb[:])
		_, _ = extEnd.Write(callFrame)
	}()

	// Read the host's response frame.
	_ = extEnd.SetReadDeadline(time.Now().Add(15 * time.Second))
	var lb [4]byte
	if _, err := io.ReadFull(extEnd, lb[:]); err != nil {
		t.Fatalf("read response length: %v", err)
	}
	n := binary.BigEndian.Uint32(lb[:])
	if n > MaxFrameSize {
		t.Fatalf("error response frame %d should be small, not oversized", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(extEnd, buf); err != nil {
		t.Fatalf("read response frame: %v", err)
	}
	var resp Envelope
	if err := json.Unmarshal(buf, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Type != MsgCallResult || resp.CallResult == nil || resp.CallResult.Error == nil {
		t.Fatalf("expected an error call_result, got %+v", resp)
	}
	if resp.CallResult.Error.Code != "result_too_large" {
		t.Fatalf("expected result_too_large, got %q (%s)", resp.CallResult.Error.Code, resp.CallResult.Error.Message)
	}
}
