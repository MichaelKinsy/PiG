//go:build unix

package aotruntime

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func registrationFrame(body string) []byte {
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	return append(frame, body...)
}

// readRegistration returns once the extension's whole first frame has arrived, however the frame is split on the wire, and says what kind of frame it was.
func TestReadRegistrationWaitsForTheWholeFrame(t *testing.T) {
	frame := registrationFrame(`{"type":"register","register":{"name":"probe"}}`)
	for _, cut := range []int{1, 3, 4, 5, len(frame) - 1} {
		host, extension := net.Pipe()
		go func() {
			_, _ = extension.Write(frame[:cut])
			_, _ = extension.Write(frame[cut:])
		}()
		kind, err := readRegistration(host, time.Now().Add(10*time.Second))
		_ = host.Close()
		_ = extension.Close()
		if err != nil || kind != "register" {
			t.Errorf("frame cut at %d: readRegistration = %q, %v; want register", cut, kind, err)
		}
	}
}

func TestReadRegistrationReportsWhatItCannotUse(t *testing.T) {
	for name, tc := range map[string]struct {
		wire    []byte
		wantErr string
	}{
		"closed before the frame ends": {registrationFrame(`{"type":"register"}`)[:6], "EOF"},
		"not JSON":                     {registrationFrame(`register`), "invalid character"},
		"over the frame cap":           {binary.BigEndian.AppendUint32(nil, maxRegistrationFrame+1), "exceeds"},
	} {
		host, extension := net.Pipe()
		go func() {
			_, _ = extension.Write(tc.wire)
			_ = extension.Close()
		}()
		_, err := readRegistration(host, time.Now().Add(10*time.Second))
		_ = host.Close()
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: readRegistration error = %v, want one containing %q", name, err, tc.wantErr)
		}
	}
	host, extension := net.Pipe()
	defer func() { _ = host.Close(); _ = extension.Close() }()
	if _, err := readRegistration(host, time.Now().Add(20*time.Millisecond)); err == nil {
		t.Error("readRegistration of a connection that sends nothing returned no error at its deadline")
	}
}

// TestTrainHostHelper is the host of TestTrainRunEndsOnTheWholeRegistration: a runtime that connects to the training socket and sends its registration in two
// parts, with a pause that a host closing on the first part would cut through.
func TestTrainHostHelper(t *testing.T) {
	result := os.Getenv("AOT_TRAIN_HELPER_RESULT")
	if result == "" {
		t.Skip("runs as the training host of TestTrainRunEndsOnTheWholeRegistration")
	}
	outcome := func() string {
		conn, err := net.Dial("unix", os.Getenv("PIG_EXT_SOCKET"))
		if err != nil {
			return "dial: " + err.Error()
		}
		defer func() { _ = conn.Close() }()
		frame := registrationFrame(`{"type":"register","register":{"name":"probe"}}`)
		if _, err := conn.Write(frame[:6]); err != nil {
			return "first part: " + err.Error()
		}
		// Longer than any wait a host could add after the first part.
		time.Sleep(500 * time.Millisecond)
		if _, err := conn.Write(frame[6:]); err != nil {
			return "second part: " + err.Error()
		}
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
			return "after the frame the host did not close: " + err.Error()
		}
		return "closed after the whole frame"
	}()
	_ = os.WriteFile(result, []byte(outcome), 0o644)
}

// A training run closes the extension's connection when its registration has arrived whole. A host that closed on the first bytes it read would cut the
// registration of a runtime that sends it in parts.
func TestTrainRunEndsOnTheWholeRegistration(t *testing.T) {
	// A short directory: the training socket's path must fit a Unix socket address.
	dir, err := os.MkdirTemp("", "aot")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(dir, "result")
	host := filepath.Join(dir, "host")
	script := "#!/bin/sh\nAOT_TRAIN_HELPER_RESULT='" + result + "' exec '" + self + "' -test.run='^TestTrainHostHelper$'\n"
	if err := os.WriteFile(host, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	if err := trainRun(context.Background(), buildOptions{WorkDir: dir}, host, filepath.Join(dir, "order"), []string{"cli", "ext.mjs"}, 30*time.Second, &log); err != nil {
		t.Fatalf("trainRun = %v\n%s", err, log.String())
	}
	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "closed after the whole frame" {
		t.Fatalf("the runtime saw %q\n%s", got, log.String())
	}
	if log.Len() != 0 {
		t.Fatalf("the run logged %q", log.String())
	}
}
