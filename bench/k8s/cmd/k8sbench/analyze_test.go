package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// trackLine computes one tracker line from one pod's log for the profile's first pig target, as analyze does for each
// pig target of a single-node run.
func trackLine(log *podLog, in trackInput) (*object, error) {
	if in.pig.Name == "" {
		in.pig = in.prof.pigs()[0]
	}
	tv, err := computeTrack(in.prof, in.pig, log, in.turns)
	if err != nil {
		return nil, err
	}
	return buildTrackLine(in, tv), nil
}

func readLog(t *testing.T, path string) *podLog {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	log, err := parsePodLog(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.complete(); err != nil {
		t.Fatal(err)
	}
	return log
}

func objectFrom(t *testing.T, text string) *object {
	t.Helper()
	o := &object{}
	if err := json.Unmarshal([]byte(text), o); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestStatisticsMatchTrackRow(t *testing.T) {
	xs := []float64{5, 1, 4, 2, 3}
	if quantile(xs, 0.5) != 3 || quantile(xs, 0.95) != 4.8 || quantile([]float64{1, 2}, 0.5) != 1.5 {
		t.Error("quantile is not track-row's linear interpolation")
	}
	for _, c := range []struct {
		in   float64
		want float64
	}{{2.5, 3}, {-2.5, -2}, {0.49999999999999994, 0}, {1.45, 1}} {
		if got := jsRound(c.in); got != c.want {
			t.Errorf("jsRound(%v) = %v, want Math.round's %v", c.in, got, c.want)
		}
	}
	for _, c := range []struct {
		a, b float64
		want string
	}{{81.5, 80, "+2%"}, {98, 100, "-2%"}, {99.6, 100, "-0%"}, {102.5, 100, "+3%"}, {97.5, 100, "-3%"}, {5, 0, "n/a"}} {
		if got := pct(c.a, c.b, true); got != c.want {
			t.Errorf("pct(%v, %v) = %q, want %q (toFixed(0))", c.a, c.b, got, c.want)
		}
	}
	if pct(1, 1, false) != "n/a" {
		t.Error("a missing baseline value is n/a")
	}
	for _, c := range []struct {
		pig, pi float64
		want    string
	}{{104, 100, "lose"}, {103, 100, "tie"}, {96, 100, "slightly better"}, {76.9, 100, "win"}, {77, 100, "slightly better"}} {
		if got := verdict(c.pig, c.pi); got != c.want {
			t.Errorf("verdict(%v, %v) = %q, want %q", c.pig, c.pi, got, c.want)
		}
	}
	for v, want := range map[float64]string{1234.56: "1,235", 99.94: "99.9", 1234567: "1,234,567", 0.25: "0.3", -1500: "-1,500"} {
		if got := formatNumber(v); got != want {
			t.Errorf("formatNumber(%v) = %q, want %q", v, got, want)
		}
	}
}

// The expected values were computed from testdata/compare.jsonl with track-row.sh's own JavaScript formulas.
func TestTrackLineMatchesTrackRow(t *testing.T) {
	log := readLog(t, filepath.Join("testdata", "compare.jsonl"))
	var prof profile
	if !log.facts.get("profileSpec", &prof) {
		t.Fatal("no profileSpec")
	}
	if err := likeForLike(&prof, log, []string{"pig", "pi"}, 50); err != nil {
		t.Fatal(err)
	}
	baseline := objectFrom(t, `{"core":"base","warm":80,"cold":100,"piWarm":90}`)
	var history []*object
	for i, v := range []float64{70, 75, 200, 210, 220} {
		history = append(history, objectFrom(t, `{"at":"2026-10-0`+string(rune('1'+i))+`T00:00:00.000Z","rule":"native","turns":50,"values":{"piWarm":`+strings.TrimSuffix(formatNumber(v), ".0")+`}}`))
	}
	slices.Reverse(history) // order on disk does not matter; "at" does
	at := time.Date(2026, 10, 9, 1, 2, 3, 456e6, time.UTC)
	line, err := trackLine(log, trackInput{prof: &prof, turns: 50, run: "/runs/r1", commit: "0123456789", at: at, baseline: baseline, history: history})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"at", "rule", "warmTurns", "core", "turns", "fingerprint", "run", "baseline", "values", "delta", "compare", "disagree", "summary"}; !slices.Equal(line.keys, want) {
		t.Errorf("tracker fields %v, want track-row's %v", line.keys, want)
	}
	values, _ := line.values["values"].MarshalJSON()
	if want := `{"warm":81.5,"warmP95":85.5,"warmMax":85.5,"cold":98.3,"coldP95":100.4,"coldMax":100.7,"seedS":0.6,"peakRssMB":102.2,"piWarm":78.7,"piWarmP95":137.9,"piWarmMax":140.5,"piCold":167.2,"piColdP95":175.7,"piSeedS":1.2,"piPeakRssMB":102.2}`; string(values) != want {
		t.Errorf("values\n got %s\nwant %s", values, want)
	}
	delta, _ := line.values["delta"].MarshalJSON()
	if want := `{"warm":"+2%","warmP95":"n/a","warmMax":"n/a","cold":"-2%","coldP95":"n/a","coldMax":"n/a","seedS":"n/a","peakRssMB":"n/a","piWarm":"-13%","piWarmP95":"n/a","piWarmMax":"n/a","piCold":"n/a","piColdP95":"n/a","piSeedS":"n/a","piPeakRssMB":"n/a"}`; string(delta) != want {
		t.Errorf("delta\n got %s\nwant %s", delta, want)
	}
	var compare map[string]struct {
		Pig, PiRun, PiRolling float64
		NRolling              int
		Run, Rolling          string
		Disagree              bool
	}
	line.get("compare", &compare)
	// The rolling control is the median of the last four history values (75, 200, 210, 220) and this run's 78.7.
	if w := compare["warm"]; w.PiRolling != 200 || w.NRolling != 5 || w.Run != "lose" || w.Rolling != "win" || !w.Disagree {
		t.Errorf("warm compare %+v", w)
	}
	var disagree []string
	line.get("disagree", &disagree)
	if !slices.Equal(disagree, []string{"warm"}) {
		t.Errorf("disagree %v", disagree)
	}
	if line.str("at") != "2026-10-09T01:02:03.456Z" || line.str("baseline") != "base" || line.str("fingerprint") != "b017b487524e44a4" || line.str("core") != "0123456789" {
		t.Errorf("header fields: %s %s %s %s", line.str("at"), line.str("baseline"), line.str("fingerprint"), line.str("core"))
	}
	summary := line.str("summary")
	for _, want := range []string{"Rule native", "node node-a", "cores 4,5", "shared cpuset", "no floor", "warm n=54", "DISAGREE", "pi 1 (1.5 ms)", "Private tracker"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary lacks %q: %s", want, summary)
		}
	}
}

