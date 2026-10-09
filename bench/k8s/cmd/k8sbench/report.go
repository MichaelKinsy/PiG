package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// spreadFile, in a run directory, names the per-node run directories of a spread session.
const spreadFile = "spread.json"

// loadedRun is one collected pod session.
type loadedRun struct {
	dir     string
	log     *podLog
	prof    profile
	turns   []int
	targets []string
	commit  string
	mode    string
	node    string
	cpuSet  string
}

// loadRun reads a collected run directory (pod.log, and cluster.json when the runner collected it) and writes its
// results.jsonl (the sample lines), seeds.jsonl and ENV.txt.
func loadRun(runDir string) (*loadedRun, error) {
	f, err := os.Open(filepath.Join(runDir, "pod.log"))
	if err != nil {
		return nil, err
	}
	log, err := parsePodLog(f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	if err := log.complete(); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(runDir), err)
	}
	var cluster *object
	if data, err := os.ReadFile(filepath.Join(runDir, "cluster.json")); err == nil {
		cluster = &object{}
		if err := json.Unmarshal(data, cluster); err != nil {
			return nil, fmt.Errorf("cluster.json: %w", err)
		}
	}
	if err := writeLines(filepath.Join(runDir, "results.jsonl"), sampleObjects(log)); err != nil {
		return nil, err
	}
	if err := writeLines(filepath.Join(runDir, "seeds.jsonl"), log.seeds); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(runDir, "ENV.txt"), []byte(envText(log, cluster)), 0o644); err != nil {
		return nil, err
	}
	r := &loadedRun{dir: runDir, log: log, mode: log.facts.str("mode"), node: log.facts.str("node"), cpuSet: log.facts.str("cpuSet")}
	if !log.facts.get("turns", &r.turns) || len(r.turns) == 0 {
		return nil, errors.New("facts line has no sizes")
	}
	if !log.facts.get("profileSpec", &r.prof) {
		return nil, errors.New("facts line has no profileSpec")
	}
	if err := r.prof.validate(); err != nil {
		return nil, err
	}
	log.facts.get("targets", &r.targets)
	// The tracker's core is the abbreviated commit, as track-row writes it; a dirty image keeps its marker.
	commit, dirty := strings.CutSuffix(log.facts.str("commit"), "-dirty")
	if len(commit) > 10 {
		commit = commit[:10]
	}
	if dirty {
		commit += "-dirty"
	}
	r.commit = commit
	return r, nil
}

// loadRuns reads a run directory, or every per-node run of a spread session, and checks that they are one session.
func loadRuns(runDir string) ([]*loadedRun, error) {
	dirs := []string{runDir}
	if data, err := os.ReadFile(filepath.Join(runDir, spreadFile)); err == nil {
		var spread struct{ Runs []string }
		if err := json.Unmarshal(data, &spread); err != nil {
			return nil, fmt.Errorf("%s: %w", spreadFile, err)
		}
		if len(spread.Runs) == 0 {
			return nil, fmt.Errorf("%s names no runs", spreadFile)
		}
		dirs = nil
		for _, r := range spread.Runs {
			dirs = append(dirs, filepath.Join(runDir, filepath.Base(r)))
		}
	}
	var runs []*loadedRun
	for _, d := range dirs {
		r, err := loadRun(d)
		if err != nil {
			return nil, err
		}
		if len(runs) > 0 {
			first := runs[0]
			if r.mode != first.mode || r.prof.Name != first.prof.Name || r.commit != first.commit || !slices.Equal(r.turns, first.turns) {
				return nil, fmt.Errorf("%s and %s are not one session (mode, profile, commit or sizes differ)", filepath.Base(first.dir), filepath.Base(d))
			}
		}
		runs = append(runs, r)
	}
	return runs, nil
}

// sessionGates is the gate of a session: auto uses wall times only when every node gave the pod exclusive cores.
func sessionGates(mode string, runs []*loadedRun) ([]string, error) {
	cpuSet := "exclusive"
	for _, r := range runs {
		if r.cpuSet != "exclusive" {
			cpuSet = "shared"
		}
	}
	return gatesFor(mode, cpuSet)
}

// combineNoise is the noise of a spread session: for each metric every node measured, the median over the nodes of
// the mean, standard deviation and CV, and the fewest samples any node had.
func combineNoise(noises []*object, gates []string) *object {
	if len(noises) == 1 {
		return noises[0]
	}
	out := newObject()
	for _, k := range noises[0].keys {
		var means, sds, cvs []float64
		n := -1
		for _, no := range noises {
			var m metricNoise
			if !no.get(k, &m) {
				break
			}
			means, sds, cvs = append(means, m.Mean), append(sds, m.SD), append(cvs, m.CV)
			if n < 0 || m.N < n {
				n = m.N
			}
		}
		if len(cvs) == len(noises) {
			out.set(k, metricNoise{N: n, Mean: roundTo(median(means), 3), SD: roundTo(median(sds), 3), CV: median(cvs), Gate: slices.Contains(gates, k)})
		}
	}
	return out
}

