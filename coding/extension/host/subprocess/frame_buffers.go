package subprocess

import (
	"io"
	"math/bits"
	"sync"
)

// Frames of up to pooledFrameMax bytes are read into reused buffers. A larger frame is rare and gets a buffer of its own, which the collector reclaims, so one large frame never pins memory in the pool.
const (
	pooledFrameMinShift = 9
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
