// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// snipLines is the longest Go snippet of a row.
const snipLines = 16

// snipShards is the number of snippet files; a row's snippet is read from the shard of its key.
const snipShards = 32

var statusCode = map[string]int{StatusPorted: 0, StatusDerived: 1, StatusDesignedOut: 2, StatusGap: 3}

type strTable struct {
	idx  map[string]int
	list []string
}

func newStrTable() *strTable { return &strTable{idx: map[string]int{}} }

func (t *strTable) add(s string) int {
	if i, ok := t.idx[s]; ok {
		return i
	}
	t.idx[s] = len(t.list)
	t.list = append(t.list, s)
	return len(t.list) - 1
}

// ledgerData is the JSON of one pin ledger as the port map loads it.
type ledgerData struct {
	Version   string         `json:"version"`
	Role      string         `json:"role"`
	Pkgs      []string       `json:"pkgs"`
	Fams      []string       `json:"fams"`
	Files     [][]any        `json:"files"`
	Str       []string       `json:"str"`
	Rat       []string       `json:"rat"`
	Rows      [][]any        `json:"rows"`
	Tok       []string       `json:"tok"`
	Post      [][]int        `json:"post"`
	ProofRead bool           `json:"proofRead"`
	GapBase   map[string]int `json:"gapBase"`
	ProofBase map[string]int `json:"proofBase"`
}

var camelRe = regexp.MustCompile(`[A-Z]+[a-z0-9]*|[a-z0-9]+`)

// tokenize lower-cases the words of s, splitting identifiers at camel-case boundaries. A whole identifier is also a token, so a full name finds its row.
func tokenize(s string, out map[string]struct{}) {
	var word strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		w := word.String()
		word.Reset()
		if len(w) >= 2 {
			out[strings.ToLower(w)] = struct{}{}
		}
		if hasMixedCase(w) {
			for _, p := range camelRe.FindAllString(w, -1) {
				if len(p) >= 2 {
					out[strings.ToLower(p)] = struct{}{}
				}
			}
		}
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
}

func hasMixedCase(w string) bool {
	up, low := false, false
	for _, r := range w {
		if unicode.IsUpper(r) {
			up = true
		} else if unicode.IsLower(r) {
			low = true
		}
	}
	return up && low
}

// templateRationale replaces the row's own names with placeholders so the many derived rationales share one table entry.
func templateRationale(r *Row) string {
	s := r.Rationale
	seen := map[string]bool{}
	for i, t := range r.Targets {
		ref := t.File + "#" + t.Sym
		if seen[ref] {
			continue
		}
		seen[ref] = true
		s = strings.ReplaceAll(s, ref, fmt.Sprintf("\x01%d\x02", i))
	}
	if r.Name != "" {
		s = strings.ReplaceAll(s, r.Name, "\x03")
	}
	return s
}

