// Command portmapcheck derives the PORT_MAP ✅ ticks instead of trusting them. A row is proven only when the interface ledger and the
// Go tree agree with it:
//
//  1. Every exported member of the Pi file (the interface inventory rows whose published declaration file is that file) is ported or
//     designed out in the ledger, and the Go symbol each ported member maps to is defined in a Go file the row cites. A cited Go file
//     that holds no mapped symbol fails.
//  2. A Pi file with no exported member (a side-effect module, script or CLI entry point) needs a Go test whose source carries
//     `// pi: <Pi file path>`. The marker is necessary, not sufficient: until the marked test's coverage of a cited file and its
//     mutation red-proof are derived, a marked row stays unproven.
//  3. A renamed implementation is allowed only through a reviewed entry (portmap-reviewed.json) that names the Go symbol for each
//     Pi member; the check verifies that the symbol is defined in a cited file.
//
// A ✅ row that is not proven fails unless the committed baseline lists it. The baseline only shrinks: a new unproven tick fails, and so
// does a baseline entry for a row that is now proven or no longer ✅. `-generate` rewrites the status column from the derivation.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

const (
	portMapFile  = "docs/parity/PORT_MAP.md"
	baselineFile = "test/parity/portmap-unproven-baseline.txt"
	reviewedFile = "test/parity/portmap-reviewed.json"
	mutantsFile  = "test/parity/portmap-mutants.json"
	inventoryGlb = "test/parity/interfaces/upstream-v*.json"
)

// row is one PORT_MAP table row.
type row struct {
	Pi     string
	Cell   string
	Status string
	Line   int
}

// result is the derivation of one ✅ row.
type result struct {
	Row      row
	Proven   bool
	Reasons  []string
	Cited    []string
	Members  int
	Evidence string
	// Marker is set for a Pi file with no ledger member that a `// pi:` test names; rule 3 plus a killed mutant must prove it.
	Marker bool
	// Tests are the evidence tests (`dir#TestName`) of the row, and Hit the cited non-test Go files a Pi member maps to.
	Tests []string
	Hit   []string
}

type reviewedEntry struct {
	Reason  string            `json:"reason"`
	Members map[string]string `json:"members"`
}

var (
	rowRe      = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|\\s*(.*?)\\s*\\|\\s*(✅|🟡|⬜|⏸|🔴|n/a)\\s*\\|\\s*$")
	goFileRe   = regexp.MustCompile(`[A-Za-z0-9_@./*{}-]+\.go\b`)
	goDirRe    = regexp.MustCompile("`([A-Za-z0-9_@./-]+/)`")
	testFuncRe = regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	piMarker   = regexp.MustCompile(`(?m)^\s*//\s*pi:\s*(\S+)`)
)

func main() {
	root := flag.String("root", ".", "repository root")
	report := flag.Bool("report", false, "print every unproven tick")
	updateBaseline := flag.Bool("update-baseline", false, "rewrite the baseline from the current derivation (shrink-only review)")
	generate := flag.Bool("generate", false, "rewrite the PORT_MAP status column from the derivation")
	flag.Parse()
	res, err := checkWith(*root, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "portmapcheck:", err)
		os.Exit(2)
	}
	proven, unproven := 0, 0
	for _, r := range res {
		if r.Proven {
			proven++
		} else {
			unproven++
		}
	}
	fmt.Printf("portmap-check: %d ✅ rows, %d proven by the ledger, %d unproven\n", len(res), proven, unproven)
	if *report {
		for _, r := range res {
			if !r.Proven {
				fmt.Printf("%s | %s\n", r.Row.Pi, strings.Join(r.Reasons, "; "))
			}
		}
	}
	if *generate {
		if err := generateStatus(*root, res); err != nil {
			fmt.Fprintln(os.Stderr, "portmapcheck:", err)
			os.Exit(2)
		}
		return
	}
	if *updateBaseline {
		if err := writeBaseline(*root, res); err != nil {
			fmt.Fprintln(os.Stderr, "portmapcheck:", err)
			os.Exit(2)
		}
		return
	}
	problems, err := compareBaseline(*root, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "portmapcheck:", err)
		os.Exit(2)
	}
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "FAIL", p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
}

// check derives every ✅ row of the PORT_MAP under root.
func check(root string) ([]result, error) { return checkWith(root, false) }

