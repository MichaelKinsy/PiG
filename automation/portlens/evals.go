// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Evals is the data of the evals and benchmarks view.
type Evals struct {
	Bar        DurableBar
	Gate       *ContractGate
	Scoreboard Scoreboard
	Matrices   []Matrix
}

// ---- Durable bar ----

// DurableBar is the four-item bar of success for the Durable core, evaluated from committed data.
type DurableBar struct {
	Sizes    []int
	Arms     []DurableArm
	Items    []BarItem
	Baseline BaselineInfo
	// Cluster lists the committed cluster compare result files.
	Cluster []string
}

// BaselineInfo is the Durable contract baseline file (known differences from Pi).
type BaselineInfo struct {
	Path    string
	Entries int
}

// DurableArm is the medians of one results file.
type DurableArm struct {
	Name     string
	File     string
	Pi       bool
	Headline bool
	Cells    map[int]ArmCell
}

// ArmCell is the median over the samples of one size.
type ArmCell struct {
	Samples int
	ColdMs  float64
	WarmMs  float64
	CPUSec  float64
	RSSMB   float64
}

// BarItem is one of the bar's conditions with its verdict.
type BarItem struct {
	N         int
	Title     string
	Criterion string
	Met       bool
	Verdict   string
	// Cells lists per size verdicts where the item is judged per size.
	Cells []BarCell
}

// BarCell is a per-measure, per-size verdict.
type BarCell struct {
	Measure string
	Size    int
	Met     bool
	PiVal   float64
	PigVal  float64
	Unit    string
}

type durableSample struct {
	Target     string    `json:"target"`
	Variant    string    `json:"variant"`
	Turns      int       `json:"turns"`
	Open       float64   `json:"open"`
	Turn       []float64 `json:"turn"`
	RSS        float64   `json:"rss"`
	PeakRSS    float64   `json:"peakRss"`
	CPUSeconds float64   `json:"cpuSeconds"`
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

const (
	headlineArm = "pig-walk-faux-default-procs"
	piArm       = "pi-durable-1.0.4-node"
	// A flat memory curve grows less than this many KiB per turn between 1,000 and 3,500 turns.
	flatKiBPerTurn = 1.0
)

func loadDurableBar(root string) DurableBar {
	bar := DurableBar{Sizes: []int{50, 250, 1000, 3500}}
	dir := filepath.Join(root, "bench", "durableperf", "results")
	files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	sort.Strings(files)
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		arm := DurableArm{Name: name, File: filepath.ToSlash(mustRel(root, f)), Pi: name == piArm, Headline: name == headlineArm, Cells: map[int]ArmCell{}}
		by := map[int][]durableSample{}
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var s durableSample
			if json.Unmarshal(sc.Bytes(), &s) == nil && s.Turns > 0 && len(s.Turn) > 1 {
				by[s.Turns] = append(by[s.Turns], s)
			}
		}
		_ = fh.Close()
		for turns, ss := range by {
			var cold, warm, cpu, rss []float64
			for _, s := range ss {
				cold = append(cold, s.Open+s.Turn[0])
				warm = append(warm, median(s.Turn[1:]))
				cpu = append(cpu, s.CPUSeconds)
				r := s.PeakRSS
				if r == 0 {
					r = s.RSS
				}
				rss = append(rss, r)
			}
			arm.Cells[turns] = ArmCell{Samples: len(ss), ColdMs: median(cold), WarmMs: median(warm), CPUSec: median(cpu), RSSMB: median(rss)}
		}
		bar.Arms = append(bar.Arms, arm)
	}
	sort.SliceStable(bar.Arms, func(i, j int) bool {
		a, b := bar.Arms[i], bar.Arms[j]
		if a.Pi != b.Pi {
			return a.Pi
		}
		if a.Headline != b.Headline {
			return a.Headline
		}
		return a.Name < b.Name
	})
	bar.Baseline = loadBaselineEntries(root)
	cluster, _ := filepath.Glob(filepath.Join(root, "bench", "k8s", "results", "*.jsonl"))
	for _, c := range cluster {
		bar.Cluster = append(bar.Cluster, filepath.ToSlash(mustRel(root, c)))
	}
	sort.Strings(bar.Cluster)
	bar.Items = evaluateBar(&bar)
	return bar
}

func mustRel(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return r
}

