package main

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestCPUList(t *testing.T) {
	got, err := parseCPUList("0-3,8,10-11,3")
	if err != nil || !slices.Equal(got, []int{0, 1, 2, 3, 8, 10, 11}) {
		t.Fatalf("parse: %v %v", got, err)
	}
	if s := formatCPUList(got); s != "0-3,8,10,11" {
		t.Errorf("format: %s", s)
	}
	for _, bad := range []string{"a", "3-1", "1-x"} {
		if _, err := parseCPUList(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCgroupV2(t *testing.T) {
	s := system{root: fakeRoot(t, nil)}
	if s.cgroupVersion() != 2 || s.cpuMax() != "200000 100000" || s.memoryMax() != "8589934592" {
		t.Errorf("v2: %d %q %q", s.cgroupVersion(), s.cpuMax(), s.memoryMax())
	}
	stat, ok := s.cpuStat()
	if !ok || stat != (cpuStat{UsageUsec: 1000, NrPeriods: 10, NrThrottled: 2, ThrottledUsec: 3000, HaveUsage: true}) {
		t.Errorf("cpu.stat %+v", stat)
	}
	if allowed, err := s.cpusAllowed(); err != nil || len(allowed) != 8 {
		t.Errorf("allowed %v %v", allowed, err)
	}
}

// cgroup v1 reports throttled time in nanoseconds and the quota in two files.
func TestCgroupV1(t *testing.T) {
	root := fakeRoot(t, map[string]string{
		"sys/fs/cgroup/cpu,cpuacct/cpu.stat":          "nr_periods 7\nnr_throttled 3\nthrottled_time 4500000\n",
		"sys/fs/cgroup/cpu,cpuacct/cpuacct.usage":     "2000000\n",
		"sys/fs/cgroup/cpu,cpuacct/cpu.cfs_quota_us":  "-1\n",
		"sys/fs/cgroup/cpu,cpuacct/cpu.cfs_period_us": "100000\n",
		"sys/fs/cgroup/memory/memory.limit_in_bytes":  "1073741824\n",
	})
	s := system{root: root}
	if err := removeFile(root, "sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Fatal(err)
	}
	stat, ok := s.cpuStat()
	if s.cgroupVersion() != 1 || !ok || stat != (cpuStat{UsageUsec: 2000, NrPeriods: 7, NrThrottled: 3, ThrottledUsec: 4500, HaveUsage: true}) {
		t.Errorf("v1 %d %+v", s.cgroupVersion(), stat)
	}
	if s.cpuMax() != "max 100000" || s.memoryMax() != "1073741824" {
		t.Errorf("v1 limits %q %q", s.cpuMax(), s.memoryMax())
	}
}

// cgroup v1 can mount cpuacct apart from cpu. The usage then comes from the cpuacct hierarchy, and a cgroup without
// a readable usage reports none rather than zero: the CPU gate must not judge a usage that was never measured.
func TestCgroupV1UsageFromItsOwnHierarchy(t *testing.T) {
	root := fakeRoot(t, map[string]string{
		"sys/fs/cgroup/cpu/cpu.stat":          "nr_periods 7\nnr_throttled 0\nthrottled_time 0\n",
		"sys/fs/cgroup/cpuacct/cpuacct.usage": "5000000\n",
	})
	if err := removeFile(root, "sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Fatal(err)
	}
	s := system{root: root}
	if stat, ok := s.cpuStat(); !ok || !stat.HaveUsage || stat.UsageUsec != 5000 {
		t.Errorf("separate cpuacct: %+v %v", stat, ok)
	}
	if err := removeFile(root, "sys/fs/cgroup/cpuacct/cpuacct.usage"); err != nil {
		t.Fatal(err)
	}
	stat, ok := s.cpuStat()
	if !ok || stat.HaveUsage || stat.NrPeriods != 7 {
		t.Errorf("no cpuacct.usage: %+v %v", stat, ok)
	}
	if d := stat.minus(stat); d.HaveUsage {
		t.Errorf("a delta of unread usage is unread: %+v", d)
	}
}

func TestCPUModelOnARM(t *testing.T) {
	s := system{root: fakeRoot(t, map[string]string{"proc/cpuinfo": "processor\t: 0\nCPU implementer\t: 0x41\nCPU part\t: 0xd0c\n"})}
	if m := s.cpuModel(); m != "implementer 0x41 part 0xd0c" {
		t.Errorf("model %q", m)
	}
}

func TestCPUFeaturesListTheVectorFeaturesInOneOrder(t *testing.T) {
	arm := system{root: fakeRoot(t, map[string]string{"proc/cpuinfo": "processor\t: 0\nFeatures\t: fp sve2 asimd sve crc32\n"})}
	if f := strings.Join(arm.cpuFeatures(), " "); f != "asimd sve sve2" {
		t.Errorf("arm64 features %q", f)
	}
	x86 := system{root: fakeRoot(t, map[string]string{"proc/cpuinfo": "processor\t: 0\nflags\t\t: gfni avx512vl sse avx2\n\nprocessor\t: 1\nflags\t\t: avx512f\n"})}
	if f := strings.Join(x86.cpuFeatures(), " "); f != "avx2 avx512vl gfni" {
		t.Errorf("x86 features %q (the first core's, in vectorFeatures order)", f)
	}
	if f := (system{root: fakeRoot(t, nil)}).cpuFeatures(); f != nil {
		t.Errorf("no flags line: %v", f)
	}
	// Linux spells avx512_vbmi2 and avx512_bitalg with an underscore; the Green Tea scan path needs avx512_bitalg and popcnt.
	iceLake := system{root: fakeRoot(t, map[string]string{"proc/cpuinfo": "processor\t: 0\nflags\t\t: fpu popcnt avx avx2 bmi2 avx512f avx512dq avx512cd avx512bw avx512vl avx512vbmi avx512_vbmi2 gfni avx512_vnni avx512_bitalg avx512_vpopcntdq\n"})}
	if f := strings.Join(iceLake.cpuFeatures(), " "); f != "avx avx2 avx512f avx512bw avx512cd avx512dq avx512vl avx512vbmi avx512_vbmi2 avx512_bitalg gfni bmi2 popcnt" {
		t.Errorf("x86 features with Linux's names %q", f)
	}
}

func TestQuietestPrefersIdleThenLowerID(t *testing.T) {
	before := map[int][2]int64{0: {0, 0}, 1: {0, 0}, 2: {0, 0}, 3: {0, 0}}
	after := map[int][2]int64{0: {50, 100}, 1: {90, 100}, 2: {90, 100}, 3: {100, 100}}
	got, idle := quietest([]int{0, 1, 2, 3}, before, after, 2)
	if !slices.Equal(got, []int{1, 3}) || idle[3] != 1 {
		t.Errorf("quietest %v %v", got, idle)
	}
}

func removeFile(root, path string) error { return os.Remove(root + "/" + path) }
