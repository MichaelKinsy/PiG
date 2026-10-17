package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// system reads the kernel's view of the pod: /proc and /sys under root ("/" in a pod, a fake tree in tests).
type system struct{ root string }

func (s system) read(path string) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.root, path))
	return strings.TrimSpace(string(data)), err
}

// cpuStat is the cgroup's CPU accounting. cgroup v1 reports throttled time in nanoseconds; it is converted.
type cpuStat struct {
	UsageUsec     int64 `json:"usageUsec"`
	NrPeriods     int64 `json:"nrPeriods"`
	NrThrottled   int64 `json:"nrThrottled"`
	ThrottledUsec int64 `json:"throttledUsec"`
	// HaveUsage is false when the cgroup's CPU usage was unreadable; UsageUsec then measures nothing.
	HaveUsage bool `json:"-"`
}

func (a cpuStat) minus(b cpuStat) cpuStat {
	return cpuStat{UsageUsec: a.UsageUsec - b.UsageUsec, NrPeriods: a.NrPeriods - b.NrPeriods, NrThrottled: a.NrThrottled - b.NrThrottled, ThrottledUsec: a.ThrottledUsec - b.ThrottledUsec, HaveUsage: a.HaveUsage && b.HaveUsage}
}

// cgroupVersion is 2 when the unified hierarchy is mounted, 1 when the v1 cpu controller is, 0 when neither is readable.
func (s system) cgroupVersion() int {
	if _, err := os.Stat(filepath.Join(s.root, "sys/fs/cgroup/cgroup.controllers")); err == nil {
		return 2
	}
	for _, dir := range []string{"sys/fs/cgroup/cpu,cpuacct", "sys/fs/cgroup/cpu"} {
		if _, err := os.Stat(filepath.Join(s.root, dir, "cpu.stat")); err == nil {
			return 1
		}
	}
	return 0
}

func (s system) v1Dir() string {
	for _, dir := range []string{"sys/fs/cgroup/cpu,cpuacct", "sys/fs/cgroup/cpu"} {
		if _, err := os.Stat(filepath.Join(s.root, dir, "cpu.stat")); err == nil {
			return dir
		}
	}
	return "sys/fs/cgroup/cpu"
}

func parseKeyValues(text string) map[string]int64 {
	out := map[string]int64{}
	for line := range strings.Lines(text) {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if n, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			out[fields[0]] = n
		}
	}
	return out
}

// cpuStat reads the cgroup's cpu.stat. ok is false when the pod cannot read it.
func (s system) cpuStat() (cpuStat, bool) {
	switch s.cgroupVersion() {
	case 2:
		text, err := s.read("sys/fs/cgroup/cpu.stat")
		if err != nil {
			return cpuStat{}, false
		}
		kv := parseKeyValues(text)
		usage, haveUsage := kv["usage_usec"]
		return cpuStat{UsageUsec: usage, NrPeriods: kv["nr_periods"], NrThrottled: kv["nr_throttled"], ThrottledUsec: kv["throttled_usec"], HaveUsage: haveUsage}, true
	case 1:
		dir := s.v1Dir()
		text, err := s.read(dir + "/cpu.stat")
		if err != nil {
			return cpuStat{}, false
		}
		kv := parseKeyValues(text)
		stat := cpuStat{NrPeriods: kv["nr_periods"], NrThrottled: kv["nr_throttled"], ThrottledUsec: kv["throttled_time"] / 1000}
		// cpuacct is mounted with cpu ("cpu,cpuacct") or as its own hierarchy.
		for _, usageDir := range []string{dir, "sys/fs/cgroup/cpuacct"} {
			usage, err := s.read(usageDir + "/cpuacct.usage")
			if err != nil {
				continue
			}
			if ns, err := strconv.ParseInt(usage, 10, 64); err == nil {
				stat.UsageUsec, stat.HaveUsage = ns/1000, true
				break
			}
		}
		return stat, true
	}
	return cpuStat{}, false
}

// cpuMax is the CFS quota as "quota period" in microseconds ("max period" when unlimited), as cgroup v2 writes it.
func (s system) cpuMax() string {
	if s.cgroupVersion() == 2 {
		v, _ := s.read("sys/fs/cgroup/cpu.max")
		return v
	}
	dir := s.v1Dir()
	quota, err1 := s.read(dir + "/cpu.cfs_quota_us")
	period, err2 := s.read(dir + "/cpu.cfs_period_us")
	if err1 != nil || err2 != nil {
		return ""
	}
	if quota == "-1" {
		quota = "max"
	}
	return quota + " " + period
}

func (s system) memoryMax() string {
	if s.cgroupVersion() == 2 {
		v, _ := s.read("sys/fs/cgroup/memory.max")
		return v
	}
	v, _ := s.read("sys/fs/cgroup/memory/memory.limit_in_bytes")
	return v
}

// cpusAllowed is the process's affinity mask (Cpus_allowed_list of /proc/self/status).
func (s system) cpusAllowed() ([]int, error) {
	text, err := s.read("proc/self/status")
	if err != nil {
		return nil, err
	}
	for line := range strings.Lines(text) {
		if name, value, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && name == "Cpus_allowed_list" {
			return parseCPUList(strings.TrimSpace(value))
		}
	}
	return nil, fmt.Errorf("no Cpus_allowed_list in /proc/self/status")
}

// cpuModel is the first "model name" of /proc/cpuinfo; on CPUs without one, the implementer and part.
func (s system) cpuModel() string {
	text, err := s.read("proc/cpuinfo")
	if err != nil {
		return ""
	}
	var implementer, part string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		name, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		switch name {
		case "model name":
			return value
		case "CPU implementer":
			implementer = value
		case "CPU part":
			part = value
		}
	}
	if implementer != "" {
		return "implementer " + implementer + " part " + part
	}
	return ""
}

