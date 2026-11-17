//go:build linux

package main

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// processStart is the start time of process pid in clock ticks since boot (field 22 of /proc/<pid>/stat), which a reused pid does not
// share with the process that held it before. A zombie is gone.
func processStart(pid int) (string, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return "", errProcessGone
	}
	if err != nil {
		return "", err
	}
	// The name in field 2 may hold spaces and parentheses: the fields that follow start after its last ')'.
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", errors.New("unreadable /proc stat")
	}
	f := strings.Fields(s[i+1:]) // f[0] is field 3, the state
	if len(f) < 20 {
		return "", errors.New("short /proc stat")
	}
	if f[0] == "Z" || f[0] == "X" {
		return "", errProcessGone
	}
	return f[19], nil
}

// processScope names the pid space and the boot a pid belongs to: the same number in another pid namespace (a sibling container sharing
// the temporary directory) or after a reboot is another process, which this process cannot look up.
func processScope() string {
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	ns, _ := os.Readlink("/proc/self/ns/pid")
	return strings.TrimSpace(string(boot)) + " " + ns
}
