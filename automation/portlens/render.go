// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed assets/* templates/*
var assets embed.FS

// ---- view models ----

type indexData struct {
	Overview
	Ledgers      []ledgerView
	NDivergences int
	NBugs        int
	NRows        string
	BugSince     string
}

type ledgerView struct {
	LedgerSummary
	ID string
}

type nameCount struct {
	Name string
	N    int
}

type divView struct {
	*Divergence
	Search string
}

type divsView struct {
	Items     []divView
	Classes   []nameCount
	Statuses  []nameCount
	PiVersion string
}

type bugView struct {
	BugEntry
	Search       string
	ClassLabel   string
	DLinks       []string
	PiIssueLabel string
}

type bugsView struct {
	Items     []bugView
	Classes   []nameCount
	Platforms []string
	Releases  []string
	Exempt    []nameCount
}

type cellView struct {
	Present bool
	Met     bool
	Text    string
	N       int
}

type measureRow struct {
	Measure string
	Cells   []cellView
}

type barItemView struct {
	BarItem
	Sizes []int
	Rows  []measureRow
}

type armView struct {
	DurableArm
	Cells []cellView
	Link  string
}

type gateView struct {
	Kind, Text string
}

type metricView struct {
	ScoreMetric
	Search string
	Gates  []gateView
}

type matrixCellView struct {
	Text   string
	Best   bool
	Cannot string
	Note   string
}

type matrixRowView struct {
	MatrixRow
	Cells []matrixCellView
	Link  string
}

type matrixView struct {
	Matrix
	Rows []matrixRowView
}

type evalsView struct {
	Bar struct {
		Items    []barItemView
		Sizes    []int
		Baseline BaselineInfo
	}
	Arms     []armView
	Gate     *ContractGate
	Score    Scoreboard
	Metrics  []metricView
	Areas    []string
	Statuses []string
	Matrices []matrixView
}

// ---- template functions ----

func fmtn(v any) string {
	switch n := v.(type) {
	case int:
		return commas(int64(n))
	case int64:
		return commas(n)
	}
	return fmt.Sprint(v)
}

func pctStr(a, b int) string {
	if b == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(a)/float64(b))
}

func pctNum(a, b int) string {
	if b == 0 {
		return "0"
	}
	return fmt.Sprintf("%.2f", 100*float64(a)/float64(b))
}

type stackSeg struct{ Class, Pct string }

type stackView struct {
	Label string
	Segs  []stackSeg
}

// stackBar is the status bar of one package row.
func stackBar(p PkgCounts) stackView {
	v := stackView{Label: fmt.Sprintf("%d ported, %d derived, %d designed out, %d gap", p.Ported, p.Derived, p.DesignedOut, p.Gap)}
	for _, s := range []struct {
		n   int
		cls string
	}{{p.Ported, "ported"}, {p.Derived, "derived"}, {p.DesignedOut, "designed-out"}, {p.Gap, "gap"}} {
		if s.n > 0 && p.Total > 0 {
			v.Segs = append(v.Segs, stackSeg{Class: "c-" + s.cls, Pct: fmt.Sprintf("%.2f%%", 100*float64(s.n)/float64(p.Total))})
		}
	}
	return v
}

type sparkView struct {
	Label, Points string
	CX, CY        string
}

// sparkline is the polyline of a trend.
func sparkline(t Trend) sparkView {
	const w, h, pad = 400.0, 56.0, 4.0
	n := len(t.Points)
	if n == 0 {
		return sparkView{}
	}
	lo, hi := math.MaxFloat64, -math.MaxFloat64
	for _, p := range t.Points {
		lo, hi = math.Min(lo, float64(p.Value)), math.Max(hi, float64(p.Value))
	}
	if hi == lo {
		hi = lo + 1
	}
	var pts []string
	var lastX, lastY float64
	for i, p := range t.Points {
		x := pad + (w-2*pad)*float64(i)/math.Max(1, float64(n-1))
		y := h - pad - (h-2*pad)*(float64(p.Value)-lo)/(hi-lo)
		pts = append(pts, fmt.Sprintf("%.1f,%.1f", x, y))
		lastX, lastY = x, y
	}
	return sparkView{
		Label:  fmt.Sprintf("%s: %d points from %d to %d", t.Name, n, t.Points[0].Value, t.Points[n-1].Value),
		Points: strings.Join(pts, " "),
		CX:     fmt.Sprintf("%.1f", lastX), CY: fmt.Sprintf("%.1f", lastY),
	}
}

type codePart struct {
	Text string
	Code bool
}