// vectorFeatures are the CPU features that pick a Go runtime's vector code (the Green Tea collector's AVX-512 scan path,
// memmove, hashing): two nodes whose cores differ in them run a Go program's CPU time on different code. The names are
// Linux's /proc/cpuinfo names (avx512_vbmi2 and avx512_bitalg carry an underscore, avx512vbmi does not). The Green Tea
// scan path needs avx512vl, avx512bw, gfni, avx512_bitalg, avx512dq, avx512vbmi and popcnt (internal/runtime/gc/scan).
var vectorFeatures = []string{"avx", "avx2", "avx512f", "avx512bw", "avx512cd", "avx512dq", "avx512vl", "avx512vbmi", "avx512_vbmi2", "avx512_bitalg", "gfni", "bmi2", "popcnt", "asimd", "sve", "sve2"}

// cpuFeatures lists the vector features of the first core in /proc/cpuinfo (x86 "flags", arm64 "Features"), in
// vectorFeatures' order.
func (s system) cpuFeatures() []string {
	text, err := s.read("proc/cpuinfo")
	if err != nil {
		return nil
	}
	for line := range strings.Lines(text) {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); name != "flags" && name != "Features" {
			continue
		}
		have := strings.Fields(value)
		var out []string
		for _, f := range vectorFeatures {
			if slices.Contains(have, f) {
				out = append(out, f)
			}
		}
		return out
	}
	return nil
}

// idleTicks reads /proc/stat's per-CPU idle and total ticks.
func (s system) idleTicks() (map[int][2]int64, error) {
	text, err := s.read("proc/stat")
	if err != nil {
		return nil, err
	}
	out := map[int][2]int64{}
	for line := range strings.Lines(text) {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.HasPrefix(fields[0], "cpu") || fields[0] == "cpu" {
			continue
		}
		id, err := strconv.Atoi(strings.TrimPrefix(fields[0], "cpu"))
		if err != nil {
			continue
		}
		var total, idle int64
		for i, f := range fields[1:] {
			n, _ := strconv.ParseInt(f, 10, 64)
			total += n
			if i == 3 || i == 4 { // idle and iowait
				idle += n
			}
		}
		out[id] = [2]int64{idle, total}
	}
	return out, nil
}

// governor is a CPU's cpufreq governor: "" when the CPU has no cpufreq governor, and governorUnreadable when the file
// exists but cannot be read, so whether the clock scales is unknown.
func (s system) governor(cpu int) string {
	v, err := s.read(fmt.Sprintf("sys/devices/system/cpu/cpu%d/cpufreq/scaling_governor", cpu))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ""
	case err != nil || v == "":
		return governorUnreadable
	}
	return v
}

const governorUnreadable = "unreadable"

// curMHz is a CPU's current clock as cpufreq reports it, when it does.
func (s system) curMHz(cpu int) (float64, bool) {
	v, err := s.read(fmt.Sprintf("sys/devices/system/cpu/cpu%d/cpufreq/scaling_cur_freq", cpu))
	if err != nil {
		return 0, false
	}
	khz, err := strconv.ParseFloat(v, 64)
	if err != nil || khz <= 0 {
		return 0, false
	}
	return khz / 1000, true
}

// fixedClock reports whether a cpufreq governor keeps the clock from following load: no governor (cpufreq absent, the
// kernel does not scale the clock) or performance. Every other governor (schedutil, ondemand, conservative, powersave,
// userspace) and an unreadable one may move the clock with load, and a CPU time measured in nanoseconds moves with it.
func fixedClock(governor string) bool { return governor == "" || governor == "performance" }

// parseCPUList parses a kernel CPU list such as "0-3,8,10-11".
func parseCPUList(text string) ([]int, error) {
	var out []int
	for part := range strings.SplitSeq(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil {
			return nil, fmt.Errorf("CPU list %q: %w", text, err)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil || b < a {
				return nil, fmt.Errorf("CPU list %q: bad range %q", text, part)
			}
		}
		for c := a; c <= b; c++ {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// formatCPUList writes sorted CPUs in the kernel's list form, with ranges of three or more.
func formatCPUList(cpus []int) string {
	var parts []string
	for i := 0; i < len(cpus); {
		j := i
		for j+1 < len(cpus) && cpus[j+1] == cpus[j]+1 {
			j++
		}
		switch {
		case j-i >= 2:
			parts = append(parts, fmt.Sprintf("%d-%d", cpus[i], cpus[j]))
		case j > i:
			parts = append(parts, strconv.Itoa(cpus[i]), strconv.Itoa(cpus[j]))
		default:
			parts = append(parts, strconv.Itoa(cpus[i]))
		}
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// quietest picks n of the allowed CPUs with the largest idle share between two /proc/stat readings, ties to the lower id.
func quietest(allowed []int, before, after map[int][2]int64, n int) ([]int, map[int]float64) {
	idle := map[int]float64{}
	for _, c := range allowed {
		b, a := before[c], after[c]
		if total := a[1] - b[1]; total > 0 {
			idle[c] = float64(a[0]-b[0]) / float64(total)
		}
	}
	ranked := slices.Clone(allowed)
	slices.SortStableFunc(ranked, func(x, y int) int {
		if idle[x] != idle[y] {
			if idle[x] > idle[y] {
				return -1
			}
			return 1
		}
		return x - y
	})
	chosen := slices.Clone(ranked[:min(n, len(ranked))])
	slices.Sort(chosen)
	return chosen, idle
}
