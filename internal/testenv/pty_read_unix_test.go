//go:build linux || darwin

package testenv

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReadPTYUntilSentinelStripsSentinel(t *testing.T) {
	master, slave, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	finish := ReadPTYUntilSentinel(t, master, slave, 5*time.Second)
	const payload = "hello\x1b[?1049l\x1b[?25h"
	if _, err := slave.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	got := string(finish())
	if got != payload {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

// maxReadPipe caps each Read on an *os.File to max bytes so a test can force
// the sentinel to arrive across multiple Read calls deterministically.
type maxReadPipe struct {
	*os.File
	max int
}

func (m maxReadPipe) Read(p []byte) (int, error) {
	if len(p) > m.max {
		p = p[:m.max]
	}
	return m.File.Read(p)
}

func TestReadPTYUntilSentinelSplitAcrossReads(t *testing.T) {
	master, slave, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Cap each read to 5 bytes so the 22-byte sentinel is split across 5 reads.
	finish, cleanup := startPTYReader(t.Context(), maxReadPipe{File: master, max: 5}, slave, 5*time.Second)
	t.Cleanup(cleanup)
	const payload = "split-payload-bytes"
	if _, err := io.WriteString(slave, payload); err != nil {
		t.Fatal(err)
	}
	got, err := finish()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestReadPTYUntilSentinelFailsWithCollectedBytesWhenSentinelNeverArrives(t *testing.T) {
	master, slave, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Use a separate sink for finish so its sentinel write does not reach master.
	sinkR, sinkW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sinkR.Close(); _ = sinkW.Close(); _ = slave.Close() })

	finish, cleanup := startPTYReader(t.Context(), master, sinkW, 50*time.Millisecond)
	t.Cleanup(cleanup)
	if _, err := slave.WriteString("partial-output"); err != nil {
		t.Fatal(err)
	}
	_, err = finish()
	if err == nil {
		t.Fatal("expected error when sentinel never arrives")
	}
	if !strings.Contains(err.Error(), "pty sentinel did not arrive") || !strings.Contains(err.Error(), "partial-output") {
		t.Fatalf("unexpected error: %v", err)
	}
}
