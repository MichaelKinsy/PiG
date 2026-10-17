//go:build unix || windows

package host

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Node's IPC channel from the child's side. manager.test.ts forks a Node child, whose `process` is the channel; a Go
// child of Node reaches it through ProcessChannel, which on Windows adopts the pipe behind NODE_CHANNEL_FD (a CRT file
// descriptor) and speaks libuv's IPC frames. testdata/process_channel_parent.mjs is the Node parent.

const processChannelChildEnv = "CHORD_PROCESS_CHANNEL_CHILD"

// TestProcessChannelChild is the Go child of the Node parent; it only runs as that.
func TestProcessChannelChild(t *testing.T) {
	if os.Getenv(processChannelChildEnv) != "1" {
		t.Skip("child process of TestProcessChannelIsNodesIPCChannel")
	}
	os.Exit(runProcessChannelChild())
}

func runProcessChannelChild() int {
	channel := ProcessChannel()
	if !channel.Connected() {
		return 2
	}
	big := IpcListener(func(message any) {
		object, ok := message.(*delta.JsonObject)
		if !ok || object == nil || !object.Has("big") {
			return
		}
		text, _ := object.Value("big").(string)
		channel.Send(map[string]any{"bigLength": len(text)}, nil)
	})
	channel.On("message", &big)
	control := ChildControl(channel)
	init, err := control.Init(context.Background())
	if err != nil {
		return 3
	}
	greeting, _ := init.(*chordjson.Object).Value("greeting").(string)
	channel.Send(map[string]any{"greeted": greeting}, nil)
	done := make(chan struct{})
	control.OnShutdown(func() { close(done) })
	control.Ready()
	select {
	case <-done:
		return 0
	case <-time.After(30 * time.Second):
		return 4
	}
}

func TestProcessChannelIsNodesIPCChannel(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	parent, err := filepath.Abs(filepath.Join("testdata", "process_channel_parent.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, parent, executable, "-test.run=^TestProcessChannelChild$")
	command.Env = append(os.Environ(), processChannelChildEnv+"=1")
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("node parent: %v", err)
	}
	var result struct {
		Received []map[string]any `json:"received"`
		Code     *int             `json:"code"`
		Signal   *string          `json:"signal"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("parent output %q: %v", output, err)
	}
	if result.Code == nil || *result.Code != 0 || result.Signal != nil {
		t.Fatalf("child exit = code %v, signal %v; received %v", result.Code, result.Signal, result.Received)
	}
	want := []string{`{"greeted":"hi"}`, `{"chord2control":{"type":"ready"}}`, `{"bigLength":300000}`}
	if len(result.Received) != len(want) {
		t.Fatalf("received %v, want %v", result.Received, want)
	}
	for i, message := range result.Received {
		got, _ := json.Marshal(message)
		if string(got) != want[i] {
			t.Fatalf("message %d = %s, want %s", i, got, want[i])
		}
	}
}
