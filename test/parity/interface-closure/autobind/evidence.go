package main

import (
	"go/ast"
	"slices"
	"sort"
	"strings"
)

// testEv is one test function that references a symbol.
type testEv struct {
	Ref     string `json:"ref"`
	Direct  bool   `json:"direct"`  // the test body itself references the symbol, not only through an interface method
	Asserts bool   `json:"asserts"` // the test, or a helper it calls, reports failures through testing.T or testify
	Ported  bool   `json:"ported"`  // the file is an upstream-ported or conformance test
	Called  bool   `json:"called"`  // for a function or method: a call whose value is not discarded
	Uses    int    `json:"uses"`
}

func (t testEv) rank() int {
	r := 0
	if t.Direct {
		r += 4
	}
	if t.Asserts {
		r += 4
	}
	if t.Ported {
		r += 2
	}
	if t.Called {
		r++
	}
	return r
}

func isTestFunc(key string) bool {
	_, name, _ := strings.Cut(key, "#")
	return strings.HasPrefix(name, "Test") && !strings.Contains(name, ".") && isTestFile(strings.SplitN(key, "#", 2)[0])
}

func portedFile(file string) bool {
	return strings.Contains(file, "upstream") || strings.Contains(file, "conformance") || strings.Contains(file, "oracle")
}

// asserts reports whether the function or a helper within three calls reports failures.
func (ix *index) asserts(key string, depth int, seen map[string]bool) bool {
	if seen[key] || depth > 3 {
		return false
	}
	seen[key] = true
	fi := ix.fns[key]
	if fi == nil {
		return false
	}
	if fi.asserts {
		return true
	}
	for c := range fi.callees {
		if ix.asserts(c, depth+1, seen) {
			return true
		}
	}
	return false
}

// testsReaching returns the Test functions that reach a helper within three calls.
func (ix *index) testsReaching(helper string) []string {
	if ix.reverse == nil {
		ix.reverse = map[string][]string{}
		for k, fi := range ix.fns {
			for c := range fi.callees {
				ix.reverse[c] = append(ix.reverse[c], k)
			}
		}
	}
	reverse := ix.reverse
	var out []string
	seen := map[string]bool{helper: true}
	frontier := []string{helper}
	for depth := 0; depth < 3 && len(frontier) > 0; depth++ {
		var next []string
		for _, k := range frontier {
			for _, c := range reverse[k] {
				if seen[c] {
					continue
				}
				seen[c] = true
				if isTestFunc(c) {
					out = append(out, c)
				} else {
					next = append(next, c)
				}
			}
		}
		frontier = next
	}
	return out
}

// testsFor lists the Test functions that reference any of the declaration keys, best evidence first.
func (ix *index) testsFor(keys []string) []testEv {
	byTest := map[string]*testEv{}
	note := func(testKey string, direct, called bool) {
		ev := byTest[testKey]
		if ev == nil {
			file, _, _ := strings.Cut(testKey, "#")
			ev = &testEv{Ref: "test:" + testKey, Ported: portedFile(file), Asserts: ix.asserts(testKey, 0, map[string]bool{})}
			byTest[testKey] = ev
		}
		ev.Uses++
		ev.Direct = ev.Direct || direct
		ev.Called = ev.Called || called
	}
	for _, k := range keys {
		for _, u := range ix.uses[k] {
			if !u.Test {
				continue
			}
			// A call through an interface method is attributed to every implementer (expandDispatch). It is evidence for an implementer
			// only when the test's package can hold that type, and it ranks below a test that names the declaration itself.
			if u.Dispatch && !ix.canHold(u.File, k) {
				continue
			}
			if isTestFunc(u.key()) {
				note(u.key(), !u.Dispatch, u.Call && u.ResultUsed)
				continue
			}
			for _, t := range ix.testsReaching(u.key()) {
				note(t, false, false)
			}
		}
	}
	out := make([]testEv, 0, len(byTest))
	for _, ev := range byTest {
		out = append(out, *ev)
	}
	sort.Slice(out, func(i, j int) bool {
		if ri, rj := out[i].rank(), out[j].rank(); ri != rj {
			return ri > rj
		}
		return out[i].Ref < out[j].Ref
	})
	return out
}

// evidenceCap is the most tests one derived row lists as evidence.
const evidenceCap = 3

