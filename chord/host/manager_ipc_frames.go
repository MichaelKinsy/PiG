package host

// libuv's IPC framing, which Node's IPC channel speaks on Windows (uv_pipe_t with ipc set; unix domain sockets carry
// the JSON lines bare). Every write is one frame, a 16-byte header followed by the payload; the channel's JSON text
// and newline are the payload (libuv src/win/pipe.c uv__ipc_frame_header_t, uv__pipe_write_ipc, uv__pipe_read_ipc).

import (
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

const (
	// ipcFrameHeaderBytes is sizeof(uv__ipc_frame_header_t): flags, reserved1, data_length, reserved2 (uint32, little endian).
	ipcFrameHeaderBytes = 16
	// UV__IPC_FRAME_HAS_DATA: the frame carries data_length bytes of payload.
	ipcFrameHasData = 0x01
	// UV__IPC_FRAME_HAS_SOCKET_XFER and UV__IPC_FRAME_XFER_IS_TCP_CONNECTION: a transferred socket (Node's send(message, handle)).
	ipcFrameHasSocketXfer = 0x02
	ipcFrameXferIsTCP     = 0x04
	ipcFrameValidFlags    = ipcFrameHasData | ipcFrameHasSocketXfer | ipcFrameXferIsTCP
	// sizeof(uv__ipc_socket_xfer_info_t) follows the header of a frame that transfers a socket.
	ipcSocketXferInfoBytes = 632
)

var errIPCFrame = errors.New("invalid IPC frame")

// ipcFrames speaks libuv's IPC frames over conn: Read yields the payload bytes of the frames in order, Write sends one
// frame. A frame that transfers a socket is skipped (its handle cannot be adopted); one libuv would reject, as
// uv__pipe_read_ipc does for flags it does not know or a reserved2 that is not zero, ends the channel.
type ipcFrames struct {
	conn io.ReadWriteCloser
	// remaining is the payload bytes of the current frame not yet returned.
	remaining uint32
	header    [ipcFrameHeaderBytes]byte
	write     sync.Mutex
}

func newIPCFrames(conn io.ReadWriteCloser) *ipcFrames { return &ipcFrames{conn: conn} }

func (frames *ipcFrames) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for frames.remaining == 0 {
		if _, err := io.ReadFull(frames.conn, frames.header[:]); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				err = io.EOF
			}
			return 0, err
		}
		flags := binary.LittleEndian.Uint32(frames.header[0:])
		length := binary.LittleEndian.Uint32(frames.header[8:])
		reserved2 := binary.LittleEndian.Uint32(frames.header[12:])
		if flags&^ipcFrameValidFlags != 0 || reserved2 != 0 {
			return 0, errIPCFrame
		}
		transfer := flags & (ipcFrameHasSocketXfer | ipcFrameXferIsTCP)
		if transfer != 0 && transfer&ipcFrameHasSocketXfer == 0 {
			return 0, errIPCFrame
		}
		if flags&ipcFrameHasData != 0 {
			frames.remaining = length
		} else if length != 0 {
			return 0, errIPCFrame
		}
		if transfer != 0 {
			if _, err := io.CopyN(io.Discard, frames.conn, ipcSocketXferInfoBytes); err != nil {
				return 0, err
			}
		}
	}
	if uint64(len(p)) > uint64(frames.remaining) {
		p = p[:frames.remaining]
	}
	n, err := frames.conn.Read(p)
	frames.remaining -= uint32(n)
	return n, err
}

// Write sends data as one frame; libuv's writer sends an empty one as a header with no data flag.
func (frames *ipcFrames) Write(data []byte) (int, error) {
	if uint64(len(data)) > uint64(^uint32(0)) {
		return 0, errors.New("IPC message too large")
	}
	frame := make([]byte, ipcFrameHeaderBytes+len(data))
	if len(data) > 0 {
		binary.LittleEndian.PutUint32(frame[0:], ipcFrameHasData)
		binary.LittleEndian.PutUint32(frame[8:], uint32(len(data)))
	}
	copy(frame[ipcFrameHeaderBytes:], data)
	frames.write.Lock()
	defer frames.write.Unlock()
	if _, err := frames.conn.Write(frame); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (frames *ipcFrames) Close() error { return frames.conn.Close() }
