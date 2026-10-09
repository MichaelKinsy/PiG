package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// synth writes the pod log of a synthetic session of the durable-native profile at 50 turns. wall and cpu scale one
// sample's wall times and CPU times (the cgroup usage and the process CPU per turn); nil leaves them at 1.
type synth struct {
	mode, node, cpuSet string
	runs               int
	wall, cpu          func(target string, i int) float64
	// turnCPU makes the samples report the process CPU of the open and of each turn.
	turnCPU bool
	// fingerprint replaces the seeds' history fingerprint.
	fingerprint string
	// usage replaces every cpu line's usageUsec, as a pod that could not read the cgroup's usage wrote it.
	usage *float64
	// secondPig adds a second pig target, pig-tinygo, 10% slower than pig, with this rule ("" for the default).
	secondPig  bool
	secondRule string
	// rss replaces a target's peak RSS (MB; default 95); a negative value leaves it out of the sample lines.
	rss map[string]float64
	// extra adds fields to every sample line of a target; dropFirst leaves them out of that target's first sample.
	extra     map[string]map[string]float64
	dropFirst bool
}

var synthTurns = map[string][]float64{
	"pig":        {90, 80, 81, 79, 82, 80, 85, 83, 78, 84},
	"pig-tinygo": {99, 88, 89.1, 86.9, 90.2, 88, 93.5, 91.3, 85.8, 92.4},
	"pi":         {140, 75, 72, 71, 86, 91, 75, 77, 96, 140},
}

// synthCPU is each target's process CPU per turn (ms) at scale 1; open CPU is 10 ms.
var synthCPU = map[string]float64{"pig": 40, "pig-tinygo": 44, "pi": 60}

func (o synth) text(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "compare.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(data), "\n")
	facts := objectFrom(t, first)
	facts.set("mode", o.mode)
	facts.set("node", o.node)
	facts.set("cpuSet", o.cpuSet)
	facts.set("rounds", o.runs)
	targets := []string{"pig", "pi"}
	if o.secondPig {
		var spec profile
		facts.get("profileSpec", &spec)
		tg, _ := spec.byName("pig")
		tg.Name, tg.Rule = "pig-tinygo", o.secondRule
		spec.Targets = append(spec.Targets[:1], append([]target{tg}, spec.Targets[1:]...)...)
		facts.set("profileSpec", spec)
		targets = []string{"pig", "pig-tinygo", "pi"}
	}
	if o.mode == modeCalibrate {
		targets = []string{"pi"}
	}
	facts.set("targets", targets)
	var lines []string
	add := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	add(facts)
	lines = append(lines, `{"t":"pin","method":"quietest","cpus":"4,5"}`)
	fp := "b017b487524e44a4"
	if o.fingerprint != "" {
		fp = o.fingerprint
	}
	for _, tg := range targets {
		add(map[string]any{"t": "seed", "target": tg, "turns": 50, "fingerprint": fp, "seedMs": 1000, "bytes": 245760})
	}
	scale := func(f func(string, int) float64, tg string, i int) float64 {
		if f == nil {
			return 1
		}
		return f(tg, i)
	}
	for i := range o.runs {
		for _, tg := range targets {
			w, c := scale(o.wall, tg, i), scale(o.cpu, tg, i)
			var turn, turnCPU []float64
			for _, v := range synthTurns[tg] {
				turn = append(turn, v*w)
				turnCPU = append(turnCPU, synthCPU[tg]*c)
			}
			usage := 800000 * c
			if o.usage != nil {
				usage = *o.usage
			}
			add(map[string]any{"t": "cpu", "step": "sample", "target": tg, "turns": 50, "sample": i, "usageUsec": usage, "nrPeriods": 9, "nrThrottled": 0, "throttledUsec": 0})
			s := map[string]any{"t": "sample", "target": tg, "turns": 50, "sample": i, "open": 5 * w, "turn": turn, "peakRss": rssOf(o.rss, tg), "cpuSeconds": 1, "bytes": 245760, "fingerprint": "b73859cee894aca6"}
			for k, v := range o.extra[tg] {
				if !o.dropFirst || i != 0 {
					s[k] = v
				}
			}
			if v, ok := o.rss[tg]; ok && v < 0 {
				delete(s, "peakRss")
			}
			if o.turnCPU {
				s["openCpu"], s["turnCpu"] = 10*c, turnCPU
			}
			add(s)
		}
	}
	add(map[string]any{"t": "done", "samples": o.runs * len(targets)})
	return strings.Join(lines, "\n") + "\n"
}

