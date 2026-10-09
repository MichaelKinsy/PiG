package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Cores whose clock scales with load are refused before anything is measured, unless the run opts in; a fixed clock
// (no governor, or performance) runs.
func TestPodRefusesCoresThatScaleTheirClock(t *testing.T) {
	if _, err := lookTaskset(); err != nil {
		t.Skip("taskset is not installed")
	}
	for _, c := range []struct {
		governor string
		allow    bool
		refused  bool
	}{
		{"", false, false},
		{"performance", false, false},
		{"schedutil", false, true},
		{"powersave", false, true},
		{"schedutil", true, false},
	} {
		files := map[string]string{"sys/devices/system/cpu/cpu2/cpufreq/scaling_governor": "performance\n"}
		if c.governor != "" {
			files["sys/devices/system/cpu/cpu3/cpufreq/scaling_governor"] = c.governor + "\n"
		}
		env := testEnv(t, fakeRoot(t, files))
		var out bytes.Buffer
		s := &session{plan: &plan{CPUs: 2, Pin: "2,3", AllowFrequencyScaling: c.allow}, env: env, out: &out}
		err := s.choosePin(context.Background())
		if (err != nil) != c.refused || (c.refused && !strings.Contains(err.Error(), "CPU 3 ("+c.governor+")")) {
			t.Errorf("%q allow=%v: %v", c.governor, c.allow, err)
		}
		var line object
		if json.Unmarshal(out.Bytes(), &line) != nil {
			t.Fatalf("pin line: %s", out.String())
		}
		var fixed bool
		line.get("fixedClock", &fixed)
		if fixed != fixedClock(c.governor) {
			t.Errorf("%q: pin line %s", c.governor, out.String())
		}
	}
	// A governor that exists but cannot be read leaves the clock unknown: refused like a scaling one, never taken for
	// a missing governor.
	env := testEnv(t, fakeRoot(t, map[string]string{"sys/devices/system/cpu/cpu3/cpufreq/scaling_governor/unreadable": "x\n"}))
	if err := (&session{plan: &plan{CPUs: 2, Pin: "2,3"}, env: env, out: &bytes.Buffer{}}).choosePin(context.Background()); err == nil || !strings.Contains(err.Error(), "CPU 3 (unreadable)") {
		t.Errorf("unreadable governor: %v", err)
	}
	// Without a pin the processes may run on any allowed core, so every one of them is checked.
	env = testEnv(t, fakeRoot(t, map[string]string{"sys/devices/system/cpu/cpu7/cpufreq/scaling_governor": "ondemand\n"}))
	s := &session{plan: &plan{CPUs: 2, Pin: "none"}, env: env, out: &bytes.Buffer{}}
	if err := s.choosePin(context.Background()); err == nil || !strings.Contains(err.Error(), "CPU 7 (ondemand)") {
		t.Errorf("unpinned: %v", err)
	}
}

