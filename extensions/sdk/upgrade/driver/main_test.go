package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// fakeGoEnv, when set to "hang", makes this test binary act as a go command that waits in a long build, so the test needs no shell script and runs on every OS.
const fakeGoEnv = "PIG_TEST_FAKE_GO"

func init() {
	if os.Getenv(fakeGoEnv) == "hang" {
		time.Sleep(time.Minute)
		os.Exit(3)
	}
}

// The caller keeps standard input open while the analysis runs. Closing it cancels the analysis, so the go command the driver started stops with it.
func TestRunStopsWhenStandardInputCloses(t *testing.T) {
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeGoEnv, "hang")
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