// rawHTML marks markup the generator itself produced: goldmark output (raw HTML in the source is escaped) and chroma token markup (source text is escaped). The only input is the repository's own files, run by a developer on a local checkout.
func rawHTML(s string) template.HTML {
	return template.HTML(s) //nolint:gosec // G203: s is goldmark or chroma output that escapes its source; the CLI reads only the developer's own checkout and writes a local directory
}

// inlineCode splits text at backticks into plain and code parts.
func inlineCode(s string) []codePart {
	parts := strings.Split(s, "`")
	var out []codePart
	for i, p := range parts {
		switch {
		case i%2 == 1 && i < len(parts)-1:
			out = append(out, codePart{Text: p, Code: true})
		case i%2 == 1:
			out = append(out, codePart{Text: "`" + p})
		case p != "":
			out = append(out, codePart{Text: p})
		}
	}
	return out
}

func statusTone(s string) string {
	switch s {
	case DivActive:
		return "ok"
	case DivAdditive:
		return "neutral"
	}
	return "warn"
}

func scoreTone(s string) string {
	switch s {
	case "GATED":
		return "ok"
	case "MISSING":
		return "bad"
	}
	return "warn"
}

func ratchetNote(gap, base int) string {
	switch {
	case gap > base:
		return "Above the baseline: the ratchet fails."
	case gap == base:
		return "At the baseline."
	}
	return "Below the baseline."
}

func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"fmtn": fmtn, "pct": pctStr, "pctn": pctNum,
		"sub":   func(a, b int) int { return a - b },
		"add":   func(a, b int) int { return a + b },
		"short": func(s string, n int) string { return s[:min(n, len(s))] },
		"stack": stackBar, "spark": sparkline,
		"raw":         rawHTML,
		"code":        inlineCode,
		"ratchetNote": ratchetNote, "statusTone": statusTone, "scoreTone": scoreTone,
	}
}

// ---- view builders ----

func searchText(parts ...string) string {
	return strings.ToLower(strings.Join(parts, " "))
}

func buildDivsView(rep *Report) divsView {
	v := divsView{PiVersion: rep.Pin.Version}
	cls, sts := map[string]int{}, map[string]int{}
	for _, d := range rep.Divergences {
		var tests []string
		for _, t := range d.Tests {
			tests = append(tests, t.Name)
		}
		v.Items = append(v.Items, divView{Divergence: d, Search: searchText(d.ID, d.Title, d.Class, d.Status, strings.Join(tests, " "), strings.Join(d.BugIDs, " "), d.Raw)})
		cls[d.Class]++
		sts[d.Status]++
	}
	for _, n := range []string{ClassPiBug, ClassPerf, ClassPlatform, ClassAdditive, ClassOther} {
		v.Classes = append(v.Classes, nameCount{n, cls[n]})
	}
	for _, n := range []string{DivActive, DivAdditive, DivRetired, DivWithdrawn} {
		if sts[n] > 0 {
			v.Statuses = append(v.Statuses, nameCount{n, sts[n]})
		}
	}
	return v
}

func buildBugsView(rep *Report) bugsView {
	v := bugsView{}
	cls := map[string]int{}
	plat, rel := map[string]bool{}, map[string]bool{}
	for _, e := range rep.Bugs.Entries {
		bv := bugView{BugEntry: e, ClassLabel: bugClassLabel(e.Class)}
		if e.Divergence != "" {
			bv.DLinks = dRef.FindAllString(e.Divergence, -1)
		}
		if e.PiIssue != "" {
			bv.PiIssueLabel = "Pi issue " + e.PiIssue[strings.LastIndex(e.PiIssue, "/")+1:]
		}
		var tests []string
		for _, t := range e.Tests {
			_, n := testParts(t)
			tests = append(tests, n)
		}
		bv.Search = searchText(e.ID, e.Title, e.Symptom, e.RootCause, e.Platform, e.Impact, e.Divergence, strings.Join(tests, " "), e.FixedIn, commitHashes(e))
		v.Items = append(v.Items, bv)
		cls[bv.ClassLabel]++
		plat[e.Platform] = true
		rel[e.FixedIn] = true
	}
	for _, n := range []string{"PiG's own defect", "Fixed ahead of Pi", "Pi fix ported"} {
		v.Classes = append(v.Classes, nameCount{n, cls[n]})
	}
	for p := range plat {
		v.Platforms = append(v.Platforms, p)
	}
	for r := range rel {
		v.Releases = append(v.Releases, r)
	}
	sort.Strings(v.Platforms)
	sort.Slice(v.Releases, func(i, j int) bool { return versionLess(v.Releases[j], v.Releases[i]) })
	for _, k := range sortedKeys(rep.Bugs.Exempt) {
		v.Exempt = append(v.Exempt, nameCount{k, rep.Bugs.Exempt[k]})
	}
	return v
}