// write puts the session's pod log in dir.
func (o synth) write(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pod.log"), []byte(o.text(t)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// slowSample stretches the wall times of the reference's fourth sample to twice their length, as a busy neighbour on
// shared cores did, without changing its CPU time.
func slowSample(target string, i int) float64 {
	if target == "pi" && i == 3 {
		return 2
	}
	return 1
}

// unevenCPU varies every sample's CPU time by up to 20%.
func unevenCPU(_ string, i int) float64 { return 1 + 0.1*float64(i%3-1) }

func TestGatesFor(t *testing.T) {
	for _, c := range []struct {
		mode, cpuSet string
		wall         bool
	}{
		{gateAuto, "exclusive", true},
		{gateAuto, "shared", false},
		{gateAuto, "", false},
		{gateWall, "shared", true},
		{gateCPU, "exclusive", false},
	} {
		g, err := gatesFor(c.mode, c.cpuSet)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(g, "warm") != c.wall || slices.Contains(g, "cold") != c.wall || !slices.Contains(g, "cpu") || !slices.Contains(g, "bytes") {
			t.Errorf("%s on %q cores gates %v", c.mode, c.cpuSet, g)
		}
	}
	if _, err := gatesFor("fast", "shared"); err == nil {
		t.Error("an unknown gate is refused")
	}
}

// On shared cores a neighbour stretches wall times without changing the work: the default gate judges CPU time, and
// reports the wall times without gating them.
func TestSharedCoresGateOnCPUTime(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "run1")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 12, wall: slowSample, turnCPU: true}.write(t, dir)
	var stdout bytes.Buffer
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatalf("a wall-time outlier with equal CPU time must pass the CPU gate: %v", err)
	}
	if !strings.Contains(stdout.String(), "noise gate on cpu, cpuWarm, cpuCold, bytes, wall times informational") {
		t.Errorf("the summary names the gate:\n%s", stdout.String())
	}
	var noise struct {
		Gated []string
		Noise map[string]map[string]metricNoise
	}
	data, _ := os.ReadFile(filepath.Join(dir, "noise.json"))
	if err := json.Unmarshal(data, &noise); err != nil {
		t.Fatal(err)
	}
	if n := noise.Noise["50"]; n["cold"].Gate || n["cold"].CV < 0.05 || !n["cpu"].Gate || n["cpu"].CV != 0 || !n["cpuWarm"].Gate || !n["bytes"].Gate {
		t.Errorf("noise.json: %s", data)
	}

	err := analyze(dir, out, flagsGate(gateWall, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "warm CV") || !strings.Contains(err.Error(), "cold CV") {
		t.Fatalf("-gate wall judges the wall times, got %v", err)
	}

	noisy := filepath.Join(out, "run2")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 12, cpu: unevenCPU, turnCPU: true}.write(t, noisy)
	err = analyze(noisy, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0))
	for _, want := range []string{"cpu CV 8.5%", "cpuWarm CV 8.5%", "cpuCold CV 8.5%"} {
		if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), want) {
			t.Fatalf("uneven CPU time is noise on shared cores; want %q, got %v", want, err)
		}
	}
}

// On exclusive cores no neighbour shares the pod's cores, so the default gate judges the wall times too.
func TestExclusiveCoresGateOnWallTime(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "run1")
	synth{mode: modeCompare, node: "node-a", cpuSet: "exclusive", runs: 12, wall: slowSample}.write(t, dir)
	err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "cold CV") {
		t.Fatalf("want a refusal on the cold wall time, got %v", err)
	}
	if err := analyze(dir, out, flagsGate(gateCPU, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
		t.Fatalf("-gate cpu ignores the wall times: %v", err)
	}
}

// The tracker reports track-row's CPU per warm turn and cold CPU when the workload measures them, and compares them
// with the reference's.
func TestTrackerReportsCPUPerTurn(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "run1")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 12, turnCPU: true}.write(t, dir)
	var stdout bytes.Buffer
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	line := objectFrom(t, strings.TrimSpace(stdout.String()))
	values := map[string]float64{}
	line.get("values", &values)
	if values["cpuWarm"] != 40 || values["cpuCold"] != 50 || values["piCpuWarm"] != 60 || values["piCpuCold"] != 70 {
		t.Errorf("values %v", values)
	}
	var keys object
	line.get("values", &keys)
	if !slices.Equal(keys.keys[6:10], []string{"seedS", "cpuWarm", "cpuCold", "peakRssMB"}) || !slices.Equal(keys.keys[15:19], []string{"piSeedS", "piCpuWarm", "piCpuCold", "piPeakRssMB"}) {
		t.Errorf("values in track-row's order: %v", keys.keys)
	}
	if !strings.Contains(line.str("summary"), "CPU/warm turn 40 vs 60") || !strings.Contains(line.str("summary"), "CPU cold 50 vs 70") {
		t.Errorf("summary: %s", line.str("summary"))
	}

	// Without per-turn CPU there is no CPU value, rather than a made-up one.
	plain := filepath.Join(out, "run2")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 12}.write(t, plain)
	stdout.Reset()
	if err := analyze(plain, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	line = objectFrom(t, strings.TrimSpace(stdout.String()))
	line.get("values", &keys)
	if slices.ContainsFunc(keys.keys, func(k string) bool { return strings.Contains(strings.ToLower(k), "cpu") }) || strings.Contains(line.str("summary"), "CPU/warm turn") {
		t.Errorf("no per-turn CPU, no CPU values:\n%s", stdout.String())
	}
}

