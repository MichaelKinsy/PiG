// SPDX-License-Identifier: MIT

//go:build linux

package perfgate

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// ProcessMemory returns the resident and proportional set sizes of process pid in bytes, from /proc/<pid>/smaps_rollup.
func ProcessMemory(t testing.TB, pid int) (rss, pss float64) {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
	if err != nil {
		t.Fatal(err)
	}
	//portlint:allow pathseparators the kernel writes /proc/<pid>/smaps_rollup with \n line ends only; Go-only memory gate, no Pi counterpart
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[2] != "kB" {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			t.Fatalf("smaps_rollup %q: %v", line, err)
		}
		switch fields[0] {
		case "Rss:":
			rss = value * 1024
		case "Pss:":
			pss = value * 1024
		}
	}
	if rss == 0 || pss == 0 {
		t.Fatalf("smaps_rollup of %d has no Rss or Pss:\n%s", pid, data)
	}
	return rss, pss
}

// Descendant returns the first process named comm below pid, searching children breadth-first through /proc/<pid>/task/*/children, or 0.
func Descendant(pid int, comm string) int {
	queue := []int{pid}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		tasks, _ := os.ReadDir(fmt.Sprintf("/proc/%d/task", current))
		for _, task := range tasks {
			data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", current, task.Name()))
			if err != nil {
				continue
			}
			for field := range strings.FieldsSeq(string(data)) {
				child, err := strconv.Atoi(field)
				if err != nil {
					continue
				}
				name, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", child))
				if strings.TrimSpace(string(name)) == comm {
					return child
				}
				queue = append(queue, child)
			}
		}
	}
	return 0
}
