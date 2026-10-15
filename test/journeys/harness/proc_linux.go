//go:build linux

package harness

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcTreeRSS returns the resident set (bytes) of pid and all its descendants, and the main process's high-water mark.
func ProcTreeRSS(pid int) (total, mainHWM int64) {
	parent := map[int][]int{}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		s := string(b)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+2:])
		if len(f) < 2 {
			continue
		}
		pp, _ := strconv.Atoi(f[1])
		parent[pp] = append(parent[pp], n)
	}
	var walk func(int)
	walk = func(p int) {
		total += statusKB(p, "VmRSS:") * 1024
		for _, c := range parent[p] {
			walk(c)
		}
	}
	walk(pid)
	mainHWM = statusKB(pid, "VmHWM:") * 1024
	return total, mainHWM
}

func statusKB(pid int, key string) int64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0
	}
	for l := range strings.SplitSeq(string(b), "\n") {
		if strings.HasPrefix(l, key) {
			f := strings.Fields(l)
			if len(f) >= 2 {
				v, _ := strconv.ParseInt(f[1], 10, 64)
				return v
			}
		}
	}
	return 0
}

// findMarked returns the PIDs whose environment contains marker (KEY=VALUE).
func findMarked(marker string) []int {
	var out []int
	entries, _ := os.ReadDir("/proc")
	self := os.Getpid()
	for _, e := range entries {
		n, err := strconv.Atoi(e.Name())
		if err != nil || n == self {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil {
			continue
		}
		for kv := range strings.SplitSeq(string(b), "\x00") {
			if kv == marker {
				if st, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat")); err == nil {
					if i := strings.LastIndexByte(string(st), ')'); i > 0 && strings.HasPrefix(string(st)[i+2:], "Z") {
						break // a zombie is already dead
					}
				}
				out = append(out, n)
				break
			}
		}
	}
	return out
}

func procCmdline(pid int) string {
	b, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
}
