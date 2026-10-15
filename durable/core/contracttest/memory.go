package contracttest

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
)

// MemoryLog appends one resident-memory sample per measured turn to the file the gate names in CONTRACT_MEM, as
// {"turn":N,"rss":BYTES} lines. The gate bounds the growth per turn (durable/contract/lib/memory.mjs): a candidate that
// allocates without bound fails its row with the numbers instead of trapping or being killed later. A nil *MemoryLog
// records nothing.
type MemoryLog struct{ f *os.File }

// OpenMemoryLog opens the log at path; an empty path yields a nil log.
func OpenMemoryLog(path string) (*MemoryLog, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &MemoryLog{f: f}, nil
}

// Sample records the process's resident set after a turn. It returns freed memory to the OS first, so the sample is what
// the run retains rather than what the collector has not yet released.
func (m *MemoryLog) Sample(turn int) {
	if m == nil {
		return
	}
	debug.FreeOSMemory()
	_, _ = fmt.Fprintf(m.f, "{\"turn\":%d,\"rss\":%d}\n", turn, residentBytes())
}

// Close closes the log.
func (m *MemoryLog) Close() {
	if m != nil {
		_ = m.f.Close()
	}
}

// residentBytes reads the resident set from /proc/self/statm and falls back to the Go runtime's memory from the OS.
func residentBytes() uint64 {
	if data, err := os.ReadFile("/proc/self/statm"); err == nil {
		if fields := strings.Fields(string(data)); len(fields) >= 2 {
			if pages, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				return pages * uint64(os.Getpagesize())
			}
		}
	}
	var s runtime.MemStats
	runtime.ReadMemStats(&s)
	return s.Sys
}