func buildLedgerData(pl *PinLedger, rep *Report) (*ledgerData, map[string]snipKey) {
	ld := &ledgerData{Version: pl.Version, Role: "pinned", ProofRead: pl.ProofRead, GapBase: pl.GapBaseline, ProofBase: pl.ProofBaseline}
	if !pl.Committed {
		ld.Role = "main-follow target"
	}
	str := newStrTable()
	rat := newStrTable()
	ld.Pkgs = pl.Packages
	pkgIdx := map[string]int{}
	for i, p := range pl.Packages {
		pkgIdx[p] = i
	}
	famIdx := map[string]int{}
	for _, f := range rep.Families {
		famIdx[f.Name] = 0
	}
	var fams []string
	for n := range famIdx {
		fams = append(fams, n)
	}
	sort.Strings(fams)
	for i, n := range fams {
		famIdx[n] = i + 1
	}
	ld.Fams = fams

	snips := map[string]snipKey{}
	type fileKey struct {
		pkg  int
		path string
	}
	fileIdx := map[fileKey]int{}
	post := map[string][]int{}
	for ri, r := range pl.Rows {
		fk := fileKey{pkgIdx[r.Pkg], r.TSFile}
		fi, ok := fileIdx[fk]
		if !ok {
			fi = len(ld.Files)
			fileIdx[fk] = fi
			ld.Files = append(ld.Files, []any{fk.pkg, r.TSFile, ri, ri, 0, 0, 0, 0})
		}
		f := ld.Files[fi]
		f[3] = ri + 1
		f[4+statusCode[r.Status]] = f[4+statusCode[r.Status]].(int) + 1

		refs := func(rs []Ref) []int {
			out := make([]int, 0, len(rs)*3)
			for _, x := range rs {
				out = append(out, str.add(x.File), x.Line, str.add(x.Sym))
			}
			return out
		}
		prod := make([]int, 0, len(r.Production)*4)
		for _, p := range r.Production {
			prod = append(prod, str.add(p.Kind), str.add(p.File), p.Line, str.add(p.Sym))
		}
		strOrNeg := func(s string) int {
			if s == "" {
				return -1
			}
			return str.add(s)
		}
		layers := ""
		if len(r.Layers) > 0 {
			ks := sortedKeys(r.Layers)
			parts := make([]string, 0, len(ks))
			for _, k := range ks {
				parts = append(parts, k+"="+r.Layers[k])
			}
			layers = strings.Join(parts, " ")
		}
		contracts := make([]int, 0, len(r.Contracts))
		for _, c := range r.Contracts {
			contracts = append(contracts, str.add(c))
		}
		proof := 0
		switch {
		case r.Unproven != "":
			proof = 2
		case r.Proven:
			proof = 1
		}
		divs := r.Divergences
		if divs == nil {
			divs = []string{}
		}
		row := []any{
			r.ID, statusCode[r.Status], r.Class, fi, r.Line, famIdx[r.Family], rat.add(templateRationale(r)),
			refs(r.Targets), prod, refs(r.Tests), refs(r.Reach), strOrNeg(layers), strOrNeg(r.Async), contracts,
			strOrNeg(r.GapReason), divs, boolInt(r.PiDecl != ""), proof, strOrNeg(r.Unproven), strOrNeg(r.Kind), strOrNeg(r.SourcePath),
		}
		ld.Rows = append(ld.Rows, row)

		if len(r.Targets) > 0 {
			t := r.Targets[0]
			if t.Line > 0 && !t.Missing {
				snips[snipID(t.File, t.Line)] = snippetRange(rep.Syms, t)
			}
		}

		toks := map[string]struct{}{}
		tokenize(strings.TrimPrefix(r.ID, "pkg:"), toks)
		tokenize(r.Status, toks)
		tokenize(r.Family, toks)
		tokenize(r.Kind, toks)
		tokenize(r.GapReason, toks)
		tokenize(r.TSFile, toks)
		for _, t := range r.Targets {
			tokenize(t.File, toks)
			tokenize(t.Sym, toks)
		}
		for _, t := range r.Tests {
			tokenize(t.Sym, toks)
		}
		for _, d := range r.Divergences {
			toks[strings.ToLower(d)] = struct{}{}
		}
		for t := range toks {
			post[t] = append(post[t], ri)
		}
	}
	toks := make([]string, 0, len(post))
	for t := range post {
		toks = append(toks, t)
	}
	sort.Strings(toks)
	ld.Tok = toks
	ld.Post = make([][]int, len(toks))
	for i, t := range toks {
		ids := post[t]
		d := make([]int, len(ids))
		prev := 0
		for j, id := range ids {
			d[j] = id - prev
			prev = id
		}
		ld.Post[i] = d
	}
	ld.Str = str.list
	ld.Rat = rat.list
	return ld, snips
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

type snipKey struct {
	File string
	Line int
	End  int
}

// snippetRange is the window of a target's snippet. A member of a type is shown inside its type, with the lines around it.
func snippetRange(syms *symIndex, t Ref) snipKey {
	k := snipKey{t.File, t.Line, t.End}
	top, _, member := strings.Cut(t.Sym, ".")
	if !member {
		return k
	}
	parent := syms.resolve(t.File, top)
	if parent.Line == 0 || parent.Line > t.Line || parent.End < t.End {
		return k
	}
	k.Line = max(parent.Line, t.Line-3)
	k.End = min(parent.End, t.End+4)
	return k
}

func snipID(file string, line int) string { return fmt.Sprintf("%s:%d", file, line) }

func shardOf(key string) int {
	h := fnv.New32a()
	h.Write([]byte(key))
	return int(h.Sum32() % snipShards)
}

// writeJS writes fn(JSON) as a script file.
func writeJS(path, fn string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(append([]byte(fn+"("), b...), []byte(");\n")...), 0o644)
}

// writePortmap writes the ledger data and snippet shards of every pin ledger.
func writePortmap(rep *Report, out string) error {
	allSnips := map[string]snipKey{}
	piDecls := map[string]string{}
	for _, pl := range rep.Ledgers {
		for _, r := range pl.Rows {
			if r.PiDecl != "" {
				piDecls["pi:"+ledgerID(pl.Version)+":"+r.ID] = r.PiDecl
			}
		}
		ld, snips := buildLedgerData(pl, rep)
		maps.Copy(allSnips, snips)
		if err := writeJS(filepath.Join(out, "data", "ledger-"+ledgerID(pl.Version)+".js"), "PL.ledger", map[string]any{"id": ledgerID(pl.Version), "data": ld}); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(allSnips)+len(piDecls))
	for k := range allSnips {
		keys = append(keys, k)
	}
	for k := range piDecls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rendered := make([]string, len(keys))
	var wg sync.WaitGroup
	work := make(chan int)
	for range 8 {
		wg.Go(func() {
			for i := range work {
				if decl, ok := piDecls[keys[i]]; ok {
					rendered[i] = "\n" + highlight("typescript", decl)
					continue
				}
				k := allSnips[keys[i]]
				end := k.End
				if end <= 0 {
					end = k.Line
				}
				text, a, b, cut := rep.Syms.text(k.File, k.Line, end, snipLines)
				if a == 0 {
					continue
				}
				head := fmt.Sprintf("%d-%d", a, b)
				if cut {
					head += "+"
				}
				rendered[i] = head + "\n" + highlight("go", text)
			}
		})
	}
	for i := range keys {
		work <- i
	}
	close(work)
	wg.Wait()
	shards := make([]map[string]string, snipShards)
	for i := range shards {
		shards[i] = map[string]string{}
	}
	for i, k := range keys {
		if rendered[i] != "" {
			shards[shardOf(k)][k] = rendered[i]
		}
	}
	for i, m := range shards {
		if err := writeJS(filepath.Join(out, "data", fmt.Sprintf("snip-%02d.js", i)), "PL.snips", m); err != nil {
			return err
		}
	}
	return nil
}
