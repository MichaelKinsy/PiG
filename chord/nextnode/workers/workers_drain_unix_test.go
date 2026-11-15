//go:build unix

// SPDX-License-Identifier: MIT

package workers

import (
	"os"
	"sync"
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
