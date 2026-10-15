package host

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// libuv's IPC framing (src/win/pipe.c) and the buffers of the Windows spawn (src/win/process-stdio.c), which have to
// match what Node's libuv writes and reads byte for byte. The cases follow libuv's reader and writer.

func ipcFrame(flags, length, reserved2 uint32, payload ...byte) []byte {
	header := make([]byte, ipcFrameHeaderBytes)
	binary.LittleEndian.PutUint32(header[0:], flags)
	binary.LittleEndian.PutUint32(header[8:], length)
	binary.LittleEndian.PutUint32(header[12:], reserved2)
	return append(header, payload...)
}

type bufferConn struct {
	*bytes.Reader
	written bytes.Buffer
}

func (conn *bufferConn) Write(p []byte) (int, error) { return conn.written.Write(p) }
func (conn *bufferConn) Close() error                { return nil }

func TestIPCFramesWriteOneFramePerMessage(t *testing.T) {
	conn := &bufferConn{Reader: bytes.NewReader(nil)}
	frames := newIPCFrames(conn)
	if n, err := frames.Write([]byte("{}\n")); n != 3 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if want := ipcFrame(ipcFrameHasData, 3, 0, '{', '}', '\n'); !bytes.Equal(conn.written.Bytes(), want) {
		t.Fatalf("frame = % x, want % x", conn.written.Bytes(), want)
	}
	conn.written.Reset()
	if n, err := frames.Write(nil); n != 0 || err != nil {
		t.Fatalf("empty Write = %d, %v", n, err)
	}
	if want := ipcFrame(0, 0, 0); !bytes.Equal(conn.written.Bytes(), want) {
		t.Fatalf("empty frame = % x, want % x", conn.written.Bytes(), want)
	}
}

func TestIPCFramesReadJoinsPayloadsAcrossFramesAndSkipsTransfers(t *testing.T) {
	var stream []byte
	stream = append(stream, ipcFrame(ipcFrameHasData, 4, 0, 'a', 'b', 'c', 'd')...)
	stream = append(stream, ipcFrame(0, 0, 0)...)                                                       // no data, no socket
	stream = append(stream, ipcFrame(ipcFrameHasData, 0, 0)...)                                         // data flag, zero length
	stream = append(stream, ipcFrame(ipcFrameHasSocketXfer|ipcFrameXferIsTCP|ipcFrameHasData, 2, 0)...) // socket xfer info, then data
	stream = append(stream, make([]byte, ipcSocketXferInfoBytes)...)
	stream = append(stream, 'e', 'f')
	stream = append(stream, ipcFrame(ipcFrameHasSocketXfer, 0, 0)...) // a server handle without data
	stream = append(stream, make([]byte, ipcSocketXferInfoBytes)...)
	stream = append(stream, ipcFrame(ipcFrameHasData, 1, 0, 'g')...)
	frames := newIPCFrames(&bufferConn{Reader: bytes.NewReader(stream)})
	got, err := io.ReadAll(io.LimitReader(frames, 1<<20))
	if err != nil || string(got) != "abcdefg" {
		t.Fatalf("payload = %q, %v", got, err)
	}
	// A small reader sees each payload in pieces.
	frames = newIPCFrames(&bufferConn{Reader: bytes.NewReader(stream)})
	var pieces []string
	for {
		piece := make([]byte, 3)
		n, err := frames.Read(piece)
		if n > 0 {
			pieces = append(pieces, string(piece[:n]))
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatal(err)
			}
			break
		}
	}
	if len(pieces) != 4 || pieces[0] != "abc" || pieces[1] != "d" || pieces[2] != "ef" || pieces[3] != "g" {
		t.Fatalf("pieces = %q", pieces)
	}
}

func TestIPCFramesRejectWhatLibuvRejects(t *testing.T) {
	for name, stream := range map[string][]byte{
		"unknown flag":              ipcFrame(0x08, 0, 0),
		"reserved2 set":             ipcFrame(ipcFrameHasData, 1, 1, 'x'),
		"length without data flag":  ipcFrame(0, 1, 0, 'x'),
		"tcp flag without transfer": ipcFrame(ipcFrameXferIsTCP, 0, 0),
	} {
		frames := newIPCFrames(&bufferConn{Reader: bytes.NewReader(stream)})
		if _, err := frames.Read(make([]byte, 8)); !errors.Is(err, errIPCFrame) {
			t.Errorf("%s: err = %v, want errIPCFrame", name, err)
		}
	}
	// reserved1 is ignored.
	stream := ipcFrame(ipcFrameHasData, 1, 0, 'x')
	binary.LittleEndian.PutUint32(stream[4:], 0xdeadbeef)
	frames := newIPCFrames(&bufferConn{Reader: bytes.NewReader(stream)})
	if n, err := frames.Read(make([]byte, 8)); n != 1 || err != nil {
		t.Errorf("reserved1: %d, %v", n, err)
	}
	// A header cut short is the end of the stream.
	frames = newIPCFrames(&bufferConn{Reader: bytes.NewReader(stream[:5])})
	if _, err := frames.Read(make([]byte, 8)); !errors.Is(err, io.EOF) {
		t.Errorf("short header: err = %v, want EOF", err)
	}
}