// A calibration judges a size only from twelve runs; fewer are a smoke test that is reported but neither passes nor
// fails.
func TestCalibrationJudgesTwelveRuns(t *testing.T) {
	for _, c := range []struct {
		name   string
		runs   int
		cpu    func(string, int) float64
		judged bool
		noisy  bool
	}{
		{"quiet", 12, nil, true, false},
		{"noisy", 12, unevenCPU, true, true},
		{"smoke", 11, unevenCPU, false, false},
	} {
		out := t.TempDir()
		dir := filepath.Join(out, "cal")
		synth{mode: modeCalibrate, node: "node-a", cpuSet: "shared", runs: c.runs, wall: slowSample, cpu: c.cpu, turnCPU: true}.write(t, dir)
		var stdout bytes.Buffer
		err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0))
		if errors.Is(err, errNoisy) != c.noisy || (err != nil && !c.noisy) {
			t.Fatalf("%s: %v", c.name, err)
		}
		var report struct {
			Pass  bool
			Gated []string
			Nodes []string
			Sizes map[string]struct {
				Runs   int
				Judged bool
				Pass   bool
				Noise  map[string]metricNoise
				Nodes  map[string]struct{ Runs int }
			}
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "calibration.json"))
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
		s := report.Sizes["50"]
		if report.Pass != (c.judged && !c.noisy) || s.Judged != c.judged || s.Pass != report.Pass || s.Runs != c.runs || s.Nodes["node-a"].Runs != c.runs ||
			!slices.Equal(report.Nodes, []string{"node-a"}) || slices.Contains(report.Gated, "warm") || s.Noise["cold"].Gate {
			t.Errorf("%s: %s", c.name, raw)
		}
		if smoke := strings.Contains(stdout.String(), "not judged (50 turns: 11 runs)"); smoke == c.judged {
			t.Errorf("%s: %s", c.name, stdout.String())
		}
	}
}

func writeCalibration(t *testing.T, path string, runs int, nodes ...string) {
	t.Helper()
	quiet := metricNoise{N: runs, Mean: 100, CV: 0.01, Gate: true}
	data, _ := json.Marshal(map[string]any{"profile": "durable-native", "nodes": nodes, "sizes": map[string]any{"50": map[string]any{"runs": runs, "noise": map[string]any{
		"warm": quiet, "cold": quiet, "cpu": quiet}}}})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A smoke calibration does not vouch for a comparison.
func TestCompareRefusesASmokeCalibration(t *testing.T) {
	out, dir := setupRun(t, "compare.jsonl")
	cal := filepath.Join(out, "calibration.json")
	writeCalibration(t, cal, 6, "node-a")
	err := analyze(dir, out, flagsGate(gateAuto, 0.05, cal), &bytes.Buffer{}, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "has 6 runs at 50 turns; a calibration judges the noise floor from 12") {
		t.Fatalf("want a refusal of the smoke calibration, got %v", err)
	}
	writeCalibration(t, cal, 12, "node-a")
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, cal), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
}

// writeSpread lays out a spread session: one run directory per node and spread.json naming them.
func writeSpread(t *testing.T, dir string, runs ...synth) {
	t.Helper()
	var names []string
	for i, r := range runs {
		name := fmt.Sprintf("run-n%d", i+1)
		r.write(t, filepath.Join(dir, name))
		names = append(names, name)
	}
	data, _ := json.Marshal(map[string]any{"runs": names})
	if err := os.WriteFile(filepath.Join(dir, spreadFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// slower scales every wall and CPU time of one node by a factor.
func slower(f float64) func(string, int) float64 { return func(string, int) float64 { return f } }

// A spread session reports, per value, the median over its nodes.
func TestSpreadTakesTheMedianOverNodes(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "spread")
	writeSpread(t, dir,
		synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, turnCPU: true},
		synth{mode: modeCompare, node: "node-b", cpuSet: "shared", runs: 6, wall: slower(1.5), cpu: slower(1.5), turnCPU: true},
		synth{mode: modeCompare, node: "node-c", cpuSet: "shared", runs: 6, wall: slower(1.2), cpu: slower(1.2), turnCPU: true},
	)
	var stdout bytes.Buffer
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	line := objectFrom(t, strings.TrimSpace(stdout.String()))
	values := map[string]float64{}
	line.get("values", &values)
	// node-a's pig warm p50 is 81, node-c's 97.2, node-b's 121.5: the median is node-c's.
	var warmTurns int
	line.get("warmTurns", &warmTurns)
	if values["warm"] != 97.2 || values["cpuWarm"] != 48 || values["piCpuWarm"] != 72 || warmTurns != 3*6*9 {
		t.Errorf("values %v, warm turns %d", values, warmTurns)
	}
	if !strings.Contains(line.str("summary"), "median of 3 Kubernetes nodes node-a, node-b, node-c (shared cpusets") {
		t.Errorf("summary: %s", line.str("summary"))
	}
	for _, n := range []string{"run-n1", "run-n2", "run-n3"} {
		if _, err := os.Stat(filepath.Join(dir, n, "results.jsonl")); err != nil {
			t.Error(err)
		}
	}

	// A calibration from fewer nodes does not vouch for the others.
	cal := filepath.Join(out, "calibration.json")
	writeCalibration(t, cal, 12, "node-a", "node-b")
	err := analyze(dir, out, flagsGate(gateAuto, 0.05, cal), &bytes.Buffer{}, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "calibrated nodes node-a, node-b, not node-c") {
		t.Fatalf("want a refusal naming the uncalibrated node, got %v", err)
	}
	writeCalibration(t, cal, 12, "node-a", "node-b", "node-c")
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, cal), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
}

