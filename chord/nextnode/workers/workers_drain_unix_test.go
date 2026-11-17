//go:build unix

package workers

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// heldSource is a pipe's read end whose reads tell the test when one found nothing and is about to wait for the stream.
type heldSource struct {
	*os.File
	waits func()
}

func (s *heldSource) SyscallConn() (syscall.RawConn, error) {
	raw, err := s.File.SyscallConn()
	if err != nil {
		return nil, err
	}
	return &heldRaw{RawConn: raw, waits: s.waits}, nil
}

type heldRaw struct {
	syscall.RawConn
	waits func()
}

func (r *heldRaw) Read(read func(descriptor uintptr) bool) error {
	return r.RawConn.Read(func(descriptor uintptr) bool {
		done := read(descriptor)
		if !done {
			r.waits()
		}
		return done
	})
}

// The process ends between a reader's look at its stream and its wait for it: the reader looked while the process lived
// (nothing to read, nothing settled) and is held before it waits; the process ends meanwhile. Its wait must not be for the
// stream, which a holder keeps open: it is woken, looks again and settles, so the exit is told. Without the wake the drain
// settles only at the stream's end.
func TestADrainSettlesWhenItsProcessEndsBetweenItsReadersLookAtItsStreamAndItsWaitForIt(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// The holder: the stream does not end.
	defer func() { _ = write.Close() }()
	told := newDrain()
	looked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	reader := told.reader(&heldSource{File: read, waits: func() {
		once.Do(func() {
			close(looked)
			<-release
		})
	}})
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		buffer := make([]byte, 16)
		for {
			if _, err := reader.Read(buffer); err != nil {
				return
			}
		}
	}()
	<-looked
	told.processEnded()
	select {
	case <-told.settled:
		t.Fatal("settled before its reader looked at the stream after the process's end")
	default:
	}
	close(release)
	until(t, func() bool {
		select {
		case <-told.settled:
			return true
		default:
			return false
		}
	})
	select {
	case <-returned:
		t.Error("the reader returned: it settled by its end, not by a look at the stream")
	default:
	}
	_ = read.Close()
	<-returned
}

// streamPair is a stream a worker process writes, as a drain reads it, and the writer of whoever holds its other end: a pipe
// is a worker's output, a Unix socket its channel.
func streamPair(t testing.TB, kind string) (source drainSource, writer io.WriteCloser) {
	t.Helper()
	switch kind {
	case "pipe":
		read, write, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = read.Close(); _ = write.Close() })
		return read, write
	case "socket":
		fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range fds {
			syscall.CloseOnExec(fd)
			if err := syscall.SetNonblock(fd, true); err != nil {
				t.Fatal(err)
			}
		}
		readFile, writeFile := os.NewFile(uintptr(fds[0]), "read"), os.NewFile(uintptr(fds[1]), "write")
		conn, err := net.FileConn(readFile)
		_ = readFile.Close()
		if err != nil {
			_ = writeFile.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close(); _ = writeFile.Close() })
		return conn.(drainSource), writeFile
	}
	t.Fatalf("no stream of kind %q", kind)
	return nil, nil
}

var drainStreamKinds = []string{"pipe", "socket"}

// drainChunk is how much the reader of these tests takes at a time.
const drainChunk = 100

// A drain owes what its stream held when its process ended, and no more. A process outside the worker's tree that keeps
// writing the stream (its output, its channel) never lets it run empty, and the exit must not wait for that: after the end
// the stream is full at every read, so the drain settles by what the stream held, not by finding it empty. The read that
// began once all of that was told settles it, and goes on to read what the outside process wrote since, told as before.
func TestADrainSettlesOnceItToldWhatItsStreamHeldWhenItsProcessEndedWhileAProcessOutsideKeepsItFull(t *testing.T) {
	for _, kind := range drainStreamKinds {
		for _, held := range []int{0, 1, drainChunk - 1, drainChunk, 2*drainChunk + 50, 10 * drainChunk} {
			t.Run(fmt.Sprintf("%s/%d bytes held at the end", kind, held), func(t *testing.T) {
				source, writer := streamPair(t, kind)
				told := newDrain()
				reader := told.reader(source)
				write := func(letter byte, count int) {
					t.Helper()
					if _, err := writer.Write(bytes.Repeat([]byte{letter}, count)); err != nil {
						t.Fatal(err)
					}
				}
				write('a', held)
				told.processEnded()
				buffer := make([]byte, drainChunk)
				var got []byte
				reads := 0
				for settled := false; !settled; {
					if reads == 100 {
						t.Fatalf("not settled after %d reads and %d bytes told: the stream held %d bytes at its process's end and was written to since, so it was never found empty", reads, len(got), held)
					}
					write('b', drainChunk)
					count, err := reader.Read(buffer)
					if err != nil {
						t.Fatalf("read %d: %v", reads+1, err)
					}
					reads++
					got = append(got, buffer[:count]...)
					select {
					case <-told.settled:
						settled = true
					default:
					}
				}
				if want := (held+drainChunk-1)/drainChunk + 1; reads != want {
					t.Errorf("settled by the read %d, want the read %d: the one after the %d bytes the stream held were told", reads, want, held)
				}
				if len(got) < held || !bytes.Equal(got[:held], bytes.Repeat([]byte{'a'}, held)) {
					t.Fatalf("told %d bytes when settled, want the %d the stream held at the end first", len(got), held)
				}
				if rest := got[held:]; bytes.Count(rest, []byte{'b'}) != len(rest) {
					t.Errorf("what followed what the stream held = %q, want only what the outside process wrote after the end", rest)
				}
				// It reads on: what the outside process writes after the settlement is told, as Node's `data` listeners get it.
				write('b', drainChunk)
				count, err := reader.Read(buffer)
				if err != nil || count == 0 || bytes.Count(buffer[:count], []byte{'b'}) != count {
					t.Errorf("a read after the settlement = %q, %v, want what the outside process wrote", buffer[:count], err)
				}
			})
		}
	}
}