// checkWith derives the rows; with needCoverage a row is proven only when portmap-coverage.json records, for the current content of the
// cited files and the evidence tests, that those tests execute statements of every mapped cited file (rule 3).
func checkWith(root string, needCoverage bool) ([]result, error) {
	rows, err := readRows(root)
	if err != nil {
		return nil, err
	}
	inv, err := readInventory(root)
	if err != nil {
		return nil, err
	}
	var reviewed map[string]reviewedEntry
	if b, err := os.ReadFile(filepath.Join(root, reviewedFile)); err == nil {
		if err := json.Unmarshal(b, &reviewed); err != nil {
			return nil, fmt.Errorf("%s: %w", reviewedFile, err)
		}
	}
	markers := readMarkers(root)
	var mutants map[string][]mutant
	if b, err := os.ReadFile(filepath.Join(root, mutantsFile)); err == nil {
		if err := json.Unmarshal(b, &mutants); err != nil {
			return nil, fmt.Errorf("%s: %w", mutantsFile, err)
		}
	}
	var out []result
	for _, r := range rows {
		if r.Status != "✅" {
			continue
		}
		res := derive(root, r, inv, reviewed[r.Pi], markers)
		if needCoverage && res.Marker && onlyMarkerReason(res) {
			res.Reasons, res.Proven = nil, true // coverage and a killed mutant decide below
		}
		if res.Proven {
			res.Tests = evidenceTests(root, r.Pi, inv, markers)
		}
		if needCoverage && res.Proven {
			e := coverageFor(root, res)
			if why := coverageProblem(res, e); why != "" {
				res.Proven = false
				res.Reasons = append(res.Reasons, why)
			} else if res.Marker {
				if why := mutantProblem(root, res, mutants[r.Pi]); why != "" {
					res.Proven = false
					res.Reasons = append(res.Reasons, why)
				}
			}
		}
		out = append(out, res)
	}
	return out, nil
}

func derive(root string, r row, inv *inventory, reviewed reviewedEntry, markers map[string][]string) result {
	res := result{Row: r}
	res.Cited = citedFiles(root, r.Cell)
	for _, f := range res.Cited {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			res.Reasons = append(res.Reasons, "cited Go file does not exist: "+f)
		}
	}
	if len(res.Cited) == 0 {
		res.Reasons = append(res.Reasons, "no Go file cited")
	}
	cited := map[string]bool{}
	for _, f := range res.Cited {
		cited[f] = true
	}
	ids := inv.membersOf(r.Pi)
	res.Members = len(ids)
	if len(ids) == 0 {
		if len(markers[r.Pi]) == 0 {
			res.Reasons = append(res.Reasons, "no exported member in the ledger and no Go test carries `// pi: "+r.Pi+"`")
		} else {
			// A comment is not evidence: the marked test proves the row only once its coverage of a cited file and a killed
			// mutant are derived (rule 3), so a lane cannot shrink the baseline by adding a comment to any test.
			res.Evidence = "marker test"
			res.Marker = true
			res.Reasons = append(res.Reasons, "MARKER-UNPROVEN a Go test carries `// pi: "+r.Pi+"`, but its coverage of a cited file and its mutation red-proof are not derived yet")
		}
		res.Proven = len(res.Reasons) == 0
		return res
	}
	hit := map[string]bool{}
	for _, id := range ids {
		m := inv.mapping[id]
		switch m.Disposition {
		case "designed-out":
			continue
		case "ported":
		default:
			res.Reasons = append(res.Reasons, "LEDGER-GAP "+shortID(id)+" is "+m.Disposition)
			continue
		}
		targets := m.PigTargets
		if t, ok := reviewed.Members[id]; ok {
			if !symbolIn(root, t, cited) {
				res.Reasons = append(res.Reasons, "reviewed symbol "+t+" for "+shortID(id)+" is not defined in a cited file")
				continue
			}
			targets = []string{t}
		}
		in := false
		for _, t := range targets {
			file, _, _ := strings.Cut(t, "#")
			if cited[file] {
				in = true
				hit[file] = true
			}
		}
		if !in {
			res.Reasons = append(res.Reasons, "WRONG-FILE "+shortID(id)+" maps to "+strings.Join(targets, ", ")+", not a cited file")
		}
	}
	for _, f := range res.Cited {
		if !hit[f] && !strings.HasSuffix(f, "_test.go") {
			if _, err := os.Stat(filepath.Join(root, f)); err == nil {
				res.Reasons = append(res.Reasons, "ZERO-SYMBOL cited file "+f+" holds no symbol any Pi member of this file maps to")
			}
		}
	}
	for f := range hit {
		res.Hit = append(res.Hit, f)
	}
	sort.Strings(res.Hit)
	res.Proven = len(res.Reasons) == 0
	return res
}

func shortID(id string) string {
	if _, after, ok := strings.Cut(id, "#"); ok {
		return after
	}
	return id
}