func nodesOf(runs []*loadedRun) []string {
	var out []string
	for _, r := range runs {
		out = append(out, r.node)
	}
	return out
}

// analyze turns a collected run directory into its results: calibration.json for a calibration, or for a
// comparison whose noise floor is acceptable one tracker line per size appended to the tracker file. A spread
// session's directory holds one run directory per node; its values are the medians over the nodes.
func analyze(runDir, outDir string, a analysisFlags, stdout io.Writer, now time.Time) error {
	if err := checkMaxCV(*a.maxCV); err != nil {
		return err
	}
	runs, err := loadRuns(runDir)
	if err != nil {
		return err
	}
	gates, err := sessionGates(*a.gate, runs)
	if err != nil {
		return err
	}
	first := runs[0]
	prof, turns := &first.prof, first.turns
	switch first.mode {
	case modeCalibrate:
		return calibrationReport(runDir, prof, runs, turns, gates, *a.maxCV, stdout, now)
	case modeCompare:
	default:
		return fmt.Errorf("facts line has unknown mode %q", first.mode)
	}

	ref, _ := prof.byRole(roleReference)
	var problems []string
	// A session on cores that scale their clock is reported, never judged: its CPU times follow the clock.
	for _, r := range runs {
		if sc := r.log.scalingCores(); len(sc) > 0 {
			problems = append(problems, fmt.Sprintf("%s: the measured cores scale their clock (CPU %s); a session there is not judged", r.node, strings.Join(sc, ", ")))
		}
	}
	noise := newObject()
	var calib *object
	if *a.calibration != "" {
		var calibProblems []string
		if calib, calibProblems, err = readCalibration(*a.calibration, prof, turns, nodesOf(runs)); err != nil {
			return err
		}
		problems = append(problems, calibProblems...)
	}
	for _, n := range turns {
		var noises []*object
		for _, r := range runs {
			if err := likeForLike(prof, r.log, r.targets, n); err != nil {
				return fmt.Errorf("%s: %w", r.node, err)
			}
			noises = append(noises, noiseOf(r.log.of(ref.Name, n), gates))
		}
		inRun := combineNoise(noises, gates)
		noise.set(fmt.Sprint(n), inRun)
		problems = append(problems, noiseProblems(fmt.Sprintf("%s at %d turns, this run", ref.Name, n), inRun, gates, *a.maxCV)...)
		if calib != nil {
			var sizes map[string]*object
			calib.get("sizes", &sizes)
			c, ok := sizes[fmt.Sprint(n)]
			if !ok {
				continue // readCalibration reported the missing size
			}
			var calRuns int
			if c.get("runs", &calRuns); calRuns < minJudgedRuns {
				continue // readCalibration reported the unjudged size
			}
			cn, ok := calibrationNoise(c, nodesOf(runs), gates)
			if !ok {
				return fmt.Errorf("%s: size %d has no noise", *a.calibration, n)
			}
			problems = append(problems, noiseProblems(fmt.Sprintf("%s at %d turns, calibration", ref.Name, n), cn, gates, *a.maxCV)...)
		}
	}
	verdict := newObject()
	verdict.set("maxCV", *a.maxCV)
	verdict.set("gated", gates)
	verdict.set("nodes", nodesOf(runs))
	verdict.set("noise", noise)
	if *a.calibration != "" {
		verdict.set("calibration", *a.calibration)
	}
	verdict.set("problems", append([]string{}, problems...))
	if err := writeJSON(filepath.Join(runDir, "noise.json"), verdict); err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: no comparison reported for %s:\n  %s", errNoisy, filepath.Base(runDir), strings.Join(problems, "\n  "))
	}

	baselines, err := readBaselines(*a.baseline, prof)
	if err != nil {
		return err
	}
	trackPath := *a.track
	if trackPath == "" {
		trackPath = filepath.Join(outDir, "track.jsonl")
	}
	historyPaths := []string{trackPath}
	if *a.history != "" {
		historyPaths = strings.Split(*a.history, ",")
	}
	var lines []*object
	for _, n := range turns {
		for _, pig := range prof.pigs() {
			rule := prof.ruleOf(pig)
			history, err := readHistory(historyPaths, rule, n, runDir)
			if err != nil {
				return err
			}
			var parts []trackValues
			for _, r := range runs {
				tv, err := computeTrack(prof, pig, r.log, n)
				if err != nil {
					return fmt.Errorf("%s: %w", r.node, err)
				}
				parts = append(parts, tv)
			}
			tv, err := mergeTrack(parts)
			if err != nil {
				return err
			}
			lines = append(lines, buildTrackLine(trackInput{prof: prof, pig: pig, turns: n, run: runDir, commit: first.commit, at: now, baseline: baselines[rule], history: history, gates: gates}, tv))
		}
	}
	if err := os.MkdirAll(filepath.Dir(trackPath), 0o755); err != nil {
		return err
	}
	tf, err := os.OpenFile(trackPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	for _, line := range lines {
		data, _ := line.MarshalJSON()
		if _, err := fmt.Fprintln(tf, string(data)); err != nil {
			_ = tf.Close()
			return err
		}
		_, _ = fmt.Fprintln(stdout, string(data))
	}
	return tf.Close()
}

