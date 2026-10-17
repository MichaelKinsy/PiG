// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"io"
	"os"
	"testing"
)

// upstream: packages/coding-agent/src/modes/rpc/rpc-mode.ts:54 (runRpcMode(runtimeHost) serves the runtime it is given and nothing else)
// mutation-checked: dropping the missing-state guard makes runRpcMode read a nil options slot and panic
func TestRunRpcModeWithoutItsModeStateServesNothing(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("runRpcMode without RPC mode state on the context panicked: %v", recovered)
		}
	}()
	runRpcMode(context.Background(), nil)
}

// mutation-checked: returning the zero options instead of the stored ones fails it
func TestRPCModeOptionsTravelOnTheContext(t *testing.T) {
	var code int
	ctx := withRPCModeOptions(context.Background(), rpcModeOptions{exitCode: &code})
	got, ok := ctx.Value(rpcModeOptionsKey{}).(rpcModeOptions)
	if !ok || got.exitCode != &code {
		t.Fatalf("the options on the context are %+v (ok=%v), want the ones stored", got, ok)
	}
}

// rpc-mode.ts:54 runRpcMode(runtimeHost) has no way to run without the state the process built for it; Pig reports that on stderr, as the process's diagnostics go, and serves nothing.
func TestRunRpcModeWithoutItsModeStateSaysSoOnStderr(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = write
	runRpcMode(context.Background(), nil)
	os.Stderr = original
	_ = write.Close()
	got, err := io.ReadAll(read)
	_ = read.Close()
	if err != nil {
		t.Fatal(err)
	}
	if want := "pig --rpc: no RPC mode state on the context\n"; string(got) != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