// A spread calibration reports each node's noise and, as the session's, the median over the nodes.
func TestSpreadCalibration(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "spread")
	cpuOf := func(scale float64) func(string, int) float64 {
		return func(_ string, i int) float64 { return 1 + scale*float64(i%2) }
	}
	writeSpread(t, dir,
		synth{mode: modeCalibrate, node: "node-a", cpuSet: "shared", runs: 12, cpu: cpuOf(0.01)},
		synth{mode: modeCalibrate, node: "node-b", cpuSet: "shared", runs: 12, cpu: cpuOf(0.02)},
		synth{mode: modeCalibrate, node: "node-c", cpuSet: "shared", runs: 12, cpu: cpuOf(0.2)},
	)
	var stdout bytes.Buffer
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatalf("the median node is quiet: %v", err)
	}
	var report struct {
		Pass  bool
		Nodes []string
		Sizes map[string]struct {
			Judged bool
			Noise  map[string]metricNoise
			Nodes  map[string]struct {
				Pass     bool
				Problems []string
				Noise    map[string]metricNoise
			}
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "calibration.json"))
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	s := report.Sizes["50"]
	b := s.Nodes["node-b"].Noise["cpu"].CV
	if !report.Pass || !s.Judged || len(report.Nodes) != 3 || b == 0 || s.Noise["cpu"].CV != b || s.Nodes["node-c"].Noise["cpu"].CV < 0.05 {
		t.Errorf("calibration: %s", raw)
	}
	// The quiet median passes, but each node keeps its own verdict, and the noisy one is named.
	if !s.Nodes["node-a"].Pass || s.Nodes["node-c"].Pass || len(s.Nodes["node-c"].Problems) == 0 {
		t.Errorf("per-node verdicts: %s", raw)
	}
	if !strings.Contains(stdout.String(), "median over 3 nodes: node-a, node-b, node-c") || !strings.Contains(stdout.String(), "noisy on its own") ||
		!strings.Contains(stdout.String(), "pi at 50 turns on node-c: cpu CV") || strings.Contains(stdout.String(), "on node-a: cpu CV") ||
		!strings.Contains(stdout.String(), "50       node-b: cpu ") {
		t.Errorf("stdout:\n%s", stdout.String())
	}
}

func TestSpreadRefusesDifferentSessions(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "spread")
	writeSpread(t, dir,
		synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6},
		synth{mode: modeCalibrate, node: "node-b", cpuSet: "shared", runs: 6},
	)
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); err == nil || !strings.Contains(err.Error(), "not one session") {
		t.Fatalf("got %v", err)
	}
	dir = filepath.Join(out, "spread2")
	writeSpread(t, dir,
		synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6},
		synth{mode: modeCompare, node: "node-b", cpuSet: "shared", runs: 6, fingerprint: "ffffffffffffffff"},
	)
	// The published fingerprint check refuses the second node's history first; mergeTrack is the backstop.
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); err == nil || !strings.Contains(err.Error(), "ffffffffffffffff") {
		t.Fatalf("got %v", err)
	}
	if _, err := mergeTrack([]trackValues{{fingerprint: "a"}, {fingerprint: "b"}}); err == nil {
		t.Error("mergeTrack accepts different histories")
	}
}