// calibrationReport writes calibration.json: per size, the coefficient of variation of each metric over the
// reference's runs (the median over the nodes of a spread session, with each node's own), the drift between the first
// and second half of the runs, and the CFS throttling. A size with fewer than minJudgedRuns runs is a smoke test: it
// is reported but not judged. It fails with errNoisy when a judged size's gated metric exceeds maxCV.
func calibrationReport(runDir string, prof *profile, runs []*loadedRun, turns []int, gates []string, maxCV float64, stdout io.Writer, now time.Time) error {
	ref, _ := prof.byRole(roleReference)
	first := runs[0]
	var scaling []string
	for _, r := range runs {
		if sc := r.log.scalingCores(); len(sc) > 0 {
			scaling = append(scaling, r.node+" CPU "+strings.Join(sc, ", "))
		}
	}
	report := newObject()
	report.set("at", now.UTC().Format("2006-01-02T15:04:05.000Z"))
	report.set("run", runDir)
	report.set("profile", prof.Name)
	report.set("rule", prof.Rule)
	report.set("target", ref.Name)
	report.set("nodes", nodesOf(runs))
	var cpuSets, cores []string
	for _, r := range runs {
		cpuSets = append(cpuSets, r.cpuSet)
		if r.log.pin != nil {
			cores = append(cores, r.log.pin.str("cpus"))
		}
	}
	report.set("cpuSets", cpuSets)
	report.set("cpus", cores)
	// What ran: the image or base image, how the artifacts arrived, and each artifact's sha256.
	for _, k := range []string{"commit", "delivery", "image", "bundleSha256", "nodeRuntime", "artifacts"} {
		if raw, ok := first.log.facts.values[k]; ok {
			report.setRaw(k, raw)
		}
	}
	report.set("maxCV", maxCV)
	report.set("gated", gates)
	sizes := newObject()
	var problems, unjudged, nodeLines, noisyNodes []string
	var table strings.Builder
	_, _ = fmt.Fprintf(&table, "%-8s %-11s %4s %12s %10s %8s  %s\n", "turns", "metric", "n", "mean", "sd", "CV", "gate")
	for _, n := range turns {
		var noises []*object
		perNode := newObject()
		fewest := -1
		for _, r := range runs {
			samples := r.log.of(ref.Name, n)
			if seeds := r.log.seedOf(ref.Name, n); len(seeds) == 1 {
				if want := prof.Fingerprints[fmt.Sprint(n)]; want != "" && seeds[0].str("fingerprint") != want {
					return fmt.Errorf("%s seeded %d turns with fingerprint %q on %s, want %s", ref.Name, n, seeds[0].str("fingerprint"), r.node, want)
				}
			}
			no := noiseOf(samples, gates)
			noises = append(noises, no)
			if fewest < 0 || len(samples) < fewest {
				fewest = len(samples)
			}
			nodeEntry := newObject()
			nodeEntry.set("runs", len(samples))
			nodeEntry.set("noise", no)
			// Drift: the warm p50 of the second half of the runs over the first half's. Above about 1.15 the node's
			// load changed during the session.
			if len(samples) >= 4 {
				half := len(samples) / 2
				var firstHalf, secondHalf []float64
				for i, s := range samples {
					if i < half {
						firstHalf = append(firstHalf, s.warm()...)
					} else {
						secondHalf = append(secondHalf, s.warm()...)
					}
				}
				nodeEntry.set("warmDrift", roundTo(median(secondHalf)/median(firstHalf), 4))
			}
			nodeEntry.set("throttle", throttleOf(r.log, ref.Name, n))
			nodeEntry.set("clock", clockOf(r.log, samples))
			// Each node's own verdict: compare judges a session on some of the nodes by their own entries, so a quiet
			// median does not vouch for a noisy node.
			np := noiseProblems(fmt.Sprintf("%s at %d turns on %s", ref.Name, n, r.node), no, gates, maxCV)
			nodeJudged := len(samples) >= minJudgedRuns && len(r.log.scalingCores()) == 0
			nodeEntry.set("pass", nodeJudged && len(np) == 0)
			nodeEntry.set("problems", append([]string{}, np...))
			perNode.set(r.node, nodeEntry)
			{
				var cvs []string
				for _, k := range gates {
					var m metricNoise
					if no.get(k, &m) {
						cvs = append(cvs, fmt.Sprintf("%s %.1f%%", k, 100*m.CV))
					}
				}
				var drift float64
				nodeEntry.get("warmDrift", &drift)
				clockText := ""
				if c := clockOf(r.log, samples); c.has("mhz") {
					var mhz, mhzCV, corr float64
					c.get("mhz", &mhz)
					c.get("mhzCv", &mhzCV)
					c.get("cpuMhzCorrelation", &corr)
					clockText = fmt.Sprintf(", clock %.0f MHz (CV %.1f%%, CPU time vs clock r=%.2f)", mhz, 100*mhzCV, corr)
				}
				if sc := r.log.scalingCores(); len(sc) > 0 {
					clockText += ", scaling governor on CPU " + strings.Join(sc, ", ")
				}
				nodeLines = append(nodeLines, fmt.Sprintf("%-8d %s: %s, warm drift %.3f%s", n, r.node, strings.Join(cvs, ", "), drift, clockText))
				if nodeJudged && len(np) > 0 {
					noisyNodes = append(noisyNodes, np...)
				}
			}
		}
		noise := combineNoise(noises, gates)
		entry := newObject()
		entry.set("runs", fewest)
		entry.set("noise", noise)
		entry.set("nodes", perNode)
		judged := fewest >= minJudgedRuns && len(scaling) == 0
		entry.set("judged", judged)
		p := noiseProblems(fmt.Sprintf("%s at %d turns", ref.Name, n), noise, gates, maxCV)
		entry.set("pass", judged && len(p) == 0)
		if judged {
			problems = append(problems, p...)
		} else {
			why := fmt.Sprintf("%d turns: %d runs", n, fewest)
			if len(scaling) > 0 {
				why = fmt.Sprintf("%d turns: the measured cores scale their clock (%s)", n, strings.Join(scaling, "; "))
			}
			unjudged = append(unjudged, why)
		}
		sizes.set(fmt.Sprint(n), entry)
		for _, k := range noise.keys {
			var m metricNoise
			noise.get(k, &m)
			gate := "info"
			if m.Gate {
				gate = "ok"
				if m.CV > maxCV {
					gate = "OVER"
				}
			}
			_, _ = fmt.Fprintf(&table, "%-8d %-11s %4d %12.3f %10.3f %7.2f%%  %s\n", n, k, m.N, m.Mean, m.SD, 100*m.CV, gate)
		}
	}
	report.set("sizes", sizes)
	report.set("pass", len(problems) == 0 && len(unjudged) == 0)
	report.set("problems", append([]string{}, problems...))
	if err := writeJSON(filepath.Join(runDir, "calibration.json"), report); err != nil {
		return err
	}
	_, _ = fmt.Fprint(stdout, table.String())
	if len(runs) > 1 {
		_, _ = fmt.Fprintf(stdout, "median over %d nodes: %s\n", len(runs), strings.Join(nodesOf(runs), ", "))
	}
	_, _ = fmt.Fprintf(stdout, "per node (gated CVs):\n  %s\n", strings.Join(nodeLines, "\n  "))
	if len(runs) > 1 {
		if len(noisyNodes) > 0 {
			_, _ = fmt.Fprintf(stdout, "noisy on its own (compare judges the median of the calibration entries of the nodes it runs on; leave such a node out of -nodes, or recalibrate it):\n  %s\n", strings.Join(noisyNodes, "\n  "))
		}
	}
	_, _ = fmt.Fprintf(stdout, "gated on %s (%s)\n", strings.Join(gates, ", "), strings.Join(slices.Compact(slices.Sorted(slices.Values(cpuSets))), "/")+" cores")
	_, _ = fmt.Fprintf(stdout, "calibration: %s\n", filepath.Join(runDir, "calibration.json"))
	if len(problems) > 0 {
		return fmt.Errorf("%w (max CV %.1f%%):\n  %s", errNoisy, 100*maxCV, strings.Join(problems, "\n  "))
	}
	if len(unjudged) > 0 {
		_, _ = fmt.Fprintf(stdout, "not judged (%s): a calibration judges the noise floor from %d runs per size; this was a smoke test\n", strings.Join(unjudged, "; "), minJudgedRuns)
	}
	return nil
}

