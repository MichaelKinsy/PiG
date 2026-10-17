// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var (
	buildOnce sync.Once
	builtRep  *Report
	builtErr  error
	builtDir  string
)

// built builds the report of this repository once and writes it to a directory shared by the tests of the package.
func built(t *testing.T) (*Report, string) {
	t.Helper()
	root := testenv.ModuleRoot(t)
	buildOnce.Do(func() {
		builtRep, builtErr = Build(Options{Root: root})
		if builtErr != nil {
			return
		}
		builtDir, builtErr = os.MkdirTemp("", "portlens-test-")
		if builtErr != nil {
			return
		}
		builtErr = WriteReport(builtRep, builtDir)
	})
	if builtErr != nil {
		t.Fatal(builtErr)
	}
	return builtRep, builtDir
}

func TestMain(m *testing.M) {
	code := m.Run()
	if builtDir != "" {
		_ = os.RemoveAll(builtDir)
	}
	os.Exit(code)
}

// readJS decodes the JSON argument of a one-call script file such as PL.ledger({...});
func readJS(t *testing.T, path, fn string, into any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, fn+"(") || !strings.HasSuffix(s, ");") {
		t.Fatalf("%s is not a %s call", path, fn)
	}
	if err := json.Unmarshal([]byte(s[len(fn)+1:len(s)-2]), into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// independentIDs reads the interface ids of a mapping ledger without the report's loader.
func independentIDs(t *testing.T, root, version string) []string {
	t.Helper()
	var doc struct {
		Mappings []struct {
			ID string `json:"id"`
		} `json:"mappings"`
	}
	b, err := os.ReadFile(filepath.Join(root, "test", "parity", "interfaces", "mapping-v"+version+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(doc.Mappings))
	for i, m := range doc.Mappings {
		ids[i] = m.ID
	}
	return ids
}

// Every row of every ledger the report shows is in its data file, and every gap-baseline row is marked as a gap.
func TestEveryLedgerRowAppearsInThePortMapData(t *testing.T) {
	rep, dir := built(t)
	root := rep.Opts.Root
	if len(rep.Ledgers) == 0 {
		t.Fatal("no ledgers")
	}
	for _, pl := range rep.Ledgers {
		want := independentIDs(t, root, pl.Version)
		var env struct {
			ID   string     `json:"id"`
			Data ledgerData `json:"data"`
		}
		readJS(t, filepath.Join(dir, "data", "ledger-"+ledgerID(pl.Version)+".js"), "PL.ledger", &env)
		got := map[string]int{}
		for i, r := range env.Data.Rows {
			got[r[0].(string)] = i
		}
		if len(env.Data.Rows) != len(want) || len(got) != len(want) {
			t.Fatalf("pin %s: %d rows in the data, %d distinct, %d in the ledger", pl.Version, len(env.Data.Rows), len(got), len(want))
		}
		for _, id := range want {
			if _, ok := got[id]; !ok {
				t.Fatalf("pin %s: ledger row %q is not in the port map data", pl.Version, id)
			}
		}
		for _, ln := range readLines(filepath.Join(root, "test", "parity", "interface-closure", "gaps", "interface-gaps-v"+pl.Version+".tsv")) {
			id, _, _ := strings.Cut(ln, "\t")
			i, ok := got[id]
			if !ok {
				t.Fatalf("pin %s: gap baseline row %q is not a ledger row", pl.Version, id)
			}
			if code := int(env.Data.Rows[i][1].(float64)); code != statusCode[StatusGap] {
				t.Fatalf("pin %s: gap baseline row %q has status code %d, want gap", pl.Version, id, code)
			}
		}
	}
}

// readFrag decodes a PL.frag("name","html"); script.
func readFrag(t *testing.T, path string) (name, html string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "PL.frag(") || !strings.HasSuffix(s, ");") {
		t.Fatalf("%s is not a PL.frag call", path)
	}
	var parts []string
	if err := json.Unmarshal([]byte("["+s[len("PL.frag("):len(s)-2]+"]"), &parts); err != nil || len(parts) != 2 {
		t.Fatalf("%s: %v", path, err)
	}
	return parts[0], parts[1]
}

var (
	dHeading = regexp.MustCompile(`(?m)^## D(\d+)\b`)
	dRetired = regexp.MustCompile("(?m)^- D(\\d+) \u2014 ")
	idAttr   = regexp.MustCompile(`<details class="(?:dv|bg)" id="([^"]+)"`)
)

// Every D-number of both divergence documents and every bug ledger entry is in its view, and no D-number is invented.
func TestEveryDivergenceAndBugAppears(t *testing.T) {
	rep, dir := built(t)
	root := rep.Opts.Root
	want := map[string]bool{}
	for _, f := range []string{"docs/parity/DIVERGENCES.md", "docs/additive-features.md"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range dHeading.FindAllStringSubmatch(string(b), -1) {
			want["D"+m[1]] = true
		}
		for _, m := range dRetired.FindAllStringSubmatch(string(b), -1) {
			want["D"+m[1]] = true
		}
	}
	if len(want) < 100 {
		t.Fatalf("only %d D-numbers found in the documents", len(want))
	}
	_, html := readFrag(t, filepath.Join(dir, "views", "divergences.js"))
	got := map[string]bool{}
	for _, m := range idAttr.FindAllStringSubmatch(html, -1) {
		got[m[1]] = true
	}
	for d := range want {
		if !got[d] {
			t.Errorf("%s is in the divergence documents and not in the report", d)
		}
	}
	for d := range got {
		if !want[d] {
			t.Errorf("%s is in the report and not in the divergence documents", d)
		}
	}

	var ledger struct {
		Entries []struct {
			ID string `json:"id"`
		} `json:"entries"`
		Divergences []struct {
			ID string `json:"id"`
		} `json:"divergences"`
	}
	b, err := os.ReadFile(filepath.Join(root, "docs", "bug-ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &ledger); err != nil {
		t.Fatal(err)
	}
	_, bugs := readFrag(t, filepath.Join(dir, "views", "bugs.js"))
	bugIDs := map[string]bool{}
	for _, m := range idAttr.FindAllStringSubmatch(bugs, -1) {
		bugIDs[m[1]] = true
	}
	if len(ledger.Entries) < 100 || len(bugIDs) != len(ledger.Entries) {
		t.Fatalf("%d bug entries in the report, %d in the ledger", len(bugIDs), len(ledger.Entries))
	}
	for _, e := range ledger.Entries {
		if !bugIDs[e.ID] {
			t.Errorf("bug %s is not in the report", e.ID)
		}
	}
	for _, d := range ledger.Divergences {
		if !want[d.ID] {
			t.Errorf("the bug ledger accounts for %s, which has no divergence record", d.ID)
		}
	}
}

// A note's Pi citations and PiG marker sites become highlighted snippets, a Pi issue becomes a link, and the class is derived from the bug ledger.
func TestDivergenceRecordsCarrySnippetsLinksAndClass(t *testing.T) {
	rep, _ := built(t)
	byID := map[string]*Divergence{}
	for _, d := range rep.Divergences {
		byID[d.ID] = d
	}
	d := byID["D120"]
	if d == nil {
		t.Fatal("D120 is not in the report")
	}
	if d.Class != ClassPiBug || len(d.PiIssues) == 0 || !strings.HasSuffix(d.PiIssues[0].URL, "/issues/10299") {
		t.Errorf("D120: class %q, issues %v", d.Class, d.PiIssues)
	}
	if len(d.GoSnips) == 0 || !strings.Contains(d.GoSnips[0].HTML, `<span class="`) {
		t.Errorf("D120: no highlighted Go snippet: %+v", d.GoSnips)
	}
	if len(d.Tests) == 0 || d.Tests[0].File == "" {
		t.Errorf("D120: tests are not resolved to a file and line: %+v", d.Tests)
	}
	if len(d.PiSnips) == 0 {
		t.Fatal("D120 cites google-generative-ai.ts:195-200 and has no Pi snippet")
	}
	for _, s := range d.PiSnips {
		if rep.PiAvailable && (s.Unavailable != "" || !strings.Contains(s.HTML, `<span class="`)) {
			t.Errorf("the Pi mirror is present and the snippet %+v is unavailable or not highlighted", s)
		}
		if !rep.PiAvailable && s.Unavailable == "" {
			t.Errorf("the Pi mirror is absent and the snippet %+v does not say so", s)
		}
	}
	// Every Pi snippet of every record shows exactly the cited lines of the pinned Pi file, read here from the mirror without the report's loader.
	for _, dv := range rep.Divergences {
		for _, s := range dv.PiSnips {
			if s.Unavailable != "" {
				continue
			}
			b, err := os.ReadFile(filepath.Join(rep.PiMirror, filepath.FromSlash(s.Path)))
			if err != nil {
				t.Fatalf("%s: %v", dv.ID, err)
			}
			src := strings.Split(string(b), "\n")
			if s.Start < 1 || s.End > len(src) {
				t.Errorf("%s: %s:%d-%d is outside the file", dv.ID, s.Path, s.Start, s.End)
				continue
			}
			// A trailing blank line of the range carries no text, so it may be absent from the snippet.
			want, got := trimBlankTail(src[s.Start-1:s.End]), trimBlankTail(strings.Split(plainText(s.HTML), "\n"))
			if len(got) != len(want) {
				t.Errorf("%s: %s:%d-%d shows %d lines, Pi %s has %d", dv.ID, s.Path, s.Start, s.End, len(got), rep.Pin.Version, len(want))
				continue
			}
			for i := range want {
				if strings.TrimSpace(got[i]) != strings.TrimSpace(want[i]) {
					t.Errorf("%s: %s:%d shows %q, Pi %s has %q", dv.ID, s.Path, s.Start+i, got[i], rep.Pin.Version, want[i])
					break
				}
			}
		}
	}
	// D2 (a separate command identity) is a deliberate difference, not a Pi bug: only a bug ledger entry or a cited Pi issue makes a record a Pi bug fix.
	if c := byID["D2"].Class; c != ClassOther {
		t.Errorf("D2: class %q, want %q", c, ClassOther)
	}
	for _, id := range []string{"D2", "D18"} {
		if byID[id] == nil {
			t.Fatalf("%s is not in the report", id)
		}
	}
	if byID["D18"].Class != ClassAdditive || byID["D18"].Status != DivAdditive {
		t.Errorf("D18 is in docs/additive-features.md: class %q status %q", byID["D18"].Class, byID["D18"].Status)
	}
}

func trimBlankTail(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// deny lists private coordination data that must never reach a published report. The literals are assembled so this file does not match itself.
var deny = regexp.MustCompile(
	`imla` + `dris|\bHP` + `E\b|Hewlett|Mainst` + `AI|/home/[a-z]|/Users/[a-z]|pig-` + `lanes|pig-` + `staging|smc` + `1\b|\.dev` + `cache|[A-Za-z0-9._-]+@[A-Za-z0-9-]+\.(?:com|net|org)\b`)

// The output carries no private host, path, address or organization name.
func TestOutputHasNoPrivateData(t *testing.T) {
	_, dir := built(t)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if m := deny.Find(b); m != nil {
			t.Errorf("%s contains %q", p, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func digestTree(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = sha256.Sum256(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Writing the same report twice gives the same bytes, and the output stands alone: it loads no remote resource.
func TestOutputIsDeterministicAndSelfContained(t *testing.T) {
	rep, dir := built(t)
	again := t.TempDir()
	if err := WriteReport(rep, again); err != nil {
		t.Fatal(err)
	}
	a, b := digestTree(t, dir), digestTree(t, again)
	if len(a) != len(b) {
		t.Fatalf("%d files, then %d", len(a), len(b))
	}
	var diff []string
	for k, v := range a {
		if b[k] != v {
			diff = append(diff, k)
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Fatalf("not deterministic: %v", diff)
	}
	remote := regexp.MustCompile(`(?:src|href)="(?:https?:)?//[^"]*"`)
	for _, f := range []string{"index.html"} {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		for _, m := range remote.FindAll(b, -1) {
			if !strings.Contains(string(m), "github.com/earendil-works/pi") {
				t.Errorf("%s loads a remote resource: %s", f, m)
			}
		}
	}
	for _, f := range []string{"assets/style.css", "assets/app.js"} {
		b, _ := os.ReadFile(filepath.Join(dir, f))
		if regexp.MustCompile(`@import|url\(https?:|fetch\(|XMLHttpRequest`).Match(b) {
			t.Errorf("%s reaches for the network", f)
		}
	}
}

// The report replaces its own earlier output and refuses any other non-empty directory, whose data, views or samples subdirectory it would delete.
func TestWriteReportRefusesADirectoryItDidNotWrite(t *testing.T) {
	rep, dir := built(t)
	if err := WriteReport(rep, dir); err != nil {
		t.Fatalf("rewriting an earlier report: %v", err)
	}
	other := t.TempDir()
	writeFile(t, other, "data/keep.txt", "not the report's")
	if err := WriteReport(rep, other); err == nil {
		t.Fatal("WriteReport wrote into a directory that holds other files")
	}
	if _, err := os.Stat(filepath.Join(other, "data", "keep.txt")); err != nil {
		t.Fatalf("a file of the refused directory is gone: %v", err)
	}
	if err := WriteReport(rep, filepath.Join(other, "new")); err != nil {
		t.Fatalf("a new directory: %v", err)
	}
}

// The -serve option serves the same files over HTTP.
func TestServeServesTheOutputDirectory(t *testing.T) {
	_, dir := built(t)
	ts := httptest.NewServer(newServer("", dir).Handler)
	defer ts.Close()
	for path, want := range map[string]string{"/": "PiG port lens", "/views/divergences.js": "PL.frag(", "/assets/app.js": "use strict"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || !strings.Contains(string(body), want) {
			t.Errorf("%s: status %d, body does not contain %q", path, resp.StatusCode, want)
		}
	}
}
