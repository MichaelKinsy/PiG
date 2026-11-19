//go:build parity

package runner

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

type processParent struct {
	pid, parent int
}

// runningProcesses excludes zombies: those have exited and cannot retain scenario resources or spawn work.
func runningProcesses(ctx context.Context) ([]processParent, error) {
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,stat=").Output()
	if err != nil {
		return nil, fmt.Errorf("inspect scenario processes: %w", err)
	}
	var processes []processParent
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("invalid ps record: %q", line)
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr != nil || parentErr != nil {
			return nil, fmt.Errorf("invalid ps identity: %q", line)
		}
		if !strings.HasPrefix(fields[2], "Z") {
			processes = append(processes, processParent{pid, parent})
		}
	}
	return processes, nil
}

// retainDescendants remembers ownership before teardown reparents children to init. Later scans also retain children spawned by a still-running owned process.
func retainDescendants(owned map[int]bool, processes []processParent) []int {
	for changed := true; changed; {
		changed = false
		for _, process := range processes {
			if owned[process.parent] && !owned[process.pid] {
				owned[process.pid] = true
				changed = true
			}
		}
	}
	var live []int
	for _, process := range processes {
		if owned[process.pid] {
			live = append(live, process.pid)
		}
	}
	slices.Sort(live)
	return live
}

func tmuxSessionProcesses(ctx context.Context, session string) (map[int]bool, error) {
	//nolint:gosec // G204 (class a): program is literal tmux; args are fixed list-panes with a harness-generated session.
	out, err := exec.CommandContext(ctx, "tmux", tmuxArgs("list-panes", "-s", "-t", session, "-F", "#{pane_pid}")...).Output()
	if err != nil {
		return nil, fmt.Errorf("inspect panes for %s: %w (%s)", session, err, tmuxServerReport(ctx, session))
	}
	owned := make(map[int]bool)
	for _, value := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid pane pid %q: %w", value, err)
		}
		owned[pid] = true
	}
	processes, err := runningProcesses(ctx)
	if err != nil {
		return nil, err
	}
	retainDescendants(owned, processes)
	return owned, nil
}

// tmuxTeardown ends one tmux run: its session, and whatever the session started that can still write into the run's temp root.
type tmuxTeardown struct {
	session, transcriptPath, scenario, side string
	// started is whether the session exists. readerStarted is whether the transcript reader was started on its pane: the reader is a child of the tmux server, not of a pane, so awaitOwnedProcesses never sees it. It comes from the start path and not from a later look at the pane, because a pane whose pipe finishTranscript closed, or whose reader is still writing its tail, no longer shows a pipe, and a look at the pane would miss exactly the reader that is alive.
	started, readerStarted bool
	// clock paces the wait for the reader's trailer; budget bounds it (the runner's cleanup budget).
	clock  captureClock
	budget time.Duration
}

// end kills the session and returns once nothing the run started can still write into its temp root: the pane's processes have exited (they are only observed, see awaitOwnedProcesses), and, when a reader was started, its trailer exists. The reader writes the trailer as tmux closes the pane's pipe, which the kill does, and the trailer is its last write into the root, so waiting for it needs no signal and leaves nothing behind. A reader that does not finish within the budget is an error that names it; the caller must not remove the root as if it had.
func (td tmuxTeardown) end(ctx context.Context) error {
	var owned map[int]bool
	var inspectErr error
	if td.started {
		owned, inspectErr = tmuxSessionProcesses(ctx, td.session)
	}
	killSession(td.session)
	var exitErr, readerErr error
	if td.started && inspectErr == nil {
		exitErr = awaitOwnedProcesses(ctx, owned)
	}
	if td.readerStarted {
		readerErr = awaitTranscriptDoneOn(ctx, td.clock, td.transcriptPath, td.scenario, td.side, td.budget)
	}
	return errors.Join(inspectErr, exitErr, readerErr)
}

// awaitOwnedProcesses only observes exit. It never sends a stronger signal to conceal a product shutdown failure.
func awaitOwnedProcesses(ctx context.Context, owned map[int]bool) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		processes, err := runningProcesses(ctx)
		if err != nil {
			return err
		}
		live := retainDescendants(owned, processes)
		if len(live) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("scenario left owned processes running: %v: %w", live, ctx.Err())
		case <-ticker.C:
		}
	}
}
