//go:build parity && unix

package runner

import (
	"bufio"
	"context"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRetainDescendantsAfterReparenting(t *testing.T) {
	owned := map[int]bool{10: true}
	// Deliberately put the grandchild before its parent to exercise transitive closure.
	before := []processParent{{30, 20}, {40, 1}, {20, 10}, {10, 1}}
	if got := retainDescendants(owned, before); !slices.Equal(got, []int{10, 20, 30}) {
		t.Fatalf("owned before shutdown = %v", got)
	}
	after := []processParent{{30, 1}, {40, 1}, {50, 30}}
	if got := retainDescendants(owned, after); !slices.Equal(got, []int{30, 50}) {
		t.Fatalf("owned after parent exit = %v", got)
	}
	if got := retainDescendants(owned, []processParent{{40, 1}}); len(got) != 0 {
		t.Fatalf("unrelated process claimed: %v", got)
	}
}

func TestAwaitOwnedProcessesReportsLeakWithoutSignalling(t *testing.T) {
	// The shell owns and joins its child once stdin closes. The guard must report
	// both as live, not kill them to manufacture passing cleanup evidence.
	cmd := exec.Command("sh", "-c", `sleep 999 & child=$!; echo "$child"; read release; kill "$child"; wait "$child"`)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		t.Fatalf("child did not start: %v", scanner.Err())
	}
	child, err := strconv.Atoi(scanner.Text())
	if err != nil {
		t.Fatal(err)
	}
	owned := map[int]bool{cmd.Process.Pid: true}
	processes, err := runningProcesses(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := retainDescendants(owned, processes); !slices.Contains(got, child) {
		t.Fatalf("child %d not discovered in %v", child, got)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := awaitOwnedProcesses(ctx, owned); err == nil {
		t.Fatal("accepted running descendants")
	}
	processes, err = runningProcesses(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := retainDescendants(owned, processes); !slices.Contains(got, child) || !slices.Contains(got, cmd.Process.Pid) {
		t.Fatalf("guard killed a process: %v", got)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	if err := awaitOwnedProcesses(ctx, owned); err != nil {
		t.Fatalf("joined descendants retained: %s", strings.TrimSpace(err.Error()))
	}
}
