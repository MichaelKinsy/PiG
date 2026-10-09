package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// podLog is one session's output as the pod wrote it.
type podLog struct {
	facts   *object
	pin     *object
	seeds   []*object
	samples []sample
	cpu     []*object
	done    *object
	errors  []string
}

type sample struct {
	target      string
	turns       int
	index       int
	open        float64
	turn        []float64
	fingerprint string
	peakRss     *float64
	cpuSeconds  *float64
	// openCPU and turnCPU are the process CPU ms of the open and of each measured turn, when the workload reports them.
	openCPU float64
	turnCPU []float64
	// usageMs is the pod cgroup's CPU time during the sample process (k8sbench's cpu line), when it was readable.
	usageMs *float64
	// mhz is the measured cores' mean clock during the sample (k8sbench's cpu line), when cpufreq reports it.
	mhz *float64
	// bytes is the store size after the measured turns, a deterministic count.
	bytes *float64
	raw   *object
}

// mib is one MiB in MB (10^6 bytes), track-row's memory unit.
const mib = 1 << 20 / 1e6

func (s sample) cold() float64   { return s.open + s.turn[0] }
func (s sample) warm() []float64 { return s.turn[1:] }

// parsePodLog reads the pod's output. Lines that are not JSON objects with a "t" field (kubectl or runtime noise)
// are skipped.
func parsePodLog(r io.Reader) (*podLog, error) {
	log := &podLog{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(text, "{") {
			continue
		}
		o := &object{}
		if json.Unmarshal([]byte(text), o) != nil {
			continue
		}
		switch o.str("t") {
		case "facts":
			log.facts = o
		case "pin":
			log.pin = o
		case "seed":
			log.seeds = append(log.seeds, o)
		case "sample":
			s := sample{target: o.str("target"), raw: o}
			if !o.get("turns", &s.turns) || !o.get("open", &s.open) || !o.get("turn", &s.turn) || len(s.turn) < 2 {
				return nil, fmt.Errorf("sample line without turns, open and at least two turn[] values: %s", text)
			}
			o.get("sample", &s.index)
			s.fingerprint = o.str("fingerprint")
			var v float64
			if o.get("peakRss", &v) {
				s.peakRss = &v
			}
			var c float64
			if o.get("cpuSeconds", &c) {
				s.cpuSeconds = &c
			}
			o.get("openCpu", &s.openCPU)
			o.get("turnCpu", &s.turnCPU)
			var b float64
			if o.get("bytes", &b) {
				s.bytes = &b
			}
			log.samples = append(log.samples, s)
		case "cpu":
			log.cpu = append(log.cpu, o)
		case "done":
			log.done = o
		case "error":
			log.errors = append(log.errors, o.str("message"))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	// Each sample's cgroup CPU time comes from the cpu line the pod wrote around its process.
	usage, clock := map[string]float64{}, map[string]float64{}
	for _, c := range log.cpu {
		var n, i int
		var us, mhz float64
		if c.str("step") == "sample" && c.get("turns", &n) && c.get("sample", &i) {
			key := fmt.Sprintf("%s/%d/%d", c.str("target"), n, i)
			if c.get("usageUsec", &us) {
				usage[key] = us / 1000
			}
			if c.get("mhz", &mhz) {
				clock[key] = mhz
			}
		}
	}
	for i := range log.samples {
		s := &log.samples[i]
		key := fmt.Sprintf("%s/%d/%d", s.target, s.turns, s.index)
		if v, ok := clock[key]; ok {
			s.mhz = &v
		}
		if ms, ok := usage[key]; ok {
			s.usageMs = &ms
		}
	}
	return log, nil
}

// complete reports why a log does not hold a finished session.
func (l *podLog) complete() error {
	if len(l.errors) > 0 {
		return fmt.Errorf("the session failed: %s", strings.Join(l.errors, "; "))
	}
	if l.facts == nil {
		return errors.New("the pod log has no facts line; the session did not start")
	}
	if l.done == nil {
		return errors.New("the pod log has no done line; the session did not finish")
	}
	return nil
}

func (l *podLog) of(target string, turns int) []sample {
	var out []sample
	for _, s := range l.samples {
		if s.target == target && s.turns == turns {
			out = append(out, s)
		}
	}
	return out
}

func (l *podLog) seedOf(target string, turns int) []*object {
	var out []*object
	for _, s := range l.seeds {
		var n int
		if s.str("target") == target && s.get("turns", &n) && n == turns {
			out = append(out, s)
		}
	}
	return out
}

// Statistics as durable-report's track-row.sh computes them.

// quantile interpolates linearly between the closest ranks.
func quantile(xs []float64, p float64) float64 {
	s := slices.Sorted(slices.Values(xs))
	k := float64(len(s)-1) * p
	f := int(math.Floor(k))
	c := min(f+1, len(s)-1)
	return s[f] + (s[c]-s[f])*(k-float64(f))
}

func median(xs []float64) float64 { return quantile(xs, 0.5) }

// jsRound is JavaScript's Math.round: halves round toward positive infinity.
func jsRound(x float64) float64 {
	f := math.Floor(x)
	if x-f >= 0.5 {
		return f + 1
	}
	return f
}

func round1(v float64) float64 { return jsRound(v*10) / 10 }

func roundTo(v float64, digits int) float64 {
	scale := math.Pow(10, float64(digits))
	return math.Round(v*scale) / scale
}

// pct is track-row's delta: "+12%" or "-3%" with Number.prototype.toFixed(0) rounding, "n/a" without a base.
func pct(a float64, b float64, haveB bool) string {
	if !haveB || b == 0 {
		return "n/a"
	}
	v := 100 * (a - b) / b
	if a >= b {
		return "+" + strconv.FormatFloat(math.Round(v), 'f', 0, 64) + "%"
	}
	return "-" + strconv.FormatFloat(math.Round(-v), 'f', 0, 64) + "%"
}

// verdict is the deck's rule: a loss when PiG is more than 3% worse, a win only from 1.3x.
func verdict(pig, pi float64) string {
	switch {
	case pig > pi*1.03:
		return "lose"
	case pi/pig >= 1.3:
		return "win"
	case pig < pi*0.97:
		return "slightly better"
	}
	return "tie"
}

// formatNumber is track-row's fmt: whole numbers from 100, one decimal below, with en-US grouping.
func formatNumber(v float64) string {
	if math.Abs(v) >= 100 {
		v = jsRound(v)
	} else {
		v = round1(v)
	}
	text := strconv.FormatFloat(v, 'f', -1, 64)
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	whole, frac, hasFrac := strings.Cut(text, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if hasFrac {
		return sign + b.String() + "." + frac
	}
	return sign + b.String()
}

func formatTurns(n int) string { return formatNumber(float64(n)) }

// metricNoise is one metric's spread over the samples of one target and size.
type metricNoise struct {
	N    int     `json:"n"`
	Mean float64 `json:"mean"`
	SD   float64 `json:"sd"`
	CV   float64 `json:"cv"`
	Gate bool    `json:"gate"`
}

// Noise gates. A gate is the set of per-sample metrics whose coefficient of variation decides whether a comparison
// is reported. Wall times (warm: the sample's median of turns 2-10; cold: open plus first turn) measure what users
// wait for, but on shared cores a neighbour stretches them without changing the work. CPU times (cpu: the pod
// cgroup's usage during the sample; cpuWarm and cpuCold, the process CPU of the same turns, when the workload reports
// them) and deterministic counts (bytes: the store size after the measured turns) measure the work itself.
const (
	gateAuto = "auto"
	gateWall = "wall"
	gateCPU  = "cpu"
)

var (
	wallMetrics = []string{"warm", "cold"}
	cpuMetrics  = []string{"cpu", "cpuWarm", "cpuCold", "bytes"}
	// requiredMetrics must be measurable when gated; the others are gated only when the workload reports them.
	requiredMetrics = map[string]bool{"warm": true, "cold": true, "cpu": true}
)

// gatesFor is the gate of a run: -gate wall gates wall and CPU times, -gate cpu gates CPU times and counts, and auto
// picks wall on exclusive cores and cpu on shared ones.
func gatesFor(mode, cpuSet string) ([]string, error) {
	switch mode {
	case gateAuto:
		if cpuSet == "exclusive" {
			return slices.Concat(wallMetrics, cpuMetrics), nil
		}
		return slices.Clone(cpuMetrics), nil
	case gateWall:
		return slices.Concat(wallMetrics, cpuMetrics), nil
	case gateCPU:
		return slices.Clone(cpuMetrics), nil
	}
	return nil, fmt.Errorf("-gate %q is not auto, wall or cpu", mode)
}

func spread(xs []float64, gate bool) metricNoise {
	n := len(xs)
	m := metricNoise{N: n, Gate: gate}
	if n == 0 {
		return m
	}
	for _, x := range xs {
		m.Mean += x
	}
	m.Mean /= float64(n)
	if n > 1 {
		var ss float64
		for _, x := range xs {
			ss += (x - m.Mean) * (x - m.Mean)
		}
		m.SD = math.Sqrt(ss / float64(n-1))
	}
	if m.Mean != 0 {
		m.CV = m.SD / m.Mean
	}
	// The CV stays exact: the noise gate compares it with -max-cv, and a rounded 5.0025% would pass a 5% gate.
	m.Mean, m.SD = roundTo(m.Mean, 3), roundTo(m.SD, 3)
	return m
}

// noiseOf is the coefficient of variation of every per-sample metric that every sample has: warm, cold, cpu,
// cpuWarm, cpuCold, cpuSeconds, peakRss and bytes. Gate marks the metrics in gates.
func noiseOf(samples []sample, gates []string) *object {
	o := newObject()
	series := map[string][]float64{}
	missing := map[string]bool{}
	put := func(k string, v *float64) {
		if v == nil {
			missing[k] = true
			return
		}
		series[k] = append(series[k], *v)
	}
	for _, s := range samples {
		warm, cold := median(s.warm()), s.cold()
		put("warm", &warm)
		put("cold", &cold)
		put("cpu", s.usageMs)
		if len(s.turnCPU) == len(s.turn) {
			cw, cc := median(s.turnCPU[1:]), s.openCPU+s.turnCPU[0]
			put("cpuWarm", &cw)
			put("cpuCold", &cc)
		} else {
			put("cpuWarm", nil)
			put("cpuCold", nil)
		}
		put("cpuSeconds", s.cpuSeconds)
		put("peakRss", s.peakRss)
		put("bytes", s.bytes)
	}
	for _, k := range []string{"warm", "cold", "cpu", "cpuWarm", "cpuCold", "cpuSeconds", "peakRss", "bytes"} {
		if !missing[k] && len(series[k]) > 0 {
			o.set(k, spread(series[k], slices.Contains(gates, k)))
		}
	}
	return o
}

// minNoiseSamples is the fewest samples of one target and size whose CV the noise gate accepts.
const minNoiseSamples = 3

// minJudgedRuns is the fewest calibration runs at one size that judge a noise floor; fewer are a smoke test.
const minJudgedRuns = 12

// checkMaxCV refuses a threshold that no CV can exceed (NaN, infinity) or that every CV exceeds.
func checkMaxCV(maxCV float64) error {
	if math.IsNaN(maxCV) || math.IsInf(maxCV, 0) || maxCV <= 0 {
		return fmt.Errorf("-max-cv %v: want a positive, finite coefficient of variation such as 0.05", maxCV)
	}
	return nil
}

// noiseProblems lists every gated metric whose CV exceeds maxCV or that is zero in every sample, a required one that
// was not measured, and too few samples to measure one.
func noiseProblems(label string, noise *object, gates []string, maxCV float64) []string {
	var out []string
	for _, k := range gates {
		var m metricNoise
		if !noise.get(k, &m) {
			if requiredMetrics[k] {
				out = append(out, fmt.Sprintf("%s: no %s samples", label, k))
			}
			continue
		}
		if m.N < minNoiseSamples {
			out = append(out, fmt.Sprintf("%s: %s has %d samples; the noise floor needs at least %d", label, k, m.N, minNoiseSamples))
			continue
		}
		// Every gated metric is a positive time or size; zero in every sample means it was not measured, and its CV
		// of 0 says nothing about the noise.
		if m.Mean <= 0 {
			out = append(out, fmt.Sprintf("%s: %s is zero in every sample; it was not measured", label, k))
			continue
		}
		if m.CV > maxCV {
			cv := fmt.Sprintf("%.1f", 100*m.CV)
			if limit := fmt.Sprintf("%.1f", 100*maxCV); cv == limit {
				cv = fmt.Sprintf("%.3f", 100*m.CV)
			}
			out = append(out, fmt.Sprintf("%s: %s CV %s%% exceeds %.1f%%", label, k, cv, 100*maxCV))
		}
	}
	return out
}

// readCalibration reads a calibration file for a comparison of prof at turns on nodes and lists why it cannot vouch
// for that comparison: it names no node, it did not measure one of nodes, or it did not calibrate or did not judge
// (fewer than minJudgedRuns runs) one of the sizes. A calibration of another profile is an error.
func readCalibration(path string, prof *profile, turns []int, nodes []string) (*object, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	calib := &object{}
	if err := json.Unmarshal(data, calib); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if calib.str("profile") != prof.Name {
		return nil, nil, fmt.Errorf("%s calibrated profile %s, not %s", path, calib.str("profile"), prof.Name)
	}
	var problems []string
	// A calibration measures the noise of the nodes it ran on.
	var calibNodes []string
	calib.get("nodes", &calibNodes)
	if len(calibNodes) == 0 {
		problems = append(problems, fmt.Sprintf("%s names no node, so it cannot vouch for the nodes this comparison runs on", path))
	}
	for _, n := range nodes {
		switch {
		case len(calibNodes) == 0:
		case n == "":
			problems = append(problems, fmt.Sprintf("this run did not record its node, so %s cannot vouch for it", path))
		case !slices.Contains(calibNodes, n):
			problems = append(problems, fmt.Sprintf("%s calibrated nodes %s, not %s, where this comparison runs; calibrate on the same nodes (-node, -nodes)", path, strings.Join(calibNodes, ", "), n))
		}
	}
	var sizes map[string]*object
	calib.get("sizes", &sizes)
	for _, n := range turns {
		c, ok := sizes[strconv.Itoa(n)]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s has no calibration at %d turns", path, n))
			continue
		}
		var calRuns int
		c.get("runs", &calRuns)
		if calRuns < minJudgedRuns {
			problems = append(problems, fmt.Sprintf("%s has %d runs at %d turns; a calibration judges the noise floor from %d (-runs %d)", path, calRuns, n, minJudgedRuns, minJudgedRuns))
		}
		// A node calibrated on cores that scale their clock (-allow-frequency-scaling) measured the clock, not the noise
		// floor, so its entry cannot vouch for this comparison.
		var perNode map[string]*object
		c.get("nodes", &perNode)
		for _, node := range nodes {
			entry := perNode[node]
			clock := &object{}
			if entry == nil || !entry.get("clock", clock) {
				continue
			}
			var scaling []string
			if clock.get("scaling", &scaling); len(scaling) > 0 {
				problems = append(problems, fmt.Sprintf("%s calibrated %s at %d turns on cores that scale their clock (CPU %s), so it cannot vouch for this comparison: calibrate on fixed-clock cores", path, node, n, strings.Join(scaling, ", ")))
			}
		}
	}
	return calib, problems, nil
}

// calibrationNoise is the noise a calibrated size measured on nodes: the median over those nodes' own entries, so a
// spread calibration's quiet median does not vouch for its noisy node, or the calibration's noise when it has no
// entry for one of them.
func calibrationNoise(size *object, nodes []string, gates []string) (*object, bool) {
	var perNode map[string]*object
	size.get("nodes", &perNode)
	var noises []*object
	for _, n := range nodes {
		entry, ok := perNode[n]
		no := &object{}
		if !ok || entry == nil || !entry.get("noise", no) {
			noises = nil
			break
		}
		noises = append(noises, no)
	}
	if len(noises) > 0 {
		return combineNoise(noises, gates), true
	}
	no := &object{}
	return no, size.get("noise", no)
}

// throttleOf sums the CFS throttling the cgroup recorded during one target's measured samples at one size.
func throttleOf(log *podLog, target string, turns int) *object {
	var steps, throttled, periods, usec int64
	measured := false
	for _, c := range log.cpu {
		var n int
		if c.str("step") != "sample" || c.str("target") != target || !c.get("turns", &n) || n != turns {
			continue
		}
		steps++
		var nr, np, us int64
		if c.get("nrThrottled", &nr) {
			measured = true
		}
		c.get("nrPeriods", &np)
		c.get("throttledUsec", &us)
		if nr > 0 {
			throttled++
		}
		periods += np
		usec += us
	}
	o := newObject()
	o.set("samples", steps)
	if measured {
		o.set("throttledSamples", throttled)
		o.set("periods", periods)
		o.set("throttledMs", roundTo(float64(usec)/1000, 3))
	}
	return o
}

// likeForLike checks that every compared target ran the same history: each seed reproduces the profile's
// fingerprint for its size and every target's seeds and measured transcripts agree.
func likeForLike(prof *profile, log *podLog, targets []string, turns int) error {
	var seedPrints, samplePrints []string
	var counts []int
	for _, name := range targets {
		t, _ := prof.byName(name)
		if t.Role == roleFloor {
			continue
		}
		seeds := log.seedOf(name, turns)
		if len(seeds) != 1 {
			return fmt.Errorf("%s has %d seed lines at %d turns, want 1", name, len(seeds), turns)
		}
		fp := seeds[0].str("fingerprint")
		if want := prof.Fingerprints[strconv.Itoa(turns)]; want != "" && fp != want {
			return fmt.Errorf("%s seeded %d turns with fingerprint %q, want %s", name, turns, fp, want)
		}
		seedPrints = append(seedPrints, name+"="+fp)
		samples := log.of(name, turns)
		counts = append(counts, len(samples))
		for _, s := range samples {
			if s.fingerprint != "" {
				samplePrints = append(samplePrints, s.fingerprint)
			}
		}
	}
	for i := 1; i < len(seedPrints); i++ {
		_, a, _ := strings.Cut(seedPrints[0], "=")
		_, b, _ := strings.Cut(seedPrints[i], "=")
		if a != b {
			return fmt.Errorf("seed fingerprints differ at %d turns: %s", turns, strings.Join(seedPrints, ", "))
		}
	}
	for _, fp := range samplePrints {
		if fp != samplePrints[0] {
			return fmt.Errorf("measured transcripts differ at %d turns: fingerprints %s and %s", turns, samplePrints[0], fp)
		}
	}
	for _, n := range counts {
		if n != counts[0] || n == 0 {
			return fmt.Errorf("targets have unequal sample counts at %d turns: %v", turns, counts)
		}
	}
	return nil
}

// trackInput is everything one tracker line is built from besides its measured values: computeTrack computes those
// from the pig, reference and optional floor samples of one size (warm, every turn after the first, pooled; cold, open
// plus first turn; p50, p95 and max net of the floor's p50; CPU per warm turn and cold CPU when the workload reports
// them; seed time), and buildTrackLine adds the deltas against the baseline and judges each PiG metric against the
// reference of this run and the rolling median of the reference over the last five runs, as durable-report's
// track-row.sh does.
type trackInput struct {
	prof *profile
	// pig is the pig target the line compares with the reference.
	pig      target
	turns    int
	run      string
	commit   string
	at       time.Time
	baseline *object
	history  []*object
	// gates are the noise-gated metrics the run passed; wall times are informational when they are not among them.
	gates []string
}

type kv struct {
	k string
	v float64
}

// trackValues are one size's measured values and the facts the summary names, from one pod or, for a spread run,
// the median over several.
type trackValues struct {
	r           []kv
	warmN       int
	fingerprint string
	hasFloor    bool
	floorWarm   float64
	nodes       []string
	// machine is the node type the samples ran on: CPU model, Go vector features and GOMAXPROCS (machineOf).
	machine           string
	cores             []string
	cpuSets           []string
	throttleReadable  bool
	throttled, refThr [2]float64 // samples, ms
}

func (t trackValues) value(k string) (float64, bool) {
	for _, e := range t.r {
		if e.k == k {
			return e.v, true
		}
	}
	return 0, false
}

func computeTrack(prof *profile, pig target, log *podLog, turns int) (trackValues, error) {
	var tv trackValues
	ref, _ := prof.byRole(roleReference)
	floorT, hasFloor := prof.byRole(roleFloor)
	pool := func(name string, kind string) []float64 {
		var out []float64
		for _, s := range log.of(name, turns) {
			switch kind {
			case "warm":
				out = append(out, s.warm()...)
			case "cold":
				out = append(out, s.cold())
			case "cpuWarm":
				out = append(out, s.turnCPU[1:]...)
			case "cpuCold":
				out = append(out, s.openCPU+s.turnCPU[0])
			}
		}
		return out
	}
	for _, name := range []string{pig.Name, ref.Name} {
		if len(log.of(name, turns)) == 0 {
			return tv, fmt.Errorf("no %s samples at %d turns", name, turns)
		}
	}
	floor := map[string]float64{"warm": 0, "cold": 0}
	if hasFloor {
		if len(log.of(floorT.Name, turns)) == 0 {
			return tv, fmt.Errorf("no %s (floor) samples at %d turns", floorT.Name, turns)
		}
		floor["warm"], floor["cold"] = median(pool(floorT.Name, "warm")), median(pool(floorT.Name, "cold"))
	}
	net := func(kind, name string) []float64 {
		raw := pool(name, kind)
		out := make([]float64, len(raw))
		for i, v := range raw {
			out[i] = v - floor[kind]
		}
		return out
	}
	seedS := func(name string) (float64, bool) {
		seeds := log.seedOf(name, turns)
		var ms float64
		if len(seeds) != 1 || !seeds[0].get("seedMs", &ms) {
			return 0, false
		}
		return ms / 1000, true
	}
	// CPU per turn exists only when every sample of both targets reports it.
	hasCPU := func(name string) bool {
		for _, s := range log.of(name, turns) {
			if len(s.turnCPU) != len(s.turn) {
				return false
			}
		}
		return true
	}
	cpu := hasCPU(pig.Name) && hasCPU(ref.Name)
	// A memory measure exists only when every sample of both targets reports it; its value is the median over the
	// samples, each a fresh process. The workloads report peakRss and retained (the memory the open store keeps after
	// a forced collection after the last measured turn) in MiB, as their rss; the tracker carries them in track-row's
	// unit, MB (10^6 bytes). growthPerTurn is bytes per measured turn.
	every := func(name, key string) ([]float64, bool) {
		var out []float64
		for _, s := range log.of(name, turns) {
			var v float64
			if s.raw == nil || !s.raw.get(key, &v) {
				return nil, false
			}
			out = append(out, v)
		}
		return out, len(out) > 0
	}
	both := func(key string) (pigV, refV float64, ok bool) {
		p, ok1 := every(pig.Name, key)
		r, ok2 := every(ref.Name, key)
		if !ok1 || !ok2 {
			return 0, 0, false
		}
		return median(p), median(r), true
	}
	pigRSS, refRSS, memory := both("peakRss")
	pigRetained, refRetained, retained := both("retained")
	pigGrowth, refGrowth, growth := both("growthPerTurn")
	add := func(k string, v float64) { tv.r = append(tv.r, kv{k, v}) }
	add("warm", median(net("warm", pig.Name)))
	add("warmP95", quantile(net("warm", pig.Name), 0.95))
	add("warmMax", slices.Max(net("warm", pig.Name)))
	add("cold", median(net("cold", pig.Name)))
	add("coldP95", quantile(net("cold", pig.Name), 0.95))
	add("coldMax", slices.Max(net("cold", pig.Name)))
	if v, ok := seedS(pig.Name); ok {
		add("seedS", v)
	}
	if cpu {
		add("cpuWarm", median(pool(pig.Name, "cpuWarm")))
		add("cpuCold", median(pool(pig.Name, "cpuCold")))
	}
	if retained {
		add("retainedMB", pigRetained*mib)
	}
	if growth {
		add("growthPerTurn", pigGrowth)
	}
	if memory {
		add("peakRssMB", pigRSS*mib)
	}
	add("piWarm", median(net("warm", ref.Name)))
	add("piWarmP95", quantile(net("warm", ref.Name), 0.95))
	add("piWarmMax", slices.Max(net("warm", ref.Name)))
	add("piCold", median(net("cold", ref.Name)))
	add("piColdP95", quantile(net("cold", ref.Name), 0.95))
	if v, ok := seedS(ref.Name); ok {
		add("piSeedS", v)
	}
	if cpu {
		add("piCpuWarm", median(pool(ref.Name, "cpuWarm")))
		add("piCpuCold", median(pool(ref.Name, "cpuCold")))
	}
	if retained {
		add("piRetainedMB", refRetained*mib)
	}
	if growth {
		add("piGrowthPerTurn", refGrowth)
	}
	if memory {
		add("piPeakRssMB", refRSS*mib)
	}
	if hasFloor {
		add("floorWarm", floor["warm"])
		add("floorCold", floor["cold"])
	}
	tv.hasFloor, tv.floorWarm = hasFloor, floor["warm"]
	if seeds := log.seedOf(pig.Name, turns); len(seeds) == 1 {
		tv.fingerprint = seeds[0].str("fingerprint")
	}
	tv.warmN = len(pool(pig.Name, "warm"))
	if log.facts != nil {
		tv.nodes, tv.cpuSets = []string{log.facts.str("node")}, []string{log.facts.str("cpuSet")}
	}
	if log.pin != nil {
		tv.cores = []string{log.pin.str("cpus")}
	}
	machine, err := machineOf(log)
	if err != nil {
		return tv, err
	}
	tv.machine = machine
	throttle, refThrottle := throttleOf(log, pig.Name, turns), throttleOf(log, ref.Name, turns)
	if throttle.get("throttledSamples", &tv.throttled[0]) && refThrottle.get("throttledSamples", &tv.refThr[0]) {
		tv.throttleReadable = true
		throttle.get("throttledMs", &tv.throttled[1])
		refThrottle.get("throttledMs", &tv.refThr[1])
	}
	return tv, nil
}

// mergeTrack combines the values of one size measured on several nodes: each value is the median over the nodes
// that have it on every node, the warm-turn counts and throttling add up, and the history must be the same.
func mergeTrack(parts []trackValues) (trackValues, error) {
	if len(parts) == 1 {
		return parts[0], nil
	}
	var m trackValues
	m.hasFloor, m.throttleReadable = parts[0].hasFloor, true
	m.fingerprint = parts[0].fingerprint
	for _, e := range parts[0].r {
		var xs []float64
		for _, p := range parts {
			if v, ok := p.value(e.k); ok {
				xs = append(xs, v)
			}
		}
		if len(xs) == len(parts) {
			m.r = append(m.r, kv{e.k, median(xs)})
		}
	}
	var floors []float64
	m.machine = parts[0].machine
	for _, p := range parts {
		if p.fingerprint != m.fingerprint {
			return m, fmt.Errorf("the nodes seeded different histories: fingerprints %s and %s", m.fingerprint, p.fingerprint)
		}
		// A Go program's CPU time follows the CPU's vector features (the Green Tea collector's vector path) and
		// GOMAXPROCS: a median over unlike nodes would mix two programs.
		if p.machine != m.machine {
			return m, fmt.Errorf("the nodes are of different types: %s and %s; spread over nodes of one type", m.machine, p.machine)
		}
		m.warmN += p.warmN
		floors = append(floors, p.floorWarm)
		m.nodes = append(m.nodes, p.nodes...)
		m.cores = append(m.cores, p.cores...)
		m.cpuSets = append(m.cpuSets, p.cpuSets...)
		m.throttleReadable = m.throttleReadable && p.throttleReadable
		for i := range 2 {
			m.throttled[i] += p.throttled[i]
			m.refThr[i] += p.refThr[i]
		}
	}
	m.floorWarm = median(floors)
	return m, nil
}

func buildTrackLine(in trackInput, tv trackValues) *object {
	pig := in.pig
	rule := in.prof.ruleOf(pig)
	ref, _ := in.prof.byRole(roleReference)
	baselineCore := "none"
	if in.baseline != nil {
		if c := in.baseline.str("core"); c != "" {
			baselineCore = c
		}
	}
	values, delta := newObject(), newObject()
	for _, e := range tv.r {
		values.set(e.k, round1(e.v))
		var b float64
		have := in.baseline != nil && in.baseline.get(e.k, &b)
		delta.set(e.k, pct(e.v, b, have))
	}

	// PiG metric -> its reference control, in track-row's order.
	pairs := [][2]string{{"warm", "piWarm"}, {"warmP95", "piWarmP95"}, {"cold", "piCold"}, {"coldP95", "piColdP95"}, {"cpuWarm", "piCpuWarm"}, {"cpuCold", "piCpuCold"}, {"retainedMB", "piRetainedMB"}, {"peakRssMB", "piPeakRssMB"}, {"seedS", "piSeedS"}}
	names := map[string]string{"warm": "warm p50", "warmP95": "warm p95", "cold": "cold p50", "coldP95": "cold p95", "cpuWarm": "CPU/warm turn", "cpuCold": "CPU cold", "retainedMB": "retained MB", "peakRssMB": "peak RSS MB", "seedS": "seed s"}
	history := slices.Clone(in.history)
	slices.SortStableFunc(history, func(a, b *object) int { return strings.Compare(a.str("at"), b.str("at")) })
	compare := newObject()
	var disagree []string
	var parts []string
	nRolling := 0
	for _, p := range pairs {
		pigV, ok1 := tv.value(p[0])
		piV, ok2 := tv.value(p[1])
		if !ok1 || !ok2 {
			continue
		}
		var past []float64
		for _, h := range history {
			var hv map[string]any
			if !h.get("values", &hv) {
				continue
			}
			if n, ok := hv[p[1]].(float64); ok {
				past = append(past, n)
			}
		}
		if len(past) > 4 {
			past = past[len(past)-4:]
		}
		rolling := median(append(past, piV))
		a, b := verdict(pigV, piV), verdict(pigV, rolling)
		dis := (a == "win") != (b == "win") || (a == "lose") != (b == "lose")
		c := newObject()
		c.set("pig", round1(pigV))
		c.set("piRun", round1(piV))
		c.set("piRolling", round1(rolling))
		c.set("nRolling", len(past)+1)
		c.set("run", a)
		c.set("rolling", b)
		c.set("disagree", dis)
		compare.set(p[0], c)
		if p[0] == "warm" {
			nRolling = len(past) + 1
		}
		if dis {
			disagree = append(disagree, p[0])
		}
		side := a
		if dis {
			side += " / rolling " + b
		}
		parts = append(parts, fmt.Sprintf("%s %s vs %s (%s) %s", names[p[0]], formatNumber(pigV), formatNumber(piV), formatNumber(rolling), side))
	}
	// Growth per turn is judged against zero, not against the reference, so it is reported without a verdict.
	var pigGrowth, piGrowth float64
	if values.get("growthPerTurn", &pigGrowth) && values.get("piGrowthPerTurn", &piGrowth) {
		parts = append(parts, fmt.Sprintf("heap growth/turn %s B vs %s B", formatNumber(pigGrowth), formatNumber(piGrowth)))
	}
	if disagree == nil {
		disagree = []string{}
	}

	floorText := "no floor"
	if tv.hasFloor {
		floorText = "net of floor " + formatNumber(round1(tv.floorWarm)) + " ms"
	}
	throttleText := "throttling not readable"
	if tv.throttleReadable {
		throttleText = fmt.Sprintf("CFS-throttled samples: %s %d (%s ms), %s %d (%s ms)", pig.Name, int(tv.throttled[0]), formatNumber(tv.throttled[1]), ref.Name, int(tv.refThr[0]), formatNumber(tv.refThr[1]))
	}
	var deltaParts []string
	for _, k := range []string{"warm", "cold"} {
		deltaParts = append(deltaParts, fmt.Sprintf("%s %s", map[string]string{"warm": "warm p50", "cold": "cold"}[k], delta.str(k)))
	}
	agree := "Same-run and rolling pi agree on every win/lose."
	if len(disagree) > 0 {
		var ds []string
		for _, k := range disagree {
			ds = append(ds, names[k])
		}
		agree = "DISAGREE (same-run vs rolling pi): " + strings.Join(ds, ", ") + "."
	}
	where := fmt.Sprintf("on Kubernetes node %s, cores %s (%s cpuset", strings.Join(tv.nodes, ""), strings.Join(tv.cores, ""), strings.Join(tv.cpuSets, ""))
	if len(tv.nodes) > 1 {
		where = fmt.Sprintf("median of %d Kubernetes nodes %s (%s cpusets", len(tv.nodes), strings.Join(tv.nodes, ", "), strings.Join(slices.Compact(slices.Sorted(slices.Values(tv.cpuSets))), "/"))
	}
	gateText := ""
	if len(in.gates) > 0 {
		gateText = "; noise gate on " + strings.Join(in.gates, ", ")
		if !slices.Contains(in.gates, "warm") {
			gateText += ", wall times informational"
		}
	}
	versus := "PiG vs pi"
	if len(in.prof.pigs()) > 1 {
		versus = fmt.Sprintf("PiG %s vs %s", pig.Name, ref.Name)
	}
	summary := fmt.Sprintf("Rule %s, %s %s turns at %s %s; %s; warm n=%d%s). %s same run (rolling median of last %d pi): %s. vs %s: %s. %s. %s fingerprint %s. Private tracker, not deck numbers.",
		rule, in.prof.Name, formatTurns(in.turns), in.commit, where, floorText, tv.warmN, gateText, versus, nRolling, strings.Join(parts, "; "), baselineCore, strings.Join(deltaParts, ", "), throttleText, agree, tv.fingerprint)

	line := newObject()
	line.set("at", in.at.UTC().Format("2006-01-02T15:04:05.000Z"))
	line.set("rule", rule)
	line.set("warmTurns", tv.warmN)
	line.set("core", in.commit)
	line.set("turns", in.turns)
	line.set("fingerprint", tv.fingerprint)
	line.set("run", in.run)
	line.set("baseline", baselineCore)
	line.set("values", values)
	line.set("delta", delta)
	line.set("compare", compare)
	line.set("disagree", disagree)
	line.set("summary", summary)
	return line
}

// readHistory reads earlier tracker lines of the same rule and size from JSON Lines files; missing files are skipped.
// A run is named by the base of its "run" directory. report -run appends a recomputed line for a run that already has
// one, so only the newest line of each run counts, and the lines of run itself, the run being reported, never do.
func readHistory(paths []string, rule string, turns int, run string) ([]*object, error) {
	var out []*object
	newest := map[string]int{}
	for _, path := range paths {
		f, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64<<10), 16<<20)
		for scanner.Scan() {
			o := &object{}
			if json.Unmarshal(scanner.Bytes(), o) != nil {
				continue
			}
			var n int
			if o.str("rule") != rule || !o.get("turns", &n) || n != turns || !o.has("values") {
				continue
			}
			id := o.str("run")
			if id == "" {
				out = append(out, o)
				continue
			}
			id = filepath.Base(id)
			if id == filepath.Base(run) {
				continue
			}
			i, seen := newest[id]
			switch {
			case !seen:
				newest[id] = len(out)
				out = append(out, o)
			case o.str("at") >= out[i].str("at"):
				out[i] = o
			}
		}
		err = scanner.Err()
		_ = f.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// scalingCores lists the measured cores whose cpufreq governor scales the clock with load, from the pod's pin line.
func (l *podLog) scalingCores() []string {
	var out []string
	if l.pin == nil {
		return nil
	}
	var governors object
	if !l.pin.get("governors", &governors) {
		return nil
	}
	for _, c := range governors.keys {
		g := governors.str(c)
		if !fixedClock(g) {
			out = append(out, fmt.Sprintf("%s (%s)", c, g))
		}
	}
	return out
}

// clockOf describes the measured cores' clock during one target's samples at one size: their governors, the mean
// sampled clock, its CV over the samples, and how closely each sample's cgroup CPU time follows its clock (Pearson
// correlation; strongly negative when a slower clock makes the same work take more CPU time).
func clockOf(log *podLog, samples []sample) *object {
	o := newObject()
	if log.pin != nil {
		var governors object
		if log.pin.get("governors", &governors) {
			o.set("governors", &governors)
		}
	}
	o.set("scaling", append([]string{}, log.scalingCores()...))
	var mhz, cpu []float64
	for _, s := range samples {
		if s.mhz != nil && s.usageMs != nil {
			mhz, cpu = append(mhz, *s.mhz), append(cpu, *s.usageMs)
		}
	}
	if len(mhz) == len(samples) && len(mhz) >= minNoiseSamples {
		m := spread(mhz, false)
		o.set("mhz", m.Mean)
		o.set("mhzCv", m.CV)
		o.set("cpuMhzCorrelation", roundTo(correlation(cpu, mhz), 3))
	}
	return o
}

func correlation(a, b []float64) float64 {
	ma, mb := spread(a, false).Mean, spread(b, false).Mean
	var sab, saa, sbb float64
	for i := range a {
		sab += (a[i] - ma) * (b[i] - mb)
		saa += (a[i] - ma) * (a[i] - ma)
		sbb += (b[i] - mb) * (b[i] - mb)
	}
	if saa == 0 || sbb == 0 {
		return 0
	}
	return sab / math.Sqrt(saa*sbb)
}

// machineOf names the node type a pod's samples ran on: the CPU model, the Go vector features and GOMAXPROCS, which every
// seed and sample line carries. All of them must agree; a log from a pod that does not record them names none.
func machineOf(log *podLog) (string, error) {
	machine, seen := "", false
	for _, s := range log.samples {
		var gmp int
		if s.raw == nil || !s.raw.has("gomaxprocs") {
			continue
		}
		s.raw.get("gomaxprocs", &gmp)
		m := fmt.Sprintf("%s [%s] GOMAXPROCS %d", s.raw.str("cpuModel"), s.raw.str("cpuFeatures"), gmp)
		if seen && m != machine {
			return "", fmt.Errorf("the samples ran on different node types or GOMAXPROCS: %s and %s", machine, m)
		}
		machine, seen = m, true
	}
	return machine, nil
}
