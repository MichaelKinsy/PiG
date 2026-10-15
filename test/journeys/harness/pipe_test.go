//go:build unix

package harness

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A descendant that inherited the program's standard output keeps the pipe open after the program dies. Close must still return, so the run
// reaches its leak sweep instead of hanging outside every hang detector.
func TestPipeCloseReturnsWhileADescendantHoldsStdout(t *testing.T) {
	p, err := StartPipe(PipeOpts{Dir: t.TempDir(), Env: []string{"PATH=" + os.Getenv("PATH")}, Argv: []string{"sh", "-c", "sleep 60 & echo child $!; exec sleep 60"}})
	if err != nil {
		t.Fatal(err)
	}
	var child int
	deadline := time.Now().Add(10 * time.Second)
	for child == 0 && time.Now().Before(deadline) {
		ch := p.Changed()
		screen, _ := p.Screen()
		if pid, ok := strings.CutPrefix(strings.TrimSpace(screen), "child "); ok {
			child, _ = strconv.Atoi(pid)
			break
		}
		select {
		case <-ch:
		case <-time.After(time.Until(deadline)):
		}
	}
	if child == 0 {
		t.Fatal("the program did not report its background child")
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not return while a descendant held standard output")
	}
}