// pickEvidence takes the evidence of s from its tests, best first: the asserting tests, at most evidenceCap. runsFile reports whether
// a picked test runs the mapped Go file (a reference to a function or method with a body in it, within three helper calls: a static
// reading, since portmap-check measures the execution itself); spare lists the asserting tests the cap left out that do.
func (d *detector) pickEvidence(s *sym, tests []testEv) (picked []string, runsFile bool, spare []testEv) {
	runs := d.ix.testsExecuting(s.Dir, s.File)
	for _, t := range tests {
		if !t.Asserts {
			continue
		}
		switch {
		case len(picked) < evidenceCap:
			picked = append(picked, t.Ref)
			runsFile = runsFile || runs[t.Ref]
		case runs[t.Ref]:
			spare = append(spare, t)
		}
	}
	return picked, runsFile, spare
}

// keepFileRunners keeps the cap from dropping the last test that runs a mapped Go file. portmap-check (rule 3) asks the evidence
// tests of one Pi file, the union over its members, to execute a statement of every Go file the members map to. A field or type
// row has no statement of its own, so a file whose only function is not a ledger member (source_info.go's GetSourcePackageKey) is
// run by the one test that happens to call it, and a new higher-ranked test for a member displaces that test from the list and
// leaves the file unexecuted. For each Pi declaration file and mapped Go file whose rows list no test that runs the Go file, the
// best-ranked test that does (a spare of one of those rows) takes the last place of its row. Nothing changes where a row of the pair
// already lists such a test.
func (d *detector) keepFileRunners(ds []*decision) {
	type pair struct{ source, file string }
	type pick struct {
		d  *decision
		ev testEv
	}
	has := map[pair]bool{}
	best := map[pair]pick{}
	for _, dec := range ds { // sorted by ID, so a tie goes to the first ID
		if dec.Gap || dec.Info == nil || dec.Sym == nil {
			continue
		}
		entry := d.l.byID[dec.ID]
		if entry == nil {
			continue
		}
		key := pair{entry.SourcePath, dec.Sym.File}
		if dec.Info.RunsFile {
			has[key] = true
			continue
		}
		for _, ev := range dec.Info.Spare {
			if cur, ok := best[key]; !ok || ev.rank() > cur.ev.rank() {
				best[key] = pick{dec, ev}
			}
		}
	}
	for key, p := range best {
		if !has[key] {
			p.d.Info.Tests = slices.Clone(p.d.Info.Tests)
			p.d.Info.Tests[evidenceCap-1] = p.ev.Ref
			p.d.Info.RunsFile = true
		}
	}
}

// testsExecuting is the set of test refs that reference a function or method with a body declared in the non-test Go file, directly
// or through a test helper, memoized per file.
func (ix *index) testsExecuting(dir, file string) map[string]bool {
	if runs, ok := ix.executing[file]; ok {
		return runs
	}
	runs := map[string]bool{}
	if info := ix.pkgs[dir]; info != nil {
		var keys []string
		for _, f := range info.Files {
			if ix.rel(ix.pos(f.Pos()).Filename) != file {
				continue
			}
			for _, decl := range f.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
					if obj := info.Info.Defs[fn.Name]; obj != nil {
						keys = append(keys, ix.declKey(obj))
					}
				}
			}
		}
		for _, ev := range ix.testsFor(keys) {
			runs[ev.Ref] = true
		}
	}
	if ix.executing == nil {
		ix.executing = map[string]map[string]bool{}
	}
	ix.executing[file] = runs
	return runs
}

// prodFor lists the production functions that reference any of the declaration keys, reachable ones first. exclude
// drops the declaration's own functions (recursion, a type's own methods).
func (ix *index) prodFor(keys []string, dir string, exclude func(caller string) bool) (reachable, unreachable []string) {
	seen := map[string]bool{}
	var all []use
	for _, k := range keys {
		for _, u := range ix.uses[k] {
			if u.Test || exclude(u.Caller) || seen[u.key()] {
				continue
			}
			seen[u.key()] = true
			all = append(all, u)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		si, sj := strings.HasPrefix(all[i].File, dir+"/"), strings.HasPrefix(all[j].File, dir+"/")
		if si != sj {
			return si
		}
		return all[i].key() < all[j].key()
	})
	for _, u := range all {
		if ix.reach[u.key()] {
			reachable = append(reachable, u.key())
		} else {
			unreachable = append(unreachable, u.key())
		}
	}
	return reachable, unreachable
}