func TestTrackLineNetsTheFloor(t *testing.T) {
	prof := &profile{Name: "p", Rule: "A", Targets: []target{
		{Name: "pig", Role: rolePig, Seed: []string{"s"}, SeedMeta: "m", Sample: []string{"x"}},
		{Name: "pi", Role: roleReference, Seed: []string{"s"}, SeedMeta: "m", Sample: []string{"x"}},
		{Name: "empty", Role: roleFloor, Sample: []string{"x"}},
	}}
	var b strings.Builder
	for _, l := range []string{
		`{"t":"sample","target":"pig","turns":10,"open":10,"turn":[20,30,40]}`,
		`{"t":"sample","target":"pi","turns":10,"open":10,"turn":[30,50,60]}`,
		`{"t":"sample","target":"empty","turns":10,"open":2,"turn":[3,5,7]}`,
	} {
		b.WriteString(l + "\n")
	}
	log, err := parsePodLog(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	line, err := trackLine(log, trackInput{prof: prof, turns: 10, commit: "c", at: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]float64
	line.get("values", &v)
	// Floor: warm p50 of [5, 7] = 6, cold = 5.
	if v["warm"] != 29 || v["cold"] != 25 || v["piWarm"] != 49 || v["floorWarm"] != 6 || v["floorCold"] != 5 {
		t.Errorf("floor netting: %v", v)
	}
	if _, ok := v["seedS"]; ok {
		t.Error("seedS without a seed line must be omitted, not invented")
	}
	if line.str("baseline") != "none" {
		t.Error("no baseline reads none")
	}
}

func TestLikeForLikeRefusesDifferentHistories(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "compare.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(string) string{
		"seed fingerprint": func(s string) string {
			return strings.Replace(s, `"target":"pi","bench_target":"pi","turns":50,"fingerprint":"b017b487524e44a4"`, `"target":"pi","bench_target":"pi","turns":50,"fingerprint":"ffffffffffffffff"`, 1)
		},
		// Both targets agree with each other but not with durable-bench's published history.
		"published fingerprint": func(s string) string {
			return strings.ReplaceAll(s, `"turns":50,"fingerprint":"b017b487524e44a4"`, `"turns":50,"fingerprint":"ffffffffffffffff"`)
		},
		"measured transcript": func(s string) string {
			i := strings.LastIndex(s, `"fingerprint":"b73859cee894aca6"`)
			return s[:i] + `"fingerprint":"0000000000000000"` + s[i+len(`"fingerprint":"b73859cee894aca6"`):]
		},
		"sample count": func(s string) string {
			lines := strings.Split(s, "\n")
			for i, line := range slices.Backward(lines) {
				if strings.Contains(line, `"t":"sample","target":"pig"`) {
					return strings.Join(append(lines[:i:i], lines[i+1:]...), "\n")
				}
			}
			return s
		},
	} {
		log, err := parsePodLog(strings.NewReader(mutate(string(data))))
		if err != nil {
			t.Fatal(err)
		}
		var prof profile
		log.facts.get("profileSpec", &prof)
		if err := likeForLike(&prof, log, []string{"pig", "pi"}, 50); err == nil {
			t.Errorf("%s: a mismatch was accepted", name)
		}
	}
}

func setupRun(t *testing.T, log string) (string, string) {
	t.Helper()
	out := t.TempDir()
	dir := filepath.Join(out, "run1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", log))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pod.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return out, dir
}