// loadBaselineEntries counts the known-difference entries of durable/contract/baseline.toml.
func loadBaselineEntries(root string) BaselineInfo {
	rel := "durable/contract/baseline.toml"
	info := BaselineInfo{Path: rel}
	var doc map[string]any
	if _, err := toml.DecodeFile(filepath.Join(root, filepath.FromSlash(rel)), &doc); err != nil {
		info.Entries = -1
		return info
	}
	for _, v := range doc {
		switch t := v.(type) {
		case []map[string]any:
			info.Entries += len(t)
		case []any:
			info.Entries += len(t)
		case map[string]any:
			info.Entries += len(t)
		}
	}
	return info
}

func (b *DurableBar) arm(name string) *DurableArm {
	for i := range b.Arms {
		if b.Arms[i].Name == name {
			return &b.Arms[i]
		}
	}
	return nil
}

// evaluateBar judges the four items from the loaded data. An item with no committed evidence is not met.
func evaluateBar(b *DurableBar) []BarItem {
	var items []BarItem
	// 1. zero baseline entries
	it1 := BarItem{N: 1, Title: "Zero baseline entries", Criterion: "durable/contract/baseline.toml lists no known difference from Pi."}
	switch {
	case b.Baseline.Entries < 0:
		it1.Verdict = "the baseline file could not be read"
	case b.Baseline.Entries == 0:
		it1.Met, it1.Verdict = true, "baseline.toml has 0 entries"
	default:
		it1.Verdict = fmt.Sprintf("baseline.toml has %d entries", b.Baseline.Entries)
	}
	items = append(items, it1)

	pi, pig := b.arm(piArm), b.arm(headlineArm)
	// 2. beat Pi on every measure
	it2 := BarItem{N: 2, Title: "Beat Pi on every measure", Criterion: "At every size the headline arm (PiG, the benchmark's own walk model, default GOMAXPROCS) is below pi-durable on warm wall, cold wall, CPU seconds and peak RSS."}
	if pi == nil || pig == nil {
		it2.Verdict = "no committed results for both arms"
	} else {
		wins, total := 0, 0
		for _, n := range b.Sizes {
			p, g := pi.Cells[n], pig.Cells[n]
			if p.Samples == 0 || g.Samples == 0 {
				continue
			}
			for _, m := range []struct {
				name, unit string
				pv, gv     float64
			}{
				{"warm wall", "ms", p.WarmMs, g.WarmMs},
				{"cold wall", "ms", p.ColdMs, g.ColdMs},
				{"CPU", "s", p.CPUSec, g.CPUSec},
				{"peak RSS", "MB", p.RSSMB, g.RSSMB},
			} {
				met := g.Samples > 0 && m.gv < m.pv
				total++
				if met {
					wins++
				}
				it2.Cells = append(it2.Cells, BarCell{Measure: m.name, Size: n, Met: met, PiVal: m.pv, PigVal: m.gv, Unit: m.unit})
			}
		}
		it2.Met = total > 0 && wins == total
		it2.Verdict = fmt.Sprintf("%d of %d measure cells beat pi-durable", wins, total)
	}
	items = append(items, it2)

	// 3. memory bounded by the context window, not by conversation length
	it3 := BarItem{N: 3, Title: "Memory bounded by the context window", Criterion: fmt.Sprintf("Peak RSS grows less than %.0f KiB per turn between 1,000 and 3,500 turns (flat in the conversation length).", flatKiBPerTurn)}
	if pig == nil || pig.Cells[1000].Samples == 0 || pig.Cells[3500].Samples == 0 {
		it3.Verdict = "no committed results at 1,000 and 3,500 turns"
	} else {
		slope := (pig.Cells[3500].RSSMB - pig.Cells[1000].RSSMB) * 1024 / 2500
		it3.Met = slope < flatKiBPerTurn
		it3.Verdict = fmt.Sprintf("headline arm: %.1f MB at 1,000 turns, %.1f MB at 3,500 turns, %.1f KiB per turn", pig.Cells[1000].RSSMB, pig.Cells[3500].RSSMB, slope)
		for _, n := range []int{1000, 3500} {
			it3.Cells = append(it3.Cells, BarCell{Measure: "peak RSS", Size: n, Met: it3.Met, PigVal: pig.Cells[n].RSSMB, Unit: "MB"})
		}
	}
	items = append(items, it3)

	// 4. proven on the cluster
	it4 := BarItem{N: 4, Title: "Proven on the cluster", Criterion: "A k8s-bench compare with Pi's coefficient of variation under 5% shows items 2 and 3 at 3,500 turns."}
	if len(b.Cluster) == 0 {
		it4.Verdict = "no cluster compare result is committed under bench/k8s/results; bench/k8s holds the runner and a fixture only"
	} else {
		it4.Verdict = fmt.Sprintf("%d cluster result files are committed (%s); this report does not judge them, so the item stays not met", len(b.Cluster), strings.Join(b.Cluster, ", "))
	}
	items = append(items, it4)
	return items
}

