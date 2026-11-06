//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// The caller keeps standard input open while the analysis runs. Closing it cancels the analysis, so the go command the driver started stops with it.
func TestRunStopsWhenStandardInputCloses(t *testing.T) {
	command := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexec sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(upgrade.LoadOptions{Dir: t.TempDir(), Command: command})
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- run(reader, &out) }()
	if _, err := writer.Write(request); err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(200*time.Millisecond, func() { _ = writer.Close() })
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the driver kept running after standard input closed")
	}
	var reply upgrade.Reply
	if err := json.Unmarshal(out.Bytes(), &reply); err != nil {
		t.Fatalf("reply %q: %v", out.String(), err)
	}
	if reply.Result != nil || reply.Error != "context canceled" {
		t.Fatalf("reply = %+v", reply)
	}
}
