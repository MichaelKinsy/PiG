//go:build unix

package tools

// pi: packages/coding-agent/src/core/tools/grep.ts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// grep.ts:194-198: an abort that arrives while ripgrep is running kills the child, and the call fails with "Operation aborted" once the child is gone.
// The stand-in rg records its pid and then sleeps for 30 s; only the kill (exec.CommandContext in runRipgrep) can end it before the 5 s guard.
func TestGrepAbortWhileRipgrepRunsKillsTheChildAndFailsWithOperationAborted(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "rg.pid")
	rg := filepath.Join(dir, "fake-rg.sh")
	script := "#!/bin/sh\necho $$ > '" + pidFile + "'\nexec sleep 30\n"
	if err := os.WriteFile(rg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args, _ := json.Marshal(map[string]any{"pattern": "x"})
	done := make(chan agent.AgentToolResult, 1)
	go func() {
		res, _ := (&GrepTool{CWD: dir, RgPath: rg}).Execute(ctx, "", args, nil)
		done <- res
	}()
	pid := 0
	for deadline := time.Now().Add(5 * time.Second); pid == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the stand-in ripgrep never started")
		}
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
	}
	cancel()
	select {
	case res := <-done:
		if !res.IsError || res.Text() != "Operation aborted" {
			t.Fatalf("result = %+v, want the error Operation aborted", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("grep did not return within 5s of the abort: the ripgrep child was not killed")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("ripgrep child %d is still alive after the call returned (kill -0: %v)", pid, err)
	}
}
