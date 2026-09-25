package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A background child that inherits stdout must not turn the call into an
// error or discard the output: upstream waitForChildProcess resolves once the
// pipe has been idle for EXIT_STDIO_GRACE_MS after the shell exits.
func TestBashTool_BackgroundChildKeepsResult(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	args, _ := json.Marshal(bashParams{Command: "echo hi; sleep 30 &"})
	start := time.Now()
	res, err := bt.Execute(context.Background(), "", args, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError || strings.TrimSpace(res.Content) != "hi" {
		t.Fatalf("res = %+v, want success with output hi", res)
	}
	if elapsed > 15*time.Second {
		t.Fatalf("call took %v; it must not wait for the background child", elapsed)
	}
}

// User bash (session ExecuteBash path) records the result instead of failing.
func TestExecuteBash_BackgroundChildReturnsExitCode(t *testing.T) {
	sh, err := defaultShellConfig()
	if err != nil {
		t.Fatal(err)
	}
	res, err := ExecuteBash(context.Background(), "echo hi; sleep 5 &", t.TempDir(), sh, BashExecOptions{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if res.ExitCode == nil || *res.ExitCode != 0 || strings.TrimSpace(res.Output) != "hi" {
		t.Fatalf("res = %+v", res)
	}
}
