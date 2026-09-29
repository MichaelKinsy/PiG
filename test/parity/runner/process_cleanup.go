//go:build parity

package runner

import (
	"context"
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
	out, err := exec.CommandContext(ctx, "tmux", tmuxArgs("list-panes", "-s", "-t", session, "-F", "#{pane_pid}")...).Output()
	if err != nil {
		return nil, fmt.Errorf("inspect panes for %s: %w", session, err)
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
