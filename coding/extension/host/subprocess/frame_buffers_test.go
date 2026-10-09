package subprocess

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"strconv"
	"testing"
)

func TestFrameClassCoversEverySizeWithABufferThatFits(t *testing.T) {
	for _, n := range []int{0, 1, 511, 512, 513, 1023, 1024, 1025, 4096, 65535, 65536, 1<<20 - 1, 1 << 20} {
		class := frameClass(n)
		if class < 0 || class >= len(frameBufferPools) {
			t.Fatalf("frameClass(%d) = %d, outside the %d pools", n, class, len(frameBufferPools))
		}
		frame := acquireFrameBuffer(n)
		if len(frame.data) != n || cap(frame.data) != pooledFrameMin<<class || cap(frame.data) < n {
			t.Fatalf("n = %d: len %d cap %d class %d", n, len(frame.data), cap(frame.data), class)
		}
		// The size class is the smallest power of two that holds n.
		if class > 0 && n <= pooledFrameMin<<(class-1) {
			t.Fatalf("n = %d took class %d, but class %d holds it", n, class, class-1)
		}
		frame.release()
	}
	for _, n := range []int{1<<20 + 1, 128 << 20} {
		if frameClass(n) != -1 {
			t.Fatalf("frameClass(%d) = %d, want -1 (not pooled)", n, frameClass(n))
		}
	}
}

// A released buffer is reused with the next payload's length, and an oversized frame never enters a pool.
func TestFrameBufferReuseKeepsLengthAndSkipsOversizedFrames(t *testing.T) {
	first := acquireFrameBuffer(3000)
	first.release()
	second := acquireFrameBuffer(2100)
	if len(second.data) != 2100 || cap(second.data) != 4096 {
		t.Fatalf("len %d cap %d", len(second.data), cap(second.data))
	}
	second.release()
	big := acquireFrameBuffer(pooledFrameMax + 1)
	if big.class != -1 || len(big.data) != pooledFrameMax+1 {
		t.Fatalf("oversized frame class %d len %d", big.class, len(big.data))
	}
	big.release()
}

func TestReadFrameBufferReportsShortReads(t *testing.T) {
	if _, err := readFrameBuffer(bytes.NewReader([]byte("short")), 100); err == nil {
		t.Fatal("short frame read succeeded")
	}
	frame, err := readFrameBuffer(bytes.NewReader([]byte("exactly")), 7)
	if err != nil || string(frame.data) != "exactly" {
		t.Fatalf("frame = %v, err = %v", frame, err)
	}
	frame.release()
}

// An envelope decoded from a pooled buffer keeps its bytes after the buffer is reused for another frame.
func TestEnvelopeDecodedFromPooledFrameSurvivesReuse(t *testing.T) {
	encode := func(id string, payload string) []byte {
		data, err := json.Marshal(Envelope{Type: MsgResponse, ID: id, Response: &ResponsePayload{Result: json.RawMessage(`{"v":"` + payload + `"}`)}})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first, second := encode("one", "AAAAAAAA"), encode("two", "BBBBBBBB")
	var decoded [2]Envelope
	for i, data := range [][]byte{first, second} {
		frame, err := readFrameBuffer(bytes.NewReader(data), len(data))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(frame.data, &decoded[i]); err != nil {
			t.Fatal(err)
		}
		frame.release()
	}
	if decoded[0].ID != "one" || string(decoded[0].Response.Result) != `{"v":"AAAAAAAA"}` || decoded[1].ID != "two" || string(decoded[1].Response.Result) != `{"v":"BBBBBBBB"}` {
		t.Fatalf("decoded = %+v / %+v", decoded[0], decoded[1])
	}
}

func benchmarkFrames(size int) []byte {
	payload, _ := json.Marshal(Envelope{Type: MsgResponse, ID: "id", Response: &ResponsePayload{Result: json.RawMessage(`"` + string(bytes.Repeat([]byte("x"), size)) + `"`)}})
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
	return append(frame, payload...)
}

// BenchmarkReadFramePayload reads and decodes one frame: a fresh buffer per frame, as the read loop did, against a pooled buffer.
func BenchmarkReadFramePayload(b *testing.B) {
	for _, size := range []int{200, 8 << 10, 256 << 10} {
		wire := benchmarkFrames(size)
		b.Run("fresh-"+byteSize(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				payload := wire[4:]
				buf := make([]byte, len(payload))
				if _, err := io.ReadFull(bytes.NewReader(payload), buf); err != nil {
					b.Fatal(err)
				}
				var env Envelope
				if err := json.Unmarshal(buf, &env); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("pooled-"+byteSize(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				payload := wire[4:]
				frame, err := readFrameBuffer(bytes.NewReader(payload), len(payload))
				if err != nil {
					b.Fatal(err)
				}
				var env Envelope
				err = json.Unmarshal(frame.data, &env)
				frame.release()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func byteSize(n int) string { return strconv.Itoa(n) + "B" }