// flagsFor gates on wall and CPU times, the gate these testdata runs were written for.
func flagsFor(maxCV float64, calibration string) analysisFlags {
	return flagsGate(gateWall, maxCV, calibration)
}

func flagsGate(gate string, maxCV float64, calibration string) analysisFlags {
	empty, cal := "", calibration
	return analysisFlags{baseline: &empty, history: &empty, track: &empty, calibration: &cal, maxCV: &maxCV, gate: &gate}
}

// The reference's per-sample CV in testdata/compare.jsonl is 1.04% (warm p50) and 4.78% (cold).
func TestCompareRefusesANoisyRun(t *testing.T) {
	out, dir := setupRun(t, "compare.jsonl")
	var stdout bytes.Buffer
	err := analyze(dir, out, flagsFor(0.04, ""), &stdout, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "cold CV 4.8% exceeds 4.0%") {
		t.Fatalf("want a noise refusal naming the cold CV, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "track.jsonl")); !os.IsNotExist(err) {
		t.Error("a refused comparison must not write a tracker line")
	}
	var noise struct {
		Problems []string
		Noise    map[string]map[string]metricNoise
	}
	data, _ := os.ReadFile(filepath.Join(dir, "noise.json"))
	if err := json.Unmarshal(data, &noise); err != nil || len(noise.Problems) != 1 || roundTo(noise.Noise["50"]["warm"].CV, 4) != 0.0104 {
		t.Errorf("noise.json: %v %s", err, data)
	}

	if err := analyze(dir, out, flagsFor(0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	track, _ := os.ReadFile(filepath.Join(out, "track.jsonl"))
	if n := strings.Count(string(track), "\n"); n != 1 {
		t.Fatalf("want one tracker line, got %d", n)
	}
	for _, f := range []string{"results.jsonl", "seeds.jsonl", "ENV.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Error(err)
		}
	}
	results, _ := os.ReadFile(filepath.Join(dir, "results.jsonl"))
	if strings.Count(string(results), "\n") != 12 {
		t.Errorf("results.jsonl holds the 12 sample lines:\n%s", results)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "ENV.txt"))
	if !strings.Contains(string(env), "cpu.max 200000 100000") || !strings.Contains(string(env), "pin quietest 4,5") {
		t.Errorf("ENV.txt:\n%s", env)
	}
}

func TestCompareRefusesANoisyCalibration(t *testing.T) {
	out, dir := setupRun(t, "compare.jsonl")
	cal := filepath.Join(out, "calibration.json")
	write := func(cv float64) {
		data, _ := json.Marshal(map[string]any{"profile": "durable-native", "nodes": []string{"node-a"}, "sizes": map[string]any{"50": map[string]any{"runs": 12, "noise": map[string]any{
			"warm": metricNoise{N: 12, Mean: 100, CV: cv, Gate: true}, "cold": metricNoise{N: 12, Mean: 100, CV: 0.01, Gate: true}, "cpu": metricNoise{N: 12, Mean: 100, CV: 0.01, Gate: true}}}}})
		if err := os.WriteFile(cal, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(0.09)
	err := analyze(dir, out, flagsFor(0.05, cal), &bytes.Buffer{}, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "calibration: warm CV 9.0%") {
		t.Fatalf("want a refusal from the calibration, got %v", err)
	}
	write(0.02)
	if err := analyze(dir, out, flagsFor(0.05, cal), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestCalibrationReport(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "compare.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// The same session as a calibration: the pi samples are the six runs, too few to judge.
	text := strings.Replace(string(data), `"mode":"compare"`, `"mode":"calibrate"`, 1)
	out := t.TempDir()
	dir := filepath.Join(out, "cal")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pod.log"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := analyze(dir, out, flagsFor(0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Delivery, BundleSha256 string
		Artifacts              map[string]string
		Pass                   bool
		Sizes                  map[string]struct {
			Runs   int
			Judged bool
			Noise  map[string]metricNoise
			Nodes  map[string]struct {
				WarmDrift float64
				Throttle  map[string]float64
			}
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "calibration.json"))
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Delivery != "bundle" || report.BundleSha256 != strings.Repeat("e", 64) || report.Artifacts["dist/core-tinygo.wasm"] != strings.Repeat("2", 64) {
		t.Errorf("calibration.json records what ran: %s", raw)
	}
	s := report.Sizes["50"]
	if report.Pass || s.Runs != 6 || roundTo(s.Noise["cold"].CV, 4) != 0.0478 || s.Noise["warm"].Mean != 78.567 || s.Judged || s.Nodes["node-a"].Throttle["throttledSamples"] != 1 || s.Nodes["node-a"].Throttle["throttledMs"] != 1.5 {
		t.Errorf("calibration: %s", raw)
	}
	if _, ok := s.Noise["peakRss"]; !ok || s.Noise["peakRss"].Gate {
		t.Error("peak RSS is reported, not gated")
	}
	if !strings.Contains(stdout.String(), "cold") || !strings.Contains(stdout.String(), "4.78%") || !strings.Contains(stdout.String(), "not judged (50 turns: 6 runs)") {
		t.Errorf("table:\n%s", stdout.String())
	}
	// Six runs are a smoke test: even a noise floor above -max-cv is reported, not judged.
	if err := analyze(dir, out, flagsFor(0.03, ""), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
		t.Fatalf("a smoke calibration is not judged, got %v", err)
	}
}

func TestIncompleteLogsAreRefused(t *testing.T) {
	for name, text := range map[string]string{
		"no facts": `{"t":"done"}`,
		"no done":  `{"t":"facts"}`,
		"error":    `{"t":"facts"}` + "\n" + `{"t":"error","message":"seed failed"}`,
	} {
		log, err := parsePodLog(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		if log.complete() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := parsePodLog(strings.NewReader(`{"t":"sample","target":"x","turns":1,"open":1,"turn":[1]}`)); err == nil {
		t.Error("a sample with one turn has no warm turns and must be refused")
	}
}

func TestReadHistoryKeepsTheSameRuleAndSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "track.jsonl")
	lines := strings.Join([]string{
		`{"at":"1","rule":"native","turns":50,"values":{"piWarm":1}}`,
		`{"at":"2","rule":"A","turns":50,"values":{"piWarm":2}}`,
		`{"at":"3","rule":"native","turns":3500,"values":{"piWarm":3}}`,
		`not json`,
		`{"at":"4","rule":"native","turns":50}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := readHistory([]string{path, filepath.Join(t.TempDir(), "missing.jsonl")}, "native", 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 1 || h[0].str("at") != "1" {
		t.Errorf("history %v", h)
	}
}

func TestObjectKeepsOrder(t *testing.T) {
	o := objectFrom(t, `{"z":1,"a":{"y":2,"b":3},"m":"<&>"}`)
	o.set("a", 4)
	o.set("n", "x")
	data, _ := o.MarshalJSON()
	if string(data) != `{"z":1,"a":4,"m":"<&>","n":"x"}` {
		t.Errorf("got %s", data)
	}
}

// A calibration measures the noise of the node it ran on; it cannot vouch for a comparison on another node.
func TestCompareRefusesACalibrationFromAnotherNode(t *testing.T) {
	out, dir := setupRun(t, "compare.jsonl")
	cal := filepath.Join(out, "calibration.json")
	write := func(node string) {
		data, _ := json.Marshal(map[string]any{"profile": "durable-native", "nodes": []string{node}, "sizes": map[string]any{"50": map[string]any{"runs": 12, "noise": map[string]any{
			"warm": metricNoise{N: 12, Mean: 100, CV: 0.01, Gate: true}, "cold": metricNoise{N: 12, Mean: 100, CV: 0.01, Gate: true}, "cpu": metricNoise{N: 12, Mean: 100, CV: 0.01, Gate: true}}}}})
		if err := os.WriteFile(cal, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("node-b")
	err := analyze(dir, out, flagsFor(0.05, cal), &bytes.Buffer{}, time.Unix(0, 0))
	if !errors.Is(err, errNoisy) || !strings.Contains(err.Error(), "calibrated nodes node-b, not node-a") {
		t.Fatalf("want a refusal naming both nodes, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "track.jsonl")); !os.IsNotExist(err) {
		t.Error("a refused comparison must not write a tracker line")
	}
	write("node-a")
	if err := analyze(dir, out, flagsFor(0.05, cal), &bytes.Buffer{}, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
}

// report -run recomputes a run and appends its line again. The rolling reference median must count each run once
// and never count the run being reported as one of its own past runs.
func TestReportedAgainARunCountsOnceInTheRollingHistory(t *testing.T) {
	out, dir := setupRun(t, "compare.jsonl")
	nRolling := func() int {
		t.Helper()
		var stdout bytes.Buffer
		if err := analyze(dir, out, flagsFor(0.05, ""), &stdout, time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
		line := objectFrom(t, strings.TrimSpace(stdout.String()))
		var compare map[string]struct{ NRolling int }
		line.get("compare", &compare)
		return compare["warm"].NRolling
	}
	if n := nRolling(); n != 1 {
		t.Fatalf("first report: nRolling %d, want 1", n)
	}
	if n := nRolling(); n != 1 {
		t.Errorf("the same run reported again: nRolling %d, want 1 (its earlier line is not a past run)", n)
	}
	// Another run sees the reported run once, however often it was reported.
	other := filepath.Join(out, "run2")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "pod.log"))
	if err := os.WriteFile(filepath.Join(other, "pod.log"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	dir = other
	if n := nRolling(); n != 2 {
		t.Errorf("a later run: nRolling %d, want 2 (one past run)", n)
	}
}

func TestReadHistoryKeepsTheNewestLinePerRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "track.jsonl")
	lines := strings.Join([]string{
		`{"at":"1","rule":"native","turns":50,"run":"/a/out/r1","values":{"piWarm":1}}`,
		`{"at":"2","rule":"native","turns":50,"run":"/b/out/r1","values":{"piWarm":2}}`,
		`{"at":"3","rule":"native","turns":50,"run":"/a/out/r2","values":{"piWarm":3}}`,
		`{"at":"4","rule":"native","turns":50,"run":"/a/out/r3","values":{"piWarm":4}}`,
		`{"at":"5","rule":"native","turns":50,"values":{"piWarm":5}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := readHistory([]string{path}, "native", 50, "/c/r3")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range h {
		got = append(got, o.str("at"))
	}
	if !slices.Equal(got, []string{"2", "3", "5"}) {
		t.Errorf("history %v, want the newest line of r1, r2, and the line without a run; never r3", got)
	}
}