func commitHashes(e BugEntry) string {
	var hs []string
	for _, c := range e.Commits {
		hs = append(hs, c.Hash)
	}
	return strings.Join(hs, " ")
}

func cellText(c BarCell) string {
	if c.PiVal == 0 {
		return fmt.Sprintf("PiG %s %s", trimFloat(c.PigVal), c.Unit)
	}
	return fmt.Sprintf("PiG %s %s, Pi %s %s", trimFloat(c.PigVal), c.Unit, trimFloat(c.PiVal), c.Unit)
}

func trimFloat(f float64) string {
	switch {
	case f >= 100:
		return fmt.Sprintf("%.0f", f)
	case f >= 10:
		return fmt.Sprintf("%.1f", f)
	}
	return fmt.Sprintf("%.2f", f)
}

// sampleLinker copies a raw sample file of the repository into the output and returns its relative link. A path outside the repository, a missing file and a file over maxSample give no link.
type sampleLinker func(rel string) string

const maxSample = 8 << 20

func newSampleLinker(root, out string) sampleLinker {
	return func(rel string) string {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
		if rel == "" || filepath.IsAbs(rel) || clean == ".." || strings.HasPrefix(clean, "../") {
			return ""
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(clean)))
		if err != nil || len(b) > maxSample {
			return ""
		}
		dst := filepath.Join(out, "samples", filepath.FromSlash(clean))
		if os.MkdirAll(filepath.Dir(dst), 0o755) != nil || os.WriteFile(dst, b, 0o644) != nil {
			return ""
		}
		return "samples/" + clean
	}
}

func buildEvalsView(rep *Report, link sampleLinker) evalsView {
	ev := rep.Evals
	v := evalsView{Gate: ev.Gate, Score: ev.Scoreboard}
	v.Bar.Sizes = ev.Bar.Sizes
	v.Bar.Baseline = ev.Bar.Baseline
	for _, it := range ev.Bar.Items {
		iv := barItemView{BarItem: it, Sizes: ev.Bar.Sizes}
		var order []string
		by := map[string]map[int]BarCell{}
		for _, c := range it.Cells {
			if by[c.Measure] == nil {
				by[c.Measure] = map[int]BarCell{}
				order = append(order, c.Measure)
			}
			by[c.Measure][c.Size] = c
		}
		for _, m := range order {
			row := measureRow{Measure: m}
			for _, n := range ev.Bar.Sizes {
				c, ok := by[m][n]
				row.Cells = append(row.Cells, cellView{Present: ok, Met: c.Met, Text: cellText(c)})
			}
			iv.Rows = append(iv.Rows, row)
		}
		v.Bar.Items = append(v.Bar.Items, iv)
	}
	for _, a := range ev.Bar.Arms {
		av := armView{DurableArm: a, Link: link(a.File)}
		for _, n := range ev.Bar.Sizes {
			c, ok := a.Cells[n]
			cv := cellView{Present: ok, N: c.Samples}
			if ok {
				cv.Text = fmt.Sprintf("%s/%s ms · %s s · %s MB", trimFloat(c.ColdMs), trimFloat(c.WarmMs), trimFloat(c.CPUSec), trimFloat(c.RSSMB))
			}
			av.Cells = append(av.Cells, cv)
		}
		v.Arms = append(v.Arms, av)
	}
	areas, statuses := map[string]bool{}, map[string]bool{}
	for _, m := range ev.Scoreboard.Metrics {
		mv := metricView{ScoreMetric: m}
		parts := []string{m.ID, m.Title, m.Area, m.Owner, m.Target, m.Status}
		for _, g := range m.Gates {
			gv := gateView{Kind: g.Kind}
			switch g.Kind {
			case "ratchet", "measured":
				gv.Text = g.ID
				if g.Measured != nil {
					gv.Text += " " + humanCount(*g.Measured, g.ID)
				} else {
					gv.Text += " not measured"
				}
				if g.Ceiling != nil {
					gv.Text += " (ceiling " + humanCount(*g.Ceiling, g.ID) + ")"
				}
			case "missing":
				gv.Text = g.ID + " (route: " + g.Note + ")"
			default:
				gv.Text = g.ID
			}
			parts = append(parts, gv.Text)
			mv.Gates = append(mv.Gates, gv)
		}
		mv.Search = searchText(parts...)
		v.Metrics = append(v.Metrics, mv)
		areas[m.Area] = true
		statuses[m.Status] = true
	}
	v.Areas = sortedKeys(areas)
	v.Statuses = sortedKeys(statuses)
	for _, m := range ev.Matrices {
		mv := matrixView{Matrix: m}
		for _, r := range m.Rows {
			rv := matrixRowView{MatrixRow: r, Link: link(r.Samples)}
			best := -1.0
			for _, a := range m.Arms {
				if c, ok := r.Cells[a]; ok && c.Median != nil && c.CannotRun == "" {
					if best < 0 || (r.LowerBetter && *c.Median < best) || (!r.LowerBetter && *c.Median > best) {
						best = *c.Median
					}
				}
			}
			for _, a := range m.Arms {
				c, ok := r.Cells[a]
				cv := matrixCellView{Note: c.Note, Cannot: c.CannotRun}
				switch {
				case !ok:
					cv.Cannot = "no result"
				case c.Median != nil:
					cv.Text = trimFloat(*c.Median)
					if c.Min != nil && c.Max != nil {
						cv.Text += fmt.Sprintf(" (%s–%s", trimFloat(*c.Min), trimFloat(*c.Max))
						if c.N > 0 {
							cv.Text += fmt.Sprintf(", n=%d", c.N)
						}
						cv.Text += ")"
					}
					cv.Best = *c.Median == best
				}
				rv.Cells = append(rv.Cells, cv)
			}
			mv.Rows = append(mv.Rows, rv)
		}
		v.Matrices = append(v.Matrices, mv)
	}
	return v
}