// The channel Node's parent sees: messages written by the Go child are frames of JSON text and a newline, and the
// child reads the parent's frames as JSON lines (also when one message is cut over two frames).
func TestNodeChannelOverIPCFrames(t *testing.T) {
	parent, child := net.Pipe()
	defer func() { _ = parent.Close() }()
	channel := newNodeChannel(newIPCFrames(child))
	received := make(chan any, 2)
	listener := IpcListener(func(message any) { received <- message })
	channel.On("message", &listener)

	// Parent to child: a message split across frames, and an internal Node message that stays off the channel.
	go func() {
		for _, part := range []string{`{"cmd":"NO`, `DE_x"}` + "\n" + "{", `"a":1}` + "\n"} {
			_, _ = parent.Write(ipcFrame(ipcFrameHasData, uint32(len(part)), 0, []byte(part)...))
		}
	}()
	select {
	case message := <-received:
		object, ok := message.(*delta.JsonObject)
		if !ok || object.Value("a") != float64(1) {
			t.Fatalf("message = %#v", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no message")
	}

	// Child to parent.
	done := make(chan []byte, 1)
	go func() {
		header := make([]byte, ipcFrameHeaderBytes)
		_, _ = io.ReadFull(parent, header)
		payload := make([]byte, binary.LittleEndian.Uint32(header[8:]))
		_, _ = io.ReadFull(parent, payload)
		done <- append(header, payload...)
	}()
	if !channel.Send(map[string]any{"b": 2}, nil) {
		t.Fatal("Send failed")
	}
	select {
	case frame := <-done:
		if want := ipcFrame(ipcFrameHasData, 8, 0, []byte(`{"b":2}`+"\n")...); !bytes.Equal(frame, want) {
			t.Fatalf("frame = %q, want %q", frame, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no frame")
	}
}

func TestChildStdioIsLibuvsDescriptorTable(t *testing.T) {
	table := childStdio([]byte{crtOpen | crtDevice, crtOpen | crtPipe, 0x41, crtOpen | crtPipe}, []uintptr{0x10, 0x20, 0x30, 0x1234})
	pointer := 4
	if ^uintptr(0)>>32 != 0 {
		pointer = 8
	}
	if len(table) != 4+4+pointer*4 || binary.LittleEndian.Uint32(table) != 4 || !bytes.Equal(table[4:8], []byte{0x41, 0x09, 0x41, 0x09}) {
		t.Fatalf("table = % x", table)
	}
	last := table[8+pointer*3:]
	if binary.LittleEndian.Uint32(last) != 0x1234 {
		t.Fatalf("fd 3 handle = % x", last)
	}
}

func TestEnvironmentBlockIsSortedAndLaterEntriesReplace(t *testing.T) {
	block, err := environmentBlock([]string{"b=1", "=C:=C:\\x", "A=2", "B=3", "noequals", "z="})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	start := 0
	for i, unit := range block {
		if unit == 0 {
			if i == start {
				break
			}
			got = append(got, string(utf16ToRunes(block[start:i])))
			start = i + 1
		}
	}
	want := []string{"=C:=C:\\x", "A=2", "B=3", "z="}
	if len(got) != len(want) {
		t.Fatalf("block = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("block = %q, want %q", got, want)
		}
	}
	if block[len(block)-1] != 0 || block[len(block)-2] != 0 {
		t.Fatalf("block is not ended by two NULs: %v", block[len(block)-3:])
	}
	if _, err := environmentBlock([]string{"a=\x00"}); err == nil {
		t.Fatal("NUL accepted")
	}
	if empty, _ := environmentBlock(nil); len(empty) != 2 {
		t.Fatalf("empty block = %v", empty)
	}
}

func utf16ToRunes(units []uint16) []rune { return []rune(string(utf16.Decode(units))) }