// A Go program's CPU time follows the node's vector features and GOMAXPROCS: a median over unlike nodes is refused, and one
// pod's samples must all name one node type.
func TestNodeTypesMustAgree(t *testing.T) {
	avx512 := "Example CPU [avx2 avx512f] GOMAXPROCS 2"
	if _, err := mergeTrack([]trackValues{{fingerprint: "a", machine: avx512}, {fingerprint: "a", machine: "Example CPU [avx2] GOMAXPROCS 2"}}); err == nil || !strings.Contains(err.Error(), "different types") {
		t.Errorf("unlike vector features: %v", err)
	}
	if _, err := mergeTrack([]trackValues{{fingerprint: "a", machine: avx512}, {fingerprint: "a", machine: "Example CPU [avx2 avx512f] GOMAXPROCS 4"}}); err == nil {
		t.Error("unlike GOMAXPROCS merged")
	}
	if m, err := mergeTrack([]trackValues{{fingerprint: "a", machine: avx512}, {fingerprint: "a", machine: avx512}}); err != nil || m.machine != avx512 {
		t.Errorf("like nodes: %v %q", err, m.machine)
	}
	line := func(gmp int) *object {
		var o object
		if err := json.Unmarshal([]byte(fmt.Sprintf(`{"gomaxprocs":%d,"cpuModel":"Example CPU","cpuFeatures":"avx2"}`, gmp)), &o); err != nil {
			t.Fatal(err)
		}
		return &o
	}
	log := &podLog{samples: []sample{{raw: line(2)}, {raw: line(2)}}}
	if m, err := machineOf(log); err != nil || m != "Example CPU [avx2] GOMAXPROCS 2" {
		t.Errorf("one node type: %q %v", m, err)
	}
	log.samples = append(log.samples, sample{raw: line(8)})
	if _, err := machineOf(log); err == nil {
		t.Error("a pod whose samples name two GOMAXPROCS values passes")
	}

	// The analysis refuses both on its own path: one pod whose targets ran under two GOMAXPROCS values, and a spread
	// whose nodes differ.
	gmp := func(n float64) map[string]map[string]float64 {
		return map[string]map[string]float64{"pig": {"gomaxprocs": n}, "pi": {"gomaxprocs": n}}
	}
	out := t.TempDir()
	pod := filepath.Join(out, "pod")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, extra: map[string]map[string]float64{"pig": {"gomaxprocs": 2}, "pi": {"gomaxprocs": 4}}}.write(t, pod)
	if err := analyze(pod, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); err == nil || !strings.Contains(err.Error(), "different node types or GOMAXPROCS") {
		t.Errorf("one pod, two GOMAXPROCS values: %v", err)
	}
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, extra: gmp(2)}.write(t, pod)
	if err := analyze(pod, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
		t.Errorf("one pod, one node type: %v", err)
	}
	dir := filepath.Join(out, "spread")
	writeSpread(t, dir,
		synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, extra: gmp(2)},
		synth{mode: modeCompare, node: "node-b", cpuSet: "shared", runs: 6, extra: gmp(4)},
	)
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); err == nil || !strings.Contains(err.Error(), "spread over nodes of one type") {
		t.Errorf("a spread over two GOMAXPROCS values: %v", err)
	}
}

// A spread session gates on wall times only when every node gave the pod exclusive cores.
func TestSpreadGatesOnWallTimeOnlyWhenEveryNodeIsExclusive(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "spread")
	writeSpread(t, dir,
		synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 12, wall: slowSample},
		synth{mode: modeCompare, node: "node-b", cpuSet: "exclusive", runs: 12, wall: slowSample},
	)
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
		t.Fatalf("one shared node makes wall times informational: %v", err)
	}
	dir = filepath.Join(out, "exclusive")
	writeSpread(t, dir,
		synth{mode: modeCompare, node: "node-a", cpuSet: "exclusive", runs: 12, wall: slowSample},
		synth{mode: modeCompare, node: "node-b", cpuSet: "exclusive", runs: 12, wall: slowSample},
	)
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); !errors.Is(err, errNoisy) {
		t.Fatalf("exclusive nodes gate the wall times: %v", err)
	}
}

// A gated metric that is zero in every sample was not measured, so its CV of 0 must not pass the gate. A pod on
// cgroup v1 without a readable cpuacct.usage wrote usageUsec 0 for every sample.
func TestCPUGateRefusesAnUnmeasuredUsage(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "run1")
	zero := 0.0
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 12, turnCPU: true, usage: &zero}.write(t, dir)
	err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "cpu is zero in every sample") {
		t.Fatalf("want a refusal of the unmeasured cgroup usage, got %v", err)
	}
	cal := filepath.Join(out, "cal")
	synth{mode: modeCalibrate, node: "node-a", cpuSet: "shared", runs: 12, turnCPU: true, usage: &zero}.write(t, cal)
	if err := analyze(cal, out, flagsGate(gateAuto, 0.05, ""), &bytes.Buffer{}, time.Unix(0, 0)); !errors.Is(err, errNoisy) {
		t.Fatalf("a calibration without a measured usage cannot pass: %v", err)
	}
}

// A calibration that names no node cannot show that it measured the node a comparison ran on.
func TestCompareRefusesACalibrationWithoutNodes(t *testing.T) {
	out, dir := setupRun(t, "compare.jsonl")
	cal := filepath.Join(out, "calibration.json")
	writeCalibration(t, cal, 12)
	err := analyze(dir, out, flagsGate(gateAuto, 0.05, cal), &bytes.Buffer{}, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "names no node") {
		t.Fatalf("want a refusal of the calibration without nodes, got %v", err)
	}
}

