package daemon

// Ports packages/env/daemon/src/frame.rs tests

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
)

func TestFrameRoundTripsJSONAndPayload(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, &Frame{Kind: FrameResult, ID: 7, JSON: Object{"a": float64(1)}, Payload: []byte{0, 255, 10}}); err != nil {
		t.Fatal(err)
	}
	frame, err := ReadFrame(&buffer)
	if err != nil || frame == nil {
		t.Fatalf("read: %v, %v", frame, err)
	}
	if frame.Kind != FrameResult || frame.ID != 7 || !reflect.DeepEqual(frame.JSON, Object{"a": float64(1)}) || !bytes.Equal(frame.Payload, []byte{0, 255, 10}) {
		t.Fatalf("frame = %+v", frame)
	}
	if end, err := ReadFrame(bytes.NewReader(nil)); end != nil || err != nil {
		t.Fatalf("end of input: %v, %v", end, err)
	}
}

func TestFrameLayoutIsBigEndianWithJSONLengthBeforeTheJSON(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, &Frame{Kind: FrameEvent, ID: 0x01020304, JSON: Object{"k": "v"}, Payload: []byte("xy")}); err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0, 0, 0, 9 + 9 + 2, 4, 1, 2, 3, 4, 0, 0, 0, 9}, []byte(`{"k":"v"}xy`)...)
	if !bytes.Equal(buffer.Bytes(), want) {
		t.Fatalf("frame bytes %v, want %v", buffer.Bytes(), want)
	}
}

func TestFrameReadingKeepsFramingWhenJSONDoesNotParseAndRejectsBadLengths(t *testing.T) {
	unparseable := []byte{0, 0, 0, 9 + 3, 1, 0, 0, 0, 5, 0, 0, 0, 3, '{', '{', '{'}
	frame, err := ReadFrame(bytes.NewReader(unparseable))
	if err != nil || frame == nil || frame.JSON != nil || frame.ID != 5 {
		t.Fatalf("unparseable JSON: %+v, %v", frame, err)
	}
	empty := []byte{0, 0, 0, 9, 1, 0, 0, 0, 6, 0, 0, 0, 0}
	if frame, err := ReadFrame(bytes.NewReader(empty)); err != nil || !reflect.DeepEqual(frame.JSON, Object{}) {
		t.Fatalf("empty JSON: %+v, %v", frame, err)
	}
	for name, input := range map[string][]byte{
		"too short":       {0, 0, 0, 8, 1, 0, 0, 0, 0, 0, 0, 0},
		"too long":        {0x01, 0x00, 0x00, 0x01, 1},
		"JSON past frame": {0, 0, 0, 9, 1, 0, 0, 0, 0, 0, 0, 0, 1},
		"truncated frame": {0, 0, 0, 20, 1, 0, 0, 0, 0, 0, 0, 0, 0},
	} {
		if frame, err := ReadFrame(bytes.NewReader(input)); err == nil && frame != nil {
			t.Errorf("%s: a frame was read: %+v", name, frame)
		} else if err == nil || errors.Is(err, io.EOF) {
			t.Errorf("%s: err = %v, want a framing error", name, err)
		}
	}
}

func TestFrameWritingRefusesAFrameOverTheLimit(t *testing.T) {
	err := WriteFrame(io.Discard, &Frame{Kind: FrameResult, JSON: Object{}, Payload: make([]byte, MaxFrame)})
	if err == nil {
		t.Fatal("a 16 MiB payload was written")
	}
}