// readRows reads the PORT_MAP table rows.
func readRows(root string) ([]row, error) {
	b, err := os.ReadFile(filepath.Join(root, portMapFile))
	if err != nil {
		return nil, err
	}
	var rows []row
	for i, line := range strings.Split(string(b), "\n") {
		if m := rowRe.FindStringSubmatch(line); m != nil {
			rows = append(rows, row{Pi: m[1], Cell: m[2], Status: m[3], Line: i + 1})
		}
	}
	return rows, nil
}

// citedFiles lists the Go files a row cell cites: file paths, globs and backticked directories.
func citedFiles(root, cell string) []string {
	set := map[string]bool{}
	dir := ""
	for _, p := range goFileRe.FindAllString(cell, -1) {
		if i := strings.LastIndex(p, "/"); i >= 0 {
			dir = p[:i+1]
		} else if dir != "" {
			p = dir + p // a bare file name in a cell sits beside the file cited before it
		}
		if strings.ContainsAny(p, "*{") {
			matches, _ := filepath.Glob(filepath.Join(root, p))
			for _, m := range matches {
				if rel, err := filepath.Rel(root, m); err == nil {
					set[filepath.ToSlash(rel)] = true
				}
			}
			continue
		}
		set[p] = true
	}
	for _, m := range goDirRe.FindAllStringSubmatch(cell, -1) {
		_ = filepath.WalkDir(filepath.Join(root, m[1]), func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
				if rel, err := filepath.Rel(root, p); err == nil {
					set[filepath.ToSlash(rel)] = true
				}
			}
			return nil
		})
	}
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

type mappingRow struct {
	Disposition string   `json:"disposition"`
	PigTargets  []string `json:"pigTargets"`
	Evidence    []string `json:"evidence"`
}

type inventory struct {
	byFile  map[string][]string // packages/<key>/src/<rel>.ts -> interface IDs
	mapping map[string]mappingRow
}

// membersOf returns the interface IDs whose published declaration file is the Pi source file.
func (inv *inventory) membersOf(pi string) []string {
	rel, ok := strings.CutPrefix(pi, "packages/")
	if !ok {
		return nil
	}
	key, rest, _ := strings.Cut(rel, "/")
	rest, ok = strings.CutPrefix(rest, "src/")
	if !ok {
		return nil
	}
	rest = strings.TrimSuffix(strings.TrimSuffix(rest, ".tsx"), ".ts")
	return inv.byFile[key+"/"+rest]
}

func readInventory(root string) (*inventory, error) {
	files, _ := filepath.Glob(filepath.Join(root, inventoryGlb))
	if len(files) == 0 {
		return nil, fmt.Errorf("no interface inventory under %s", inventoryGlb)
	}
	slices.Sort(files)
	invPath := files[len(files)-1]
	for _, f := range files {
		if filepath.Base(f) == "upstream-v"+pigversion.UpstreamVersion+".json" {
			invPath = f // the pinned upstream version, not the lexicographically last file (v1.10 sorts before v1.9)
		}
	}
	var inv struct {
		Packages []struct {
			Key  string `json:"key"`
			Name string `json:"name"`
		} `json:"packages"`
		Interfaces []struct {
			ID     string `json:"id"`
			Source struct {
				Path string `json:"path"`
			} `json:"source"`
		} `json:"interfaces"`
	}
	if err := readJSON(invPath, &inv); err != nil {
		return nil, err
	}
	version := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(invPath), "upstream-v"), ".json")
	var mp struct {
		Mappings []struct {
			ID string `json:"id"`
			mappingRow
		} `json:"mappings"`
	}
	if err := readJSON(filepath.Join(root, "test/parity/interfaces/mapping-v"+version+".json"), &mp); err != nil {
		return nil, err
	}
	out := &inventory{byFile: map[string][]string{}, mapping: map[string]mappingRow{}}
	for _, m := range mp.Mappings {
		out.mapping[m.ID] = m.mappingRow
	}
	keyOf := map[string]string{}
	for _, p := range inv.Packages {
		keyOf[p.Name] = p.Key
	}
	scoped := regexp.MustCompile(`^node_modules/(@[^/]+/[^/]+)/dist/(.*)\.d\.ts$`)
	local := regexp.MustCompile(`^dist/(.*)\.d\.ts$`)
	source := regexp.MustCompile(`^packages/([^/]+)/src/(.*)\.tsx?$`)
	for _, e := range inv.Interfaces {
		var k string
		switch m := scoped.FindStringSubmatch(e.Source.Path); {
		case m != nil && keyOf[m[1]] != "":
			k = keyOf[m[1]] + "/" + m[2]
		case local.MatchString(e.Source.Path):
			// a package built in this repository records its declaration file relative to the package; the ID names the package
			rest, _ := strings.CutPrefix(e.ID, "pkg:")
			key, _, _ := strings.Cut(rest, "/")
			k = key + "/" + local.FindStringSubmatch(e.Source.Path)[1]
		case source.MatchString(e.Source.Path):
			m := source.FindStringSubmatch(e.Source.Path) // an unpublished package records the TypeScript source itself
			k = m[1] + "/" + m[2]
		default:
			continue
		}
		out.byFile[k] = append(out.byFile[k], e.ID)
	}
	return out, nil
}

