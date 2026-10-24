//go:build windows

package childwait

import (
	"os"
	"testing"
)

// PeekNamedPipe tells a pipe's bytes and its write ends apart without taking a byte: bytes wait, whether or not a write end is open, and a pipe with
// no write end that has been read empty is broken.
func TestPollPipeReportsTheBytesAndTheWriteEndsOfAPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	check := func(step string, wantHungUp, wantUnread bool) {
		t.Helper()
		hungUp, unread, known := pollPipe(r)
		if !known || hungUp != wantHungUp || unread != wantUnread {
			t.Fatalf("%s: hungUp=%v unread=%v known=%v, want hungUp=%v unread=%v known=true", step, hungUp, unread, known, wantHungUp, wantUnread)
		}
	}
	check("empty, write end open", false, false)
	if _, err := w.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	check("bytes waiting, write end open", false, true)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	check("bytes waiting, write end closed", false, true)
	if _, err := r.Read(make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	check("drained, write end closed", true, false)
}
