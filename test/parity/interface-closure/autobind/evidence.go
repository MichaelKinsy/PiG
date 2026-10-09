package main

import (
	"sort"
	"strings"
)

// testEv is one test function that references a symbol.
type testEv struct {
	Ref     string `json:"ref"`
	Direct  bool   `json:"direct"`  // the test body itself references the symbol
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
			if isTestFunc(u.key()) {
				note(u.key(), true, u.Call && u.ResultUsed)
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
