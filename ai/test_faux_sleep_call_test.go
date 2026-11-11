//go:build !windows

package ai

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The "Run: sleep for a while" call's started marker is the cue at which tests kill the call. A test that kills the
// call's shell with its process group and adopts what is left of it (coding/cli rpc_signal_children_test.go) counts
// every adopted process that did not end by SIGKILL as one that outlived the kill. A child of the shell that creates
// the marker, like touch, may have exited by then without the shell having reaped it: the group kill ends the shell
// first, and the adopting process finds the child as a process that ended with status 0 (CI: "[45807 survived: 0]").
// The shell creates the marker itself, so no child of the call can have exited before the call blocks in sleep.
//
// PATH holds only a sleep that returns at once, so a marker that needs any other command is not created.
func TestTestFauxSleepCallCreatesItsMarkerInTheShellItself(t *testing.T) {
	provider := &TestFauxProvider{}
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Run: sleep for a while")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var command string
	for event := range stream.Events(t.Context()) {
		if end, ok := event.(ToolCallEndEvent); ok && end.ToolCall.Name == "bash" {
			command, _ = end.ToolCall.Arguments["command"].(string)
		}
	}
	if command == "" {
		t.Fatal("the sleep scenario emitted no bash call")
	}

	bin, work := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "sleep"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", command)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + bin}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%q: %v\n%s", command, err, output)
	}
	if _, err := os.Stat(filepath.Join(work, TestFauxToolStartedMarker)); err != nil {
		t.Fatalf("%q did not create %s with the shell alone: %v\n%s", command, TestFauxToolStartedMarker, err, output)
	}
}