// ---- Contract gate ----

// ContractGate is the newest committed Durable conformance report.
type ContractGate struct {
	Dir   string
	Impls []GateImpl
}

// GateImpl is the lane counts of one implementation.
type GateImpl struct {
	Name                         string
	Pass, Fail, Pending, Skipped int
	Failed                       []string
}

func loadContractGate(root string) *ContractGate {
	dirs, _ := filepath.Glob(filepath.Join(root, "bench", "durable", "report", "gate", "*", "report.json"))
	if len(dirs) == 0 {
		return nil
	}
	type dated struct {
		path string
		t    int64
	}
	var ds []dated
	for _, p := range dirs {
		var t int64
		if out, err := gitOut(root, "log", "-1", "--format=%ct", "--", mustRel(root, p)); err == nil {
			t, _ = strconv.ParseInt(out, 10, 64)
		}
		ds = append(ds, dated{p, t})
	}
	sort.Slice(ds, func(i, j int) bool {
		if ds[i].t != ds[j].t {
			return ds[i].t > ds[j].t
		}
		return ds[i].path < ds[j].path
	})
	var doc map[string]struct {
		Lanes map[string]struct {
			Pass    int      `json:"pass"`
			Fail    int      `json:"fail"`
			Pending int      `json:"pending"`
			Skipped int      `json:"skipped"`
			Failed  []string `json:"failed"`
		} `json:"lanes"`
	}
	if err := readJSON(ds[0].path, &doc); err != nil {
		return nil
	}
	g := &ContractGate{Dir: filepath.ToSlash(mustRel(root, filepath.Dir(ds[0].path)))}
	names := make([]string, 0, len(doc))
	for n := range doc {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		im := GateImpl{Name: n}
		for _, l := range doc[n].Lanes {
			im.Pass += l.Pass
			im.Fail += l.Fail
			im.Pending += l.Pending
			im.Skipped += l.Skipped
			im.Failed = append(im.Failed, l.Failed...)
		}
		sort.Strings(im.Failed)
		g.Impls = append(g.Impls, im)
	}
	return g
}

// ---- Performance scoreboard ----

// Scoreboard is automation/perf's registry joined with its ceilings and the last measurement.
type Scoreboard struct {
	Rev, Go, Platform string
	Metrics           []ScoreMetric
	Gates, Missing    int
}

// ScoreMetric is one registry metric with its gates.
type ScoreMetric struct {
	ID, Area, Title, Owner, Target, Basis string
	Impact                                int
	Status                                string
	Gates                                 []ScoreGate
}

// ScoreGate is one gate of a metric.
type ScoreGate struct {
	Kind     string // ratchet, measured, test, branch, missing
	ID       string
	Measured *float64
	Ceiling  *float64
	Note     string
}

