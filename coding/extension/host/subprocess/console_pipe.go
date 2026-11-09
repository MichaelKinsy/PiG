package subprocess

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strconv"
	"sync"
	"time"
)

// consolePipe is the output pipe of a packed process that stays alive after one of its members failed to load. A runner writes the failed member's cause to stderr and then closes that member's socket, while the host reads the cause from the log the moment the socket closes. A copy through a pipe the exec package owns has no point at which the host knows the process's earlier writes have reached the log, so the host keeps the write end itself: flush writes a marker no extension can guess behind everything the process wrote, and returns once the copy reached it. Pipe writes are ordered, and a marker of at most PIPE_BUF bytes is written atomically, so everything the process wrote before the socket closed is in the sink when flush returns.
//
// The copy reads into one fixed buffer and writes through, so a slow terminal slows only the extension that writes (the kernel pipe is the bound), and nothing is held in the host.
type consolePipe struct {
	read   *os.File
	write  *os.File
	sink   io.Writer
	prefix []byte
	done   chan struct{}

	mu     sync.Mutex
	closed bool
	seq    uint64
	acks   map[uint64]chan struct{}
}

const (
	consoleMarkerSeqDigits = 16
	// consoleFlushWait bounds how long flush waits for the copy: the log it protects is read for a failure diagnostic, never for extension work.
	consoleFlushWait = stderrDrainWait
	// pig divergence (D56): the host-side copy of an extension's output pipe reads into one fixed buffer, so a stalled terminal slows only the extension that writes.
	consoleBufferSize = 32 << 10
)

func newConsolePipe(sink io.Writer) (*consolePipe, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		_ = read.Close()
		_ = write.Close()
		return nil, err
	}
	done := make(chan struct{})
	c := &consolePipe{
		read:   read,
		write:  write,
		sink:   sink,
		prefix: append([]byte("\x00pig-console-flush:"), hex.EncodeToString(nonce[:])...),
		done:   done,
		acks:   make(map[uint64]chan struct{}),
	}
	//portlint:allow golifetime the copy ends when the process's write ends and the host's close (finish) end the pipe, and finish waits for it
	go func() {
		defer close(done)
		c.forward()
	}()
	return c, nil
}

func (c *consolePipe) markerLen() int { return len(c.prefix) + 1 + consoleMarkerSeqDigits + 1 }

func (c *consolePipe) marker(seq uint64) []byte {
	digits := strconv.FormatUint(seq, 16)
	out := make([]byte, 0, c.markerLen())
	out = append(out, c.prefix...)
	out = append(out, ':')
	for range consoleMarkerSeqDigits - len(digits) {
		out = append(out, '0')
	}
	out = append(out, digits...)
	return append(out, '\n')
}

// forward forwards the pipe to the sink, removing flush markers and acknowledging them. It never stops on a sink error: a sink drops its own failed writes.
func (c *consolePipe) forward() {
	buf := make([]byte, consoleBufferSize+c.markerLen())
	held := 0
	for {
		n, err := c.read.Read(buf[held:])
		data := buf[:held+n]
		held = 0
		for len(data) > 0 {
			i := bytes.Index(data, c.prefix)
			if i < 0 {
				keep := 0
				if err == nil {
					keep = partialPrefixSuffix(data, c.prefix)
				}
				_, _ = c.sink.Write(data[:len(data)-keep])
				held = copy(buf, data[len(data)-keep:])
				break
			}
			if len(data)-i < c.markerLen() {
				if err != nil {
					// The stream ended inside a marker: it was the process's own bytes.
					_, _ = c.sink.Write(data)
					break
				}
				if i > 0 {
					_, _ = c.sink.Write(data[:i])
				}
				held = copy(buf, data[i:])
				break
			}
			if i > 0 {
				_, _ = c.sink.Write(data[:i])
			}
			seqText := data[len(c.prefix)+i+1 : len(c.prefix)+i+1+consoleMarkerSeqDigits]
			if seq, parseErr := strconv.ParseUint(string(seqText), 16, 64); parseErr == nil {
				c.ack(seq)
			}
			data = data[i+c.markerLen():]
		}
		if err != nil {
			return
		}
	}
}

// partialPrefixSuffix is the length of the longest suffix of data that is a proper prefix of prefix: bytes a later read may complete into a marker.
func partialPrefixSuffix(data, prefix []byte) int {
	for k := min(len(data), len(prefix)-1); k > 0; k-- {
		if bytes.HasPrefix(prefix, data[len(data)-k:]) {
			return k
		}
	}
	return 0
}

func (c *consolePipe) ack(seq uint64) {
	c.mu.Lock()
	ch := c.acks[seq]
	delete(c.acks, seq)
	c.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

// flush returns once everything the process wrote before the call is in the sink, after consoleFlushWait, or when the pipe has ended. A nil pipe has nothing to wait for. After finish has closed the write end no marker can follow, but the copy may still be forwarding what the process wrote before it exited: flush then waits for the copy to end, as it does for a marker it cannot write.
func (c *consolePipe) flush() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		// A runner that fails its only member exits right after it wrote the cause and closed the member's socket, so the reaper's finish and the host's read of the cause can run in either order.
		timer := time.NewTimer(consoleFlushWait)
		defer timer.Stop()
		select {
		case <-c.done:
		case <-timer.C:
		}
		return
	}
	c.seq++
	seq := c.seq
	ack := make(chan struct{})
	c.acks[seq] = ack
	write := c.write
	c.mu.Unlock()
	// Handing the marker to the pipe can block: the write end is a blocking descriptor (os/exec's Fd makes it one), so a full pipe, which a stalled sink leaves behind, holds the write
	// and no deadline interrupts it. The write runs off this call, outside c.mu, so the wait below is bounded by consoleFlushWait and finish is never held up behind it. finish closes the
	// write end, which ends the write.
	//portlint:allow golifetime the write ends when the pipe drains or finish closes the write end, and flush does not wait for it
	go func() { _, _ = write.Write(c.marker(seq)) }()
	timer := time.NewTimer(consoleFlushWait)
	defer timer.Stop()
	select {
	case <-ack:
	case <-c.done:
	case <-timer.C:
		c.mu.Lock()
		delete(c.acks, seq)
		c.mu.Unlock()
	}
}

// finish ends the host's write end after the process exited and waits, at most grace, for the copy to drain what the process wrote. A descendant that kept the pipe open past the grace loses the rest, as exec's WaitDelay does. A nil pipe has nothing to finish.
func (c *consolePipe) finish(grace time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	first := !c.closed
	c.closed = true
	c.mu.Unlock()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	if first {
		// Closing a descriptor that a blocked marker write still uses waits for that write, so it runs beside the drain and the grace bounds both.
		closed := make(chan struct{})
		//portlint:allow golifetime the close returns once the pipe drains or the read end closes below
		go func() { _ = c.write.Close(); close(closed) }()
		defer func() {
			select {
			case <-closed:
			case <-timer.C:
			}
		}()
	}
	select {
	case <-c.done:
	case <-timer.C:
		_ = c.read.Close()
		<-c.done
	}
	_ = c.read.Close()
}