// writeNodeCalibration writes a spread calibration at 50 turns: per node, its own CV of every gated metric, and as
// the session's noise the median over the nodes.
func writeNodeCalibration(t *testing.T, path string, cv map[string]float64) {
	t.Helper()
	perNode := map[string]any{}
	var cvs []float64
	var nodes []string
	for _, n := range slices.Sorted(maps.Keys(cv)) {
		m := metricNoise{N: 12, Mean: 100, CV: cv[n], Gate: true}
		perNode[n] = map[string]any{"runs": 12, "noise": map[string]any{"warm": m, "cold": m, "cpu": m}}
		cvs = append(cvs, cv[n])
		nodes = append(nodes, n)
	}
	med := metricNoise{N: 12, Mean: 100, CV: median(cvs), Gate: true}
	data, _ := json.Marshal(map[string]any{"profile": "durable-native", "nodes": nodes, "sizes": map[string]any{"50": map[string]any{
		"runs": 12, "noise": map[string]any{"warm": med, "cold": med, "cpu": med}, "nodes": perNode}}})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A spread calibration's median does not vouch for a noisy node: a comparison on some of its nodes is judged by the
// noise of those nodes.
func TestCompareJudgesTheCalibrationOfItsOwnNodes(t *testing.T) {
	out, dir := setupRun(t, "compare.jsonl") // ran on node-a
	cal := filepath.Join(out, "calibration.json")
	writeNodeCalibration(t, cal, map[string]float64{"node-a": 0.2, "node-b": 0.01, "node-c": 0.02})
	err := analyze(dir, out, flagsGate(gateAuto, 0.05, cal), &bytes.Buffer{}, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "calibration: cpu CV 20.0% exceeds 5.0%") {
		t.Fatalf("node-a's own calibration is noisy, got %v", err)
	}
	writeNodeCalibration(t, cal, map[string]float64{"node-a": 0.01, "node-b": 0.2, "node-c": 0.3})
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, cal), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
		t.Fatalf("node-a's own calibration is quiet: %v", err)
	}
}

