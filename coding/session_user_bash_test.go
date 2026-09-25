package coding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newBashTestSession(t *testing.T, settingsJSON string) *Session {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("PIG_HOME", tmp)
	agentDir := filepath.Join(tmp, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settingsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	svcs, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// TOOL-15: user bash applies shellCommandPrefix like upstream executeBash,
// and records the command as typed.
func TestExecuteBashAppliesShellCommandPrefix(t *testing.T) {
	sess := newBashTestSession(t, `{"shellCommandPrefix":"X=1"}`)
	result, err := sess.ExecuteBash(context.Background(), "echo $X", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Output) != "1" {
		t.Fatalf("output = %q, want the prefix's variable", result.Output)
	}
}

// TOOL-15: a second user bash runs alongside the first instead of cancelling
// it, and AbortBash stops every run.
func TestExecuteBashRunsConcurrentlyAndAbortStopsAll(t *testing.T) {
	sess := newBashTestSession(t, `{}`)
	type outcome struct {
		result BashResult
		err    error
	}
	slow := make(chan outcome, 1)
	go func() {
		r, err := sess.ExecuteBash(context.Background(), "sleep 0.5; echo first", false)
		slow <- outcome{r, err}
	}()
	time.Sleep(100 * time.Millisecond)
	if r, err := sess.ExecuteBash(context.Background(), "echo second", false); err != nil || strings.TrimSpace(r.Output) != "second" {
		t.Fatalf("second = %+v, %v", r, err)
	}
	first := <-slow
	if first.err != nil || first.result.Cancelled || strings.TrimSpace(first.result.Output) != "first" {
		t.Fatalf("first run was disturbed: %+v", first)
	}

	done := make(chan BashResult, 2)
	for range 2 {
		go func() {
			r, _ := sess.ExecuteBash(context.Background(), "sleep 30", false)
			done <- r
		}()
	}
	time.Sleep(300 * time.Millisecond)
	sess.AbortBash()
	for range 2 {
		select {
		case r := <-done:
			if !r.Cancelled {
				t.Fatalf("run not cancelled: %+v", r)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("AbortBash did not stop every run")
		}
	}
}