func sampleObjects(log *podLog) []*object {
	out := make([]*object, len(log.samples))
	for i, s := range log.samples {
		out[i] = s.raw
	}
	return out
}

func writeLines(path string, lines []*object) error {
	var b strings.Builder
	for _, o := range lines {
		data, err := o.MarshalJSON()
		if err != nil {
			return err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// envText is the human-readable record of where the session ran, like durable-report's ENV.txt.
func envText(log *podLog, cluster *object) string {
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			_, _ = fmt.Fprintf(&b, "%s %s\n", k, v)
		}
	}
	f := log.facts
	line("run", f.str("run"))
	line("mode", f.str("mode"))
	line("profile", f.str("profile")+" (rule "+f.str("rule")+")")
	line("commit", f.str("commit"))
	line("node", f.str("node"))
	line("pod", f.str("pod"))
	line("kernel", f.str("kernel"))
	line("cpu", f.str("cpuModel"))
	line("cpus online", f.str("cpusOnline"))
	line("cpus allowed", f.str("cpusAllowed")+" ("+f.str("cpuSet")+")")
	line("cpu.max", f.str("cpuMax"))
	line("memory.max", f.str("memoryMax"))
	line("load start", f.str("loadavg"))
	line("helper", f.str("helperGo"))
	line("node runtime", f.str("nodeRuntime"))
	line("delivery", f.str("delivery"))
	line("pod image", f.str("image"))
	line("bundle sha256", f.str("bundleSha256"))
	for _, key := range []string{"versions", "artifacts"} {
		var o object
		if f.get(key, &o) {
			for _, k := range o.keys {
				line(strings.TrimSuffix(key, "s")+" "+k, o.str(k))
			}
		}
	}
	if log.pin != nil {
		line("pin", log.pin.str("method")+" "+log.pin.str("cpus"))
		var g object
		if log.pin.get("governors", &g) {
			for _, k := range g.keys {
				line("governor cpu"+k, g.str(k))
			}
		}
	}
	if cluster != nil {
		if img := cluster.str("image"); img != f.str("image") {
			line("image", img)
		}
		var policy string
		cluster.get("cpuManagerPolicy", &policy)
		if policy == "" || policy == "unreadable" {
			// Without configz, the pod's cpuset tells: only the static policy gives a Guaranteed pod exclusive cores.
			inferred := "none (the pod shares the node's cores; its CPU limit is a CFS quota)"
			if f.str("cpuSet") == "exclusive" {
				inferred = "static (the pod has exclusive cores)"
			}
			policy = "configz unreadable; inferred from the pod's cpuset: " + inferred
		}
		line("cpu manager policy", policy)
		var info object
		if cluster.get("nodeInfo", &info) {
			for _, k := range info.keys {
				line("node "+k, info.str(k))
			}
		} else if cluster.str("nodeInfo") != "" {
			line("node info", cluster.str("nodeInfo"))
		}
	}
	if log.done != nil {
		line("end", log.done.str("at"))
	}
	return b.String()
}

// readBaselines reads the comma-separated -baseline files and assigns each to the tracker rule of a pig target: a
// file with a "rule" field to that rule, a file without one to the only pig target. Two files for one rule, a rule no
// pig target has, and a rule-less file in a profile with several pig targets are refused.
func readBaselines(paths string, prof *profile) (map[string]*object, error) {
	out := map[string]*object{}
	if paths == "" {
		return out, nil
	}
	pigs := prof.pigs()
	for path := range strings.SplitSeq(paths, ",") {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		b := &object{}
		if err := json.Unmarshal(data, b); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		rule := b.str("rule")
		switch {
		case rule == "" && len(pigs) == 1:
			rule = prof.ruleOf(pigs[0])
		case rule == "":
			return nil, fmt.Errorf("-baseline %s has no rule; with %d pig targets each baseline names the rule of its line", path, len(pigs))
		case !slices.ContainsFunc(pigs, func(t target) bool { return prof.ruleOf(t) == rule }):
			return nil, fmt.Errorf("-baseline %s is for rule %s, which no pig target of profile %s has", path, rule, prof.Name)
		}
		if _, dup := out[rule]; dup {
			return nil, fmt.Errorf("-baseline names two files for rule %s", rule)
		}
		out[rule] = b
	}
	return out, nil
}