// A profile with several pig targets measures them in one session and writes one tracker line per pig target and
// size, each with its own rule, rolling history and baseline.
func TestSeveralPigTargets(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "run1")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, turnCPU: true, secondPig: true}.write(t, dir)
	var stdout bytes.Buffer
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one line per pig target:\n%s", stdout.String())
	}
	want := map[string][2]float64{"native-pig": {81, 40}, "native-pig-tinygo": {89.1, 44}}
	for _, text := range lines {
		line := objectFrom(t, text)
		values := map[string]float64{}
		line.get("values", &values)
		w, ok := want[line.str("rule")]
		if !ok || values["warm"] != w[0] || values["cpuWarm"] != w[1] || values["piWarm"] != 77 || values["piCpuWarm"] != 60 {
			t.Errorf("line %s: %v", line.str("rule"), values)
		}
		target := strings.TrimPrefix(line.str("rule"), "native-")
		if !strings.Contains(line.str("summary"), "Rule "+line.str("rule")+",") || !strings.Contains(line.str("summary"), "PiG "+target+" vs pi same run") {
			t.Errorf("summary: %s", line.str("summary"))
		}
		delete(want, line.str("rule"))
	}

	// A second run's rolling history holds the first run's line of its own rule only, and a baseline with a rule
	// applies to that rule's line alone.
	dir2 := filepath.Join(out, "run2")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, turnCPU: true, secondPig: true}.write(t, dir2)
	baseline := filepath.Join(out, "baseline.json")
	if err := os.WriteFile(baseline, []byte(`{"rule":"native-pig-tinygo","core":"base","warm":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	empty, gate, maxCV := "", gateAuto, 0.05
	a := analysisFlags{baseline: &baseline, history: &empty, track: &empty, calibration: &empty, maxCV: &maxCV, gate: &gate}
	stdout.Reset()
	if err := analyze(dir2, out, a, &stdout, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(stdout.String()), "\n") + 1; n != 2 {
		t.Fatalf("second run: %d lines, want one per pig target:\n%s", n, stdout.String())
	}
	for text := range strings.SplitSeq(strings.TrimSpace(stdout.String()), "\n") {
		line := objectFrom(t, text)
		var cmp map[string]struct{ NRolling int }
		line.get("compare", &cmp)
		delta := map[string]string{}
		line.get("delta", &delta)
		tinygo := line.str("rule") == "native-pig-tinygo"
		if cmp["warm"].NRolling != 2 || (delta["warm"] == "-10%") != tinygo || (line.str("baseline") == "base") != tinygo {
			t.Errorf("%s: rolling %d, delta %s, baseline %s", line.str("rule"), cmp["warm"].NRolling, delta["warm"], line.str("baseline"))
		}
	}

	ruleless := filepath.Join(out, "ruleless.json")
	if err := os.WriteFile(ruleless, []byte(`{"core":"base","warm":99}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.baseline = &ruleless
	if err := analyze(dir, out, a, &bytes.Buffer{}, time.Unix(2, 0)); err == nil || !strings.Contains(err.Error(), "has no rule; with 2 pig targets") {
		t.Errorf("a rule-less baseline is ambiguous with two pig targets: %v", err)
	}
}

// An explicit rule names a pig target's tracker line.
func TestPigTargetRule(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "run1")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, secondPig: true, secondRule: "core-tinygo"}.write(t, dir)
	var stdout bytes.Buffer
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"rule":"native-pig"`) || !strings.Contains(stdout.String(), `"rule":"core-tinygo"`) {
		t.Errorf("rules:\n%s", stdout.String())
	}
}

func TestProfileRoles(t *testing.T) {
	base, err := loadProfile("durable-native")
	if err != nil {
		t.Fatal(err)
	}
	pig, _ := base.byName("pig")
	ref, _ := base.byName("pi")
	with := func(ts ...target) *profile {
		p := *base
		p.Targets = ts
		return &p
	}
	tinygo := pig
	tinygo.Name = "pig-tinygo"
	if p := with(pig, ref); p.validate() != nil || p.ruleOf(pig) != "native" {
		t.Errorf("one pig target keeps the profile's rule: %v %s", p.validate(), p.ruleOf(pig))
	}
	p := with(pig, tinygo, ref)
	if err := p.validate(); err != nil || p.ruleOf(pig) != "native-pig" || p.ruleOf(tinygo) != "native-pig-tinygo" || !slices.Equal(sessionTargets(p, modeCompare), []string{"pig", "pig-tinygo", "pi"}) {
		t.Errorf("two pig targets: %v %s %s %v", err, p.ruleOf(pig), p.ruleOf(tinygo), sessionTargets(p, modeCompare))
	}
	clash := tinygo
	clash.Rule = "native-pig"
	refRule := ref
	refRule.Rule = "x"
	for name, c := range map[string]struct {
		p    *profile
		want string
	}{
		"no pig":      {with(ref), "at least one pig target"},
		"shared rule": {with(pig, clash, ref), "share the rule native-pig"},
		"ref rule":    {with(pig, refRule), "only a pig target has its own rule"},
	} {
		if err := c.p.validate(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Each -baseline file applies to exactly one tracker line: a file whose rule no pig target has, or a second file for
// one rule, is refused instead of being dropped or overwriting the first, so a line never silently loses its deltas.
func TestReadBaselines(t *testing.T) {
	base, err := loadProfile("durable-native")
	if err != nil {
		t.Fatal(err)
	}
	pig, _ := base.byName("pig")
	ref, _ := base.byName("pi")
	tinygo := pig
	tinygo.Name = "pig-tinygo"
	one := *base
	one.Targets = []target{pig, ref}
	two := *base
	two.Targets = []target{pig, tinygo, ref}
	dir := t.TempDir()
	file := func(name, text string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	ruleless := file("ruleless.json", `{"core":"a","warm":1}`)
	native := file("native.json", `{"rule":"native","core":"b","warm":2}`)
	pigLine := file("pig.json", `{"rule":"native-pig","core":"c","warm":3}`)
	tinygoLine := file("tinygo.json", `{"rule":"native-pig-tinygo","core":"d","warm":4}`)
	typo := file("typo.json", `{"rule":"native-pig-tinyg0","core":"e","warm":5}`)

	if got, err := readBaselines(ruleless, &one); err != nil || got["native"].str("core") != "a" || len(got) != 1 {
		t.Errorf("a rule-less file belongs to the only pig target: %v %v", got, err)
	}
	if got, err := readBaselines(native, &one); err != nil || got["native"].str("core") != "b" {
		t.Errorf("a file of the only pig target's rule: %v %v", got, err)
	}
	if got, err := readBaselines(pigLine+","+tinygoLine, &two); err != nil || got["native-pig"].str("core") != "c" || got["native-pig-tinygo"].str("core") != "d" || len(got) != 2 {
		t.Errorf("one file per pig target: %v %v", got, err)
	}
	for name, c := range map[string]struct {
		paths string
		prof  *profile
		want  string
	}{
		"unknown rule":       {pigLine + "," + typo, &two, "is for rule native-pig-tinyg0, which no pig target of profile durable-native has"},
		"other profile rule": {pigLine, &one, "is for rule native-pig, which no pig target"},
		"two for one rule":   {tinygoLine + "," + tinygoLine, &two, "names two files for rule native-pig-tinygo"},
		"rule-less with two": {ruleless, &two, "has no rule; with 2 pig targets"},
		"two rule-less":      {ruleless + "," + ruleless, &one, "names two files for rule native"},
	} {
		if _, err := readBaselines(c.paths, c.prof); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func rssOf(rss map[string]float64, target string) float64 {
	if v, ok := rss[target]; ok {
		return v
	}
	return 95
}

// The tracker compares PiG's peak resident memory with the reference's, as the median over the samples, converted from
// the workloads' MiB to track-row's MB (40 MiB is 41.9 MB); without it
// on every sample of both targets there is no memory value.
func TestTrackerComparesMemory(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "run1")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, rss: map[string]float64{"pig": 40, "pi": 90}}.write(t, dir)
	var stdout bytes.Buffer
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	line := objectFrom(t, strings.TrimSpace(stdout.String()))
	values := map[string]float64{}
	line.get("values", &values)
	var cmp map[string]struct{ Run string }
	line.get("compare", &cmp)
	if values["peakRssMB"] != 41.9 || values["piPeakRssMB"] != 94.4 || cmp["peakRssMB"].Run != "win" || !strings.Contains(line.str("summary"), "peak RSS MB 41.9 vs 94.4") {
		t.Errorf("memory: %v %v %s", values, cmp, line.str("summary"))
	}

	dir = filepath.Join(out, "run2")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, rss: map[string]float64{"pi": -1}}.write(t, dir)
	stdout.Reset()
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "peakRssMB") {
		t.Errorf("a reference without peak RSS has no memory pair:\n%s", stdout.String())
	}
}

// Retained memory is compared like the times; heap growth per turn is reported for both targets without a verdict.
// Either exists only when every sample of both targets reports it.
func TestTrackerReportsRetainedMemoryAndGrowth(t *testing.T) {
	out := t.TempDir()
	extra := map[string]map[string]float64{"pig": {"retained": 20, "growthPerTurn": 0}, "pi": {"retained": 80, "growthPerTurn": 4096}}
	dir := filepath.Join(out, "run1")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, extra: extra}.write(t, dir)
	var stdout bytes.Buffer
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	line := objectFrom(t, strings.TrimSpace(stdout.String()))
	var values object
	line.get("values", &values)
	var cmp map[string]struct{ Run string }
	line.get("compare", &cmp)
	// 20 and 80 MiB are 21.0 and 83.9 MB.
	want := map[string]float64{"retainedMB": 21, "piRetainedMB": 83.9, "growthPerTurn": 0, "piGrowthPerTurn": 4096}
	for k, w := range want {
		var v float64
		if !values.get(k, &v) || v != w {
			t.Errorf("%s = %v, want %v", k, v, w)
		}
	}
	if i := slices.Index(values.keys, "retainedMB"); i < 0 || values.keys[i+1] != "growthPerTurn" || values.keys[i-1] != "seedS" {
		t.Errorf("order: %v", values.keys)
	}
	if _, ok := cmp["growthPerTurn"]; ok || cmp["retainedMB"].Run != "win" {
		t.Errorf("compare: %v", cmp)
	}
	if sum := line.str("summary"); !strings.Contains(sum, "retained MB 21 vs 83.9") || !strings.Contains(sum, "heap growth/turn 0 B vs 4,096 B") {
		t.Errorf("summary: %s", sum)
	}

	dir = filepath.Join(out, "run2")
	synth{mode: modeCompare, node: "node-a", cpuSet: "shared", runs: 6, extra: extra, dropFirst: true}.write(t, dir)
	stdout.Reset()
	if err := analyze(dir, out, flagsGate(gateAuto, 0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	line = objectFrom(t, strings.TrimSpace(stdout.String()))
	values = object{}
	line.get("values", &values)
	for k := range want {
		if values.has(k) {
			t.Errorf("a sample without the measures leaves %s out: %v", k, values.keys)
		}
	}
	if strings.Contains(line.str("summary"), "growth") {
		t.Errorf("summary: %s", line.str("summary"))
	}
}

// The noise gate compares the exact CV: a reference whose CPU per warm turn varies by 5.0025% (a cluster run's) is
// over a 5% gate, alone and as the median over nodes, though it rounds to 5.00%.
func TestNoiseGateComparesTheExactCV(t *testing.T) {
	const want = 0.050025
	// Twelve samples alternating mean±d have CV d·sqrt(12/11)/mean.
	d := want * 100 / math.Sqrt(12.0/11)
	var xs []float64
	for i := range 12 {
		xs = append(xs, 100+d*float64(1-2*(i%2)))
	}
	m := spread(xs, true)
	if math.Abs(m.CV-want) > 1e-9 {
		t.Fatalf("CV %v, want %v", m.CV, want)
	}
	one := newObject()
	one.set("cpuWarm", m)
	if p := noiseProblems("pi", one, []string{"cpuWarm"}, 0.05); len(p) != 1 || p[0] != "pi: cpuWarm CV 5.003% exceeds 5.0%" {
		t.Errorf("one node: %v", p)
	}
	quiet := newObject()
	quiet.set("cpuWarm", spread([]float64{100, 101, 99}, true))
	median := combineNoise([]*object{one, one, quiet}, []string{"cpuWarm"})
	if p := noiseProblems("pi", median, []string{"cpuWarm"}, 0.05); len(p) != 1 {
		t.Errorf("median over nodes: %v", p)
	}
}