func readJSON(file string, v any) error {
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// readMarkers collects, for every Pi path a Go test file names with `// pi: <path>`, the Test functions of that file (`dir#TestName`).
func readMarkers(root string) map[string][]string {
	out := map[string][]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".upstream", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		marks := piMarker.FindAllStringSubmatch(string(b), -1)
		if len(marks) == 0 {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		var tests []string
		for _, m := range testFuncRe.FindAllStringSubmatch(string(b), -1) {
			tests = append(tests, filepath.ToSlash(filepath.Dir(rel))+"#"+m[1])
		}
		for _, m := range marks {
			out[m[1]] = append(out[m[1]], tests...)
		}
		return nil
	})
	return out
}

// symbolIn reports whether `file#Symbol` or `file#Owner.Member` is declared in a cited file.
func symbolIn(root, target string, cited map[string]bool) bool {
	file, sym, ok := strings.Cut(target, "#")
	if !ok || !cited[file] {
		return false
	}
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, file), nil, parser.SkipObjectResolution)
	if err != nil {
		return false
	}
	owner, member, hasMember := strings.Cut(sym, ".")
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.FuncDecl:
			if !hasMember && x.Recv == nil && x.Name.Name == sym {
				return true
			}
			if hasMember && x.Recv != nil && x.Name.Name == member && recvName(x.Recv) == owner {
				return true
			}
		case *ast.GenDecl:
			for _, s := range x.Specs {
				switch sp := s.(type) {
				case *ast.TypeSpec:
					if sp.Name.Name == owner {
						if !hasMember {
							return true
						}
						if st, ok := sp.Type.(*ast.StructType); ok {
							for _, fl := range st.Fields.List {
								for _, n := range fl.Names {
									if n.Name == member {
										return true
									}
								}
							}
						}
						if it, ok := sp.Type.(*ast.InterfaceType); ok {
							for _, fl := range it.Methods.List {
								for _, n := range fl.Names {
									if n.Name == member {
										return true
									}
								}
							}
						}
					}
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						if !hasMember && n.Name == sym {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func recvName(r *ast.FieldList) string {
	if r == nil || len(r.List) == 0 {
		return ""
	}
	t := r.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if ix, ok := t.(*ast.IndexExpr); ok {
		t = ix.X
	}
	if ix, ok := t.(*ast.IndexListExpr); ok {
		t = ix.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// writeBaseline records the unproven ✅ rows.
func writeBaseline(root string, res []result) error {
	var lines []string
	for _, r := range res {
		if !r.Proven {
			lines = append(lines, r.Row.Pi)
		}
	}
	sort.Strings(lines)
	return os.WriteFile(filepath.Join(root, baselineFile), []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// compareBaseline fails on a new unproven ✅ row and on a baseline entry that is stale.
func compareBaseline(root string, res []result) ([]string, error) {
	base := map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(root, baselineFile)); err == nil {
		for line := range strings.SplitSeq(string(b), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				base[line] = true
			}
		}
	}
	var problems []string
	seen := map[string]bool{}
	for _, r := range res {
		seen[r.Row.Pi] = true
		if !r.Proven && !base[r.Row.Pi] {
			problems = append(problems, fmt.Sprintf("%s:%d %s is ✅ but the ledger does not derive it: %s", portMapFile, r.Row.Line, r.Row.Pi, strings.Join(r.Reasons, "; ")))
		}
		if r.Proven && base[r.Row.Pi] {
			problems = append(problems, fmt.Sprintf("%s is now proven: delete it from %s (the baseline only shrinks)", r.Row.Pi, baselineFile))
		}
	}
	for pi := range base {
		if !seen[pi] {
			problems = append(problems, fmt.Sprintf("%s is in %s but is no longer a ✅ row: delete the entry", pi, baselineFile))
		}
	}
	sort.Strings(problems)
	return problems, nil
}

// generateStatus rewrites the status column: a ✅ row the derivation does not prove becomes 🟡.
func generateStatus(root string, res []result) error {
	demote := map[int]bool{}
	for _, r := range res {
		if !r.Proven {
			demote[r.Row.Line] = true
		}
	}
	file := filepath.Join(root, portMapFile)
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	for i := range lines {
		if demote[i+1] {
			lines[i] = strings.TrimSuffix(strings.TrimRight(lines[i], " "), "✅ |") + "🟡 |"
		}
	}
	return os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o644)
}