func loadScoreboard(root string) Scoreboard {
	var sb Scoreboard
	var reg struct {
		Metric []struct {
			ID     string              `toml:"id"`
			Area   string              `toml:"area"`
			Title  string              `toml:"title"`
			Impact int                 `toml:"impact"`
			Owner  string              `toml:"owner"`
			Target string              `toml:"target"`
			Basis  string              `toml:"basis"`
			Gates  []map[string]string `toml:"gates"`
		} `toml:"metric"`
	}
	dir := filepath.Join(root, "automation", "perf")
	if _, err := toml.DecodeFile(filepath.Join(dir, "scoreboard.toml"), &reg); err != nil {
		return sb
	}
	var rat struct {
		Ceilings map[string]float64 `toml:"ceilings"`
	}
	_, _ = toml.DecodeFile(filepath.Join(dir, "ratchet.toml"), &rat)
	var meas struct {
		Rev      string             `json:"rev"`
		Go       string             `json:"go"`
		Platform string             `json:"platform"`
		Values   map[string]float64 `json:"values"`
	}
	_ = readJSON(filepath.Join(dir, "scoreboard-measured.json"), &meas)
	sb.Rev, sb.Go, sb.Platform = meas.Rev, meas.Go, meas.Platform
	for _, m := range reg.Metric {
		sm := ScoreMetric{ID: m.ID, Area: m.Area, Title: m.Title, Owner: m.Owner, Target: m.Target, Basis: m.Basis, Impact: m.Impact}
		missing, allBranch := false, len(m.Gates) > 0
		for _, g := range m.Gates {
			sg := ScoreGate{}
			switch {
			case g["ratchet"] != "":
				sg.Kind, sg.ID = "ratchet", g["ratchet"]
				if v, ok := meas.Values[sg.ID]; ok {
					v := v
					sg.Measured = &v
				}
				if c, ok := rat.Ceilings[sg.ID]; ok {
					c := c
					sg.Ceiling = &c
				}
				allBranch = false
			case g["measured"] != "":
				sg.Kind, sg.ID = "measured", g["measured"]
				if v, ok := meas.Values[sg.ID]; ok {
					v := v
					sg.Measured = &v
				}
				allBranch = false
			case g["test"] != "":
				sg.Kind, sg.ID = "test", g["test"]
				allBranch = false
			case g["branch"] != "":
				sg.Kind, sg.ID, sg.Note = "branch", g["path"], "on another development branch"
				sb.Missing += 0
			default:
				sg.Kind, sg.ID, sg.Note = "missing", g["missing"], g["route"]
				missing, allBranch = true, false
				sb.Missing++
			}
			sb.Gates++
			sm.Gates = append(sm.Gates, sg)
		}
		switch {
		case missing:
			sm.Status = "MISSING"
		case allBranch:
			sm.Status = "BRANCH"
		default:
			sm.Status = "GATED"
		}
		sb.Metrics = append(sb.Metrics, sm)
	}
	return sb
}

// humanCount formats a measured count with the unit its id implies.
func humanCount(v float64, id string) string {
	if strings.HasSuffix(id, "bytes") || strings.Contains(id, "bytes_") {
		switch {
		case math.Abs(v) >= 1_000_000:
			return fmt.Sprintf("%.2f MB", v/1_000_000)
		case math.Abs(v) >= 10_000:
			return fmt.Sprintf("%.1f KB", v/1000)
		}
		return fmt.Sprintf("%.0f B", v)
	}
	if v == math.Trunc(v) {
		return commas(int64(v))
	}
	return fmt.Sprintf("%.1f", v)
}

func commas(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// ---- Benchmark matrices ----

// Matrix is a same-machine comparison matrix (arms by rows), read from bench/compare/results/*.json.
type Matrix struct {
	File        string
	Title       string
	Provisional bool
	Machine     string
	Note        string
	Arms        []string
	Rows        []MatrixRow
}

// MatrixRow is one scenario with a cell per arm.
type MatrixRow struct {
	ID          string                `json:"id"`
	Scenario    string                `json:"scenario"`
	Unit        string                `json:"unit"`
	LowerBetter bool                  `json:"lowerIsBetter"`
	Provisional bool                  `json:"provisional"`
	Samples     string                `json:"samples"`
	Note        string                `json:"note"`
	Cells       map[string]MatrixCell `json:"cells"`
}

// MatrixCell is the median of one arm on one scenario.
type MatrixCell struct {
	Median    *float64 `json:"median"`
	Min       *float64 `json:"min"`
	Max       *float64 `json:"max"`
	N         int      `json:"n"`
	CannotRun string   `json:"cannotRun"`
	Note      string   `json:"note"`
}

func loadMatrices(root string) []Matrix {
	files, _ := filepath.Glob(filepath.Join(root, "bench", "compare", "results", "*.json"))
	sort.Strings(files)
	var out []Matrix
	for _, f := range files {
		var m struct {
			Title       string      `json:"title"`
			Provisional bool        `json:"provisional"`
			Machine     string      `json:"machine"`
			Note        string      `json:"note"`
			Arms        []string    `json:"arms"`
			Rows        []MatrixRow `json:"rows"`
		}
		if err := readJSON(f, &m); err != nil {
			continue
		}
		out = append(out, Matrix{File: filepath.ToSlash(mustRel(root, f)), Title: m.Title, Provisional: m.Provisional, Machine: m.Machine, Note: m.Note, Arms: m.Arms, Rows: m.Rows})
	}
	return out
}

func loadEvals(root string) Evals {
	return Evals{Bar: loadDurableBar(root), Gate: loadContractGate(root), Scoreboard: loadScoreboard(root), Matrices: loadMatrices(root)}
}