// ---- output ----

func parseTemplates() (*template.Template, error) {
	return template.New("").Funcs(templateFuncs()).ParseFS(assets, "templates/*.tmpl")
}

func renderFragment(t *template.Template, name string, data any) (string, error) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// generatorMeta marks an index.html this tool wrote; WriteReport replaces files only in a directory that holds one.
const generatorMeta = `<meta name="generator" content="PiG port lens">`

// checkOutputDir accepts a missing or empty directory or the directory of an earlier report. WriteReport deletes the data, views and samples subdirectories of its output, so any other directory is refused.
func checkOutputDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) || err == nil && len(entries) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	if b, err := os.ReadFile(filepath.Join(dir, "index.html")); err == nil && bytes.Contains(b, []byte(generatorMeta)) {
		return nil
	}
	return fmt.Errorf("%s is not empty and holds no earlier port lens report; choose a new or empty directory", dir)
}

// WriteReport writes the whole static report into dir.
func WriteReport(rep *Report, dir string) error {
	if err := checkOutputDir(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		return err
	}
	// Drop the data of an earlier build: a stale ledger or snippet shard must not survive.
	for _, sub := range []string{"data", "views", "samples"} {
		if err := os.RemoveAll(filepath.Join(dir, sub)); err != nil {
			return err
		}
	}
	tpl, err := parseTemplates()
	if err != nil {
		return err
	}
	for _, name := range []string{"style.css", "app.js"} {
		b, err := assets.ReadFile("assets/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "assets", name), b, 0o644); err != nil {
			return err
		}
	}
	nrows := 0
	idx := indexData{Overview: rep.Overview, NDivergences: len(rep.Divergences), NBugs: len(rep.Bugs.Entries), BugSince: rep.Bugs.Since}
	for _, pl := range rep.Ledgers {
		nrows = max(nrows, pl.Total)
	}
	for _, ls := range rep.Overview.Ledgers {
		idx.Ledgers = append(idx.Ledgers, ledgerView{LedgerSummary: ls, ID: ledgerID(ls.Version)})
	}
	idx.NRows = commas(int64(nrows))
	html, err := renderFragment(tpl, "index.html.tmpl", idx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0o644); err != nil {
		return err
	}
	for _, f := range []struct {
		name, tmpl string
		data       any
	}{
		{"divergences", "divergences.html.tmpl", buildDivsView(rep)},
		{"bugs", "bugs.html.tmpl", buildBugsView(rep)},
		{"evals", "evals.html.tmpl", buildEvalsView(rep, newSampleLinker(rep.Opts.Root, dir))},
	} {
		h, err := renderFragment(tpl, f.tmpl, f.data)
		if err != nil {
			return fmt.Errorf("%s: %w", f.name, err)
		}
		b, _ := json.Marshal(h)
		fname, _ := json.Marshal(f.name)
		if err := os.MkdirAll(filepath.Join(dir, "views"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "views", f.name+".js"), []byte("PL.frag("+string(fname)+","+string(b)+");\n"), 0o644); err != nil {
			return err
		}
	}
	return writePortmap(rep, dir)
}
