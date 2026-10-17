package subprocess

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/bits"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Frames of up to pooledFrameMax bytes are read into reused buffers. A larger frame is rare and gets a buffer of its own, which the collector reclaims, so one large frame never pins memory in the pool.
const (
	// pooledFrameMinShift is log2 of the smallest pooled buffer: 512 bytes holds a typical streaming delta envelope, and a smaller class would only add pools.
	// pig additive (D112): Pi reads each frame into a fresh Buffer; pooling is a Go allocation choice with no observable effect.
	pooledFrameMinShift = 9
	// pooledFrameMaxShift is log2 of the largest pooled buffer: 1 MiB covers every ordinary frame, and a larger frame is read into a buffer of its own so the pool never pins one.
	// pig additive (D112): see pooledFrameMinShift.
	pooledFrameMaxShift = 20
	pooledFrameMin      = 1 << pooledFrameMinShift
	pooledFrameMax      = 1 << pooledFrameMaxShift
)

// frameBuffer holds one frame payload. Its bytes are valid until release.
type frameBuffer struct {
	data  []byte
	class int
}

// frameBufferPools holds one pool per power-of-two size from pooledFrameMin to pooledFrameMax.
var frameBufferPools [pooledFrameMaxShift - pooledFrameMinShift + 1]sync.Pool

// frameClass is the pool index for a payload of n bytes, or -1 when n is too large to pool.
func frameClass(n int) int {
	if n > pooledFrameMax {
		return -1
	}
	return bits.Len(uint(max(n, pooledFrameMin)-1)) - pooledFrameMinShift
}

// readFrameBuffer reads exactly n payload bytes from r into a pooled buffer when n is small enough to pool. The caller releases the buffer once it no longer reads its bytes.
func readFrameBuffer(r io.Reader, n int) (*frameBuffer, error) {
	frame := acquireFrameBuffer(n)
	if _, err := io.ReadFull(r, frame.data); err != nil {
		frame.release()
		return nil, err
	}
	return frame, nil
}

func acquireFrameBuffer(n int) *frameBuffer {
	class := frameClass(n)
	if class < 0 {
		return &frameBuffer{data: make([]byte, n), class: -1}
	}
	if pooled, ok := frameBufferPools[class].Get().(*frameBuffer); ok {
		pooled.data = pooled.data[:n]
		return pooled
	}
	return &frameBuffer{data: make([]byte, n, pooledFrameMin<<class), class: class}
}

// release returns the buffer to its pool. The caller must not use the bytes afterwards.
func (frame *frameBuffer) release() {
	if frame.class < 0 {
		return
	}
	frameBufferPools[frame.class].Put(frame)
}

// encodedFrame is one outbound frame under construction: the 4-byte big-endian length followed by the envelope's JSON. The frame and its encoder come from a pool the writer refills once the bytes are on the socket, so encoding a frame allocates nothing in steady state. It is an io.Writer so the encoder appends straight into buf.
//
// A request whose Args are the host encoder's own output (RequestPayload.encodedArgs) is a split frame: buf holds the envelope encoded with the one-byte placeholder argsPlaceholder for Args, argsAt is the placeholder's offset in buf, and args is the caller's Args. The frame's JSON is buf[4:argsAt] + args + buf[argsAt+1:], byte for byte json.Marshal of the envelope: the encoder's compaction of an already compact, escaped RawMessage is the identity, so the split skips only that pass over Args. The prefix and suffix come from the same encoding, so members after args keep their place. The args slice is the caller's and is read, never written or pooled.
type encodedFrame struct {
	buf    []byte
	enc    *json.Encoder
	argsAt int
	args   []byte
}

// Write appends to the frame.
func (f *encodedFrame) Write(p []byte) (int, error) {
	f.buf = append(f.buf, p...)
	return len(p), nil
}

// size is the length of the frame's JSON.
func (f *encodedFrame) size() int {
	if f.argsAt == 0 {
		return len(f.buf) - 4
	}
	return len(f.buf) - 4 - len(argsPlaceholder) + len(f.args)
}

// parts returns the frame's JSON as the bytes before Args, Args and the bytes after it, without the length prefix. An unsplit frame is all prefix.
func (f *encodedFrame) parts() (prefix, args, suffix []byte) {
	if f.argsAt == 0 {
		return f.buf[4:], nil, nil
	}
	return f.buf[4:f.argsAt], f.args, f.buf[f.argsAt+len(argsPlaceholder):]
}

// payload returns the frame's JSON; a split frame is joined into a new slice.
func (f *encodedFrame) payload() []byte {
	prefix, args, suffix := f.parts()
	if args == nil {
		return prefix
	}
	return slices.Concat(prefix, args, suffix)
}

// argsPlaceholder stands for a split frame's Args while its envelope is encoded; argsMember is how the encoder writes it. No member before args can contain argsMember: they are a string type and id, and the request's method, tool, event, handler_id and tool_call_id, and an encoded string escapes every quote inside it.
var (
	argsPlaceholder = json.RawMessage("0")
	argsMember      = []byte(`"args":0`)
)

var encodedFramePool sync.Pool

// encodeFrame encodes env as a length-prefixed frame. The encoder escapes as json.Marshal does, so the bytes are the ones the frame carried before pooling. The caller releases the frame once it is written or abandoned.
func encodeFrame(env *Envelope) (*encodedFrame, error) {
	f, _ := encodedFramePool.Get().(*encodedFrame)
	if f == nil {
		f = &encodedFrame{buf: make([]byte, 0, pooledFrameMin)}
		//portlint:allow jsonescape host-runtime IPC frame, not Pi output: the receiver's JSON.parse or decoder reads \u003c and \u0026 as the same text, and the bytes stay the ones json.Marshal gave the frame
		f.enc = json.NewEncoder(f)
	}
	f.buf = append(f.buf[:0], 0, 0, 0, 0)
	f.argsAt, f.args = 0, nil
	if req := env.Request; req != nil && req.encodedArgs && len(req.Args) > 0 && env.Register == nil && env.Ready == nil {
		return f.encodeSplit(env)
	}
	if err := f.enc.Encode(env); err != nil {
		f.release()
		return nil, err
	}
	// Encode ends the value with a newline that the frame format does not carry.
	f.buf = f.buf[:len(f.buf)-1]
	binary.BigEndian.PutUint32(f.buf[:4], uint32(len(f.buf)-4))
	return f, nil
}

// encodeSplit encodes a request whose Args are encoder output with the placeholder in their place, and records where Args go. The envelope is restored before return.
func (f *encodedFrame) encodeSplit(env *Envelope) (*encodedFrame, error) {
	req := env.Request
	args := req.Args
	req.Args = argsPlaceholder
	err := f.enc.Encode(env)
	req.Args = args
	if err != nil {
		f.release()
		return nil, err
	}
	f.buf = f.buf[:len(f.buf)-1]
	at := bytes.Index(f.buf[4:], argsMember)
	if at < 0 {
		f.release()
		return nil, errors.New("marshal envelope: request args member not found")
	}
	f.argsAt = 4 + at + len(argsMember) - len(argsPlaceholder)
	f.args = args
	binary.BigEndian.PutUint32(f.buf[:4], uint32(min(f.size(), math.MaxUint32)))
	return f, nil
}

// release returns the frame to the pool; a frame that grew past the pooled maximum is dropped so one large frame never pins memory.
func (f *encodedFrame) release() {
	f.args = nil
	if cap(f.buf) > pooledFrameMax {
		return
	}
	encodedFramePool.Put(f)
}