// The cpu line of a step records the measured cores' clock while it ran: the highest current clock among them.
func TestPodCPULineRecordsTheClock(t *testing.T) {
	taskset, err := lookTaskset()
	if err != nil {
		t.Skip("taskset is not installed")
	}
	root := fakeRoot(t, map[string]string{
		"sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq": "2400000\n",
		"sys/devices/system/cpu/cpu1/cpufreq/scaling_cur_freq": "1200000\n",
	})
	var out bytes.Buffer
	s := &session{plan: &plan{Run: "r", StepTimeoutSeconds: 10}, env: testEnv(t, root), out: &out, pin: []int{0, 1}, taskset: taskset}
	if err := os.MkdirAll(filepath.Join(s.env.data, "r", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.step(context.Background(), "sample", target{Name: "pi"}, 5, 0, []string{"sh", "-c", "sleep 0.2"}); err != nil {
		t.Fatal(err)
	}
	var line object
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	var mhz, lo, hi float64
	if !line.get("mhz", &mhz) || !line.get("mhzMin", &lo) || !line.get("mhzMax", &hi) || mhz != 2400 || lo != 2400 || hi != 2400 {
		t.Errorf("cpu line: %s", out.String())
	}
	// Without cpufreq there is no clock in the line.
	out.Reset()
	s.env = testEnv(t, fakeRoot(t, nil))
	if err := os.MkdirAll(filepath.Join(s.env.data, "r", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.step(context.Background(), "sample", target{Name: "pi"}, 5, 0, []string{"true"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "mhz") {
		t.Errorf("no cpufreq, no clock: %s", out.String())
	}
}

// withGovernor gives a synthetic session's pin line a governor on its pinned cores and each cpu line a clock that
// moves the CPU time: a slower clock makes the same work take longer.
func withGovernor(t *testing.T, text, governor string) string {
	t.Helper()
	var lines []string
	for l := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		o := objectFrom(t, l)
		switch o.str("t") {
		case "pin":
			o.set("governors", map[string]string{"4": governor, "5": governor})
		case "cpu":
			var us float64
			o.get("usageUsec", &us)
			o.set("mhz", roundTo(3000*800000/us, 1))
		}
		b, _ := json.Marshal(o)
		lines = append(lines, string(b))
	}
	return strings.Join(lines, "\n") + "\n"
}

// A session on cores that scale their clock is reported with its clock but never judged: a calibration says why and
// passes nothing, a comparison is refused.
func TestScalingClockIsNotJudged(t *testing.T) {
	out := t.TempDir()
	cal := filepath.Join(out, "cal")
	if err := os.MkdirAll(cal, 0o755); err != nil {
		t.Fatal(err)
	}
	text := withGovernor(t, synth{mode: modeCalibrate, node: "node-a", cpuSet: "shared", runs: 12, cpu: unevenCPU}.text(t), "schedutil")
	if err := os.WriteFile(filepath.Join(cal, "pod.log"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := analyze(cal, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatalf("an unjudged calibration exits 0: %v", err)
	}
	var report struct {
		Pass  bool
		Sizes map[string]struct {
			Judged bool
			Nodes  map[string]struct {
				Pass  bool
				Clock struct {
					Scaling           []string
					Mhz               float64
					CPUMhzCorrelation float64 `json:"cpuMhzCorrelation"`
				}
			}
		}
	}
	raw, _ := os.ReadFile(filepath.Join(cal, "calibration.json"))
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	s := report.Sizes["50"]
	c := s.Nodes["node-a"].Clock
	if report.Pass || s.Judged || s.Nodes["node-a"].Pass || len(c.Scaling) != 2 || c.Mhz == 0 || c.CPUMhzCorrelation > -0.9 {
		t.Errorf("calibration: %s", raw)
	}
	if !strings.Contains(stdout.String(), "the measured cores scale their clock (node-a CPU 4 (schedutil), 5 (schedutil))") || !strings.Contains(stdout.String(), "CPU time vs clock r=-") {
		t.Errorf("stdout:\n%s", stdout.String())
	}

	cmp := filepath.Join(out, "cmp")
	if err := os.MkdirAll(cmp, 0o755); err != nil {
		t.Fatal(err)
	}
	text = withGovernor(t, synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 12}.text(t), "schedutil")
	if err := os.WriteFile(filepath.Join(cmp, "pod.log"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	err := analyze(cmp, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "node-a: the measured cores scale their clock") {
		t.Errorf("compare: %v", err)
	}
	// The same session on fixed-clock cores is judged.
	text = withGovernor(t, synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 12}.text(t), "performance")
	if err := os.WriteFile(filepath.Join(cmp, "pod.log"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := analyze(cmp, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
		t.Errorf("performance governor: %v", err)
	}

	// A quiet calibration on scaling cores is not judged either, on the node or over the nodes, and cannot vouch for a
	// comparison on that node; the same calibration on fixed-clock cores can.
	for _, governor := range []string{"schedutil", "performance"} {
		quiet := filepath.Join(out, "quiet-"+governor)
		if err := os.MkdirAll(quiet, 0o755); err != nil {
			t.Fatal(err)
		}
		text := withGovernor(t, synth{mode: modeCalibrate, node: "node-a", cpuSet: "shared", runs: 12}.text(t), governor)
		if err := os.WriteFile(filepath.Join(quiet, "pod.log"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := analyze(quiet, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
			t.Fatalf("%s calibration: %v", governor, err)
		}
		calPath := filepath.Join(quiet, "calibration.json")
		raw, _ := os.ReadFile(calPath)
		report.Sizes = nil
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
		fixed := governor == "performance"
		if s := report.Sizes["50"]; s.Judged != fixed || s.Nodes["node-a"].Pass != fixed || report.Pass != fixed {
			t.Errorf("%s calibration: %s", governor, raw)
		}
		err := analyze(cmp, out, flagsGate(gateAuto, 0.05, calPath), &bytes.Buffer{}, time.Unix(0, 0))
		if fixed && err != nil {
			t.Errorf("a fixed-clock calibration vouches for a fixed-clock comparison: %v", err)
		}
		if !fixed && (!errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "calibrated node-a at 50 turns on cores that scale their clock (CPU 4 (schedutil), 5 (schedutil))")) {
			t.Errorf("a calibration on scaling cores vouched for a comparison: %v", err)
		}
	}
}

// A quiet calibration on scaling cores passes nothing either: the node's own entry is not judged, so neither the
// overall verdict nor the node's says pass although every CV is under the gate.
func TestQuietScalingNodeDoesNotPass(t *testing.T) {
	out := t.TempDir()
	cal := filepath.Join(out, "cal")
	if err := os.MkdirAll(cal, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		governor string
		pass     bool
	}{{"performance", true}, {"schedutil", false}} {
		text := withGovernor(t, synth{mode: modeCalibrate, node: "node-a", cpuSet: "shared", runs: 12}.text(t), c.governor)
		if err := os.WriteFile(filepath.Join(cal, "pod.log"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := analyze(cal, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
			t.Fatalf("%s: %v", c.governor, err)
		}
		var report struct {
			Pass  bool
			Sizes map[string]struct {
				Judged bool
				Nodes  map[string]struct{ Pass bool }
			}
		}
		raw, _ := os.ReadFile(filepath.Join(cal, "calibration.json"))
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
		s := report.Sizes["50"]
		if report.Pass != c.pass || s.Judged != c.pass || s.Nodes["node-a"].Pass != c.pass {
			t.Errorf("%s: %s", c.governor, raw)
		}
	}
}

// A step's clock is the mean, lowest and highest of the samples taken while it ran.
func TestClockSamplerSummary(t *testing.T) {
	c := &clockSampler{stop: make(chan struct{}), done: make(chan struct{}), mhz: []float64{2400, 1800, 3000, 2800}}
	close(c.done)
	if mean, lo, hi, ok := c.finish(); !ok || mean != 2500 || lo != 1800 || hi != 3000 {
		t.Errorf("finish: %v %v %v %v", mean, lo, hi, ok)
	}
	empty := &clockSampler{stop: make(chan struct{}), done: make(chan struct{})}
	close(empty.done)
	if _, _, _, ok := empty.finish(); ok {
		t.Error("no sample, no clock")
	}
	var none *clockSampler
	if _, _, _, ok := none.finish(); ok {
		t.Error("no sampler, no clock")
	}
}