// What the reader took before the process ended is not owed again, and what it took but had not told yet when the process
// ended is told before the drain settles: of the thousand bytes the process wrote, the reader took four hundred, the last
// hundred of them while the process ended; every one is told once, and the drain settles by the read after the other six
// hundred.
func TestADrainDoesNotOweWhatItsReaderTookBeforeItsProcessEnded(t *testing.T) {
	for _, kind := range drainStreamKinds {
		t.Run(kind, func(t *testing.T) {
			source, writer := streamPair(t, kind)
			told := newDrain()
			reader := told.reader(source)
			if _, err := writer.Write(bytes.Repeat([]byte{'a'}, 10*drainChunk)); err != nil {
				t.Fatal(err)
			}
			buffer := make([]byte, drainChunk)
			var got []byte
			for range 3 {
				count, err := reader.Read(buffer)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, buffer[:count]...)
			}
			count, err := reader.Read(buffer)
			if err != nil {
				t.Fatal(err)
			}
			told.processEnded()
			got = append(got, buffer[:count]...)
			reads := 0
			for settled := false; !settled; {
				if reads == 100 {
					t.Fatalf("not settled after %d reads and %d bytes told", reads, len(got))
				}
				if _, err := writer.Write(bytes.Repeat([]byte{'b'}, drainChunk)); err != nil {
					t.Fatal(err)
				}
				count, err := reader.Read(buffer)
				if err != nil {
					t.Fatal(err)
				}
				reads++
				got = append(got, buffer[:count]...)
				select {
				case <-told.settled:
					settled = true
				default:
				}
			}
			if reads != 7 {
				t.Errorf("settled by the read %d after the end, want the 7th", reads)
			}
			if letters := bytes.Count(got, []byte{'a'}); letters != 10*drainChunk || !bytes.Equal(got[:10*drainChunk], bytes.Repeat([]byte{'a'}, 10*drainChunk)) {
				t.Errorf("told %d of the 1000 bytes the process wrote (%d in all), want each once and first", letters, len(got))
			}
		})
	}
}

// The same with the reader running while the process ends and a process outside its tree writing without a pause: when the
// drain settles, everything written before the end was told, and the writer has not stopped (the drain did not wait for the
// stream to end, which on a never-empty stream it would only do when the outside writer's limit ended it).
func TestADrainTellsAllThatWasWrittenBeforeItsProcessEndedWhileItsReaderReadsAndAProcessOutsideWritesWithoutPause(t *testing.T) {
	for _, kind := range drainStreamKinds {
		t.Run(kind, func(t *testing.T) {
			source, writer := streamPair(t, kind)
			told := newDrain()
			reader := told.reader(source)
			const head, limit = 256 << 10, 256 << 20
			var delivered, written, toldAtSettle atomic.Int64
			var writerDone, writerGone atomic.Bool
			headReached, stop, returned, observed := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() {
				defer close(returned)
				var once sync.Once
				// The drain settles at the beginning of a read, once what that read's predecessors gave was told: the first read after
				// which it is settled was the one that settled it, and what was told before it began is what it had told.
				observe := func(toldBefore int64) {
					select {
					case <-told.settled:
						once.Do(func() { toldAtSettle.Store(toldBefore); close(observed) })
					default:
					}
				}
				defer func() { told.settle(); observe(delivered.Load()) }()
				buffer := make([]byte, 4096)
				for {
					before := delivered.Load()
					count, err := reader.Read(buffer)
					delivered.Add(int64(count))
					observe(before)
					if err != nil {
						return
					}
					// Slow: it reads again once the stream holds more, so that the outside writer is ahead of every read.
					for queuedBytes(source) == 0 && !writerGone.Load() {
						runtime.Gosched()
					}
				}
			}()
			go func() {
				defer func() { writerGone.Store(true); _ = writer.Close() }()
				var once sync.Once
				block := make([]byte, 4096)
				for written.Load() < limit {
					select {
					case <-stop:
						return
					default:
					}
					count, err := writer.Write(block)
					if written.Add(int64(count)) >= head {
						once.Do(func() { close(headReached) })
					}
					if err != nil {
						return
					}
				}
				writerDone.Store(true)
			}()
			<-headReached
			mark := written.Load()
			told.processEnded()
			<-told.settled
			<-observed
			if writerDone.Load() {
				t.Errorf("settled only once the writer had written its %d bytes and the stream ended: it waited for the stream, not for what it held at the end", int64(limit))
			}
			if got := toldAtSettle.Load(); got < mark {
				t.Errorf("settled with %d bytes told, %d had been written before the end", got, mark)
			}
			close(stop)
			<-returned
		})
	}
}
