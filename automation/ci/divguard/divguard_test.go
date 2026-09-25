// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fixtureEnv is the repository context the fixtures run against: a tiny
// upstream mirror, one recorded divergence (D56), and one PORT_MAP mapping.
func fixtureEnv(t *testing.T) *env {
	t.Helper()
	idx, err := loadUpstreamIndex(filepath.Join("testdata", "mirror"))
	if err != nil {
		t.Fatal(err)
	}
	return &env{
		Upstream:    idx,
		Divergences: map[string]bool{"D56": true},
		PortMap:     map[string][]string{"agent/mapped.go": {"packages/agent/src/mapped.ts"}},
	}
}

var (
	fixturePathRe = regexp.MustCompile(`^// divguard:path (\S+)`)
	wantRe        = regexp.MustCompile(`// want\b`)
)

// runFixture scans testdata/<check>/<name> as the repository file named by
// its `// divguard:path` header with only that check enabled.
func runFixture(t *testing.T, c check, name string) (hits []Hit, problems []string, want []int) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", c.Name, name))
	if err != nil {
		t.Fatalf("every check needs testdata/%s/%s: %v", c.Name, name, err)
	}
	m := fixturePathRe.FindSubmatch(src)
	if m == nil {
		t.Fatalf("testdata/%s/%s: first line must be `// divguard:path <repo path>`", c.Name, name)
	}
	rel := string(m[1])
	if !c.Applies(rel) {
		t.Fatalf("testdata/%s/%s: check does not apply to %s", c.Name, name, rel)
	}
	for i, line := range strings.Split(string(src), "\n") {
		if wantRe.MatchString(line) {
			want = append(want, i+1)
		}
	}
	hits, problems, err = scanSource(rel, src, fixtureEnv(t), []check{c})
	if err != nil {
		t.Fatal(err)
	}
	return hits, problems, want
}

func hitLines(hits []Hit) []int {
	var lines []int
	for _, h := range hits {
		lines = append(lines, h.Line)
	}
	return lines
}

// TestChecksFlagBadAndPassGoodFixtures proves every check: its bad fixture
// reproduces the pattern behind the original findings and must be flagged on
// exactly the `// want` lines, and its good fixture must pass untouched.
func TestChecksFlagBadAndPassGoodFixtures(t *testing.T) {
	for _, c := range checks {
		t.Run(c.Name, func(t *testing.T) {
			hits, _, want := runFixture(t, c, "bad.txt")
			if len(want) == 0 {
				t.Fatal("bad.txt marks no `// want` lines")
			}
			if got := hitLines(hits); !slices.Equal(got, want) {
				t.Errorf("bad.txt flagged lines %v, want %v\n%v", got, want, hits)
			}
			hits, problems, want := runFixture(t, c, "good.txt")
			if len(want) != 0 {
				t.Fatal("good.txt must not mark `// want` lines")
			}
			if len(hits) != 0 || len(problems) != 0 {
				t.Errorf("good.txt flagged %v, problems %v", hits, problems)
			}
		})
	}
}

func TestMagicLiteralNamesTheLiteralAndItsDeclaration(t *testing.T) {
	hits, _, _ := runFixture(t, magicLiteral, "bad.txt")
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	h := hits[0]
	if h.Func != "NewAgent" || h.Snippet != "opts.MaxTurns = 100" || !strings.Contains(h.Message, "MaxTurns = 100") {
		t.Errorf("first hit = %+v", h)
	}
}

// The marker is assembled at run time so the repository's divergence
// consistency gate never sees an unrecorded id in a source file.
func TestUnrecordedDivergenceMarkerIsAProblem(t *testing.T) {
	src := []byte("package agent\n\n// pig divergence (D" + "999): not in the ledger.\nconst maxQueue = 64\n")
	hits, problems, err := scanSource("agent/x.go", src, fixtureEnv(t), []check{magicLiteral})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || len(problems) != 1 || !strings.Contains(problems[0], "D999") {
		t.Errorf("hits = %v, problems = %v; want the literal flagged and one problem naming D999", hits, problems)
	}
}

func TestUpstreamReferenceMustExist(t *testing.T) {
	src := []byte("package agent\n\nimport \"time\"\n\n// upstream: agent/src/missing.ts:thing\nconst waitTimeout = 30 * time.Second\n\n// upstream: agent/src/agent-loop.ts:noSuchSymbol\nconst otherTimeout = 2 * time.Second\n")
	hits, problems, err := scanSource("agent/x.go", src, fixtureEnv(t), []check{magicLiteral})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Errorf("hits = %v, want both literals flagged", hits)
	}
	if len(problems) != 2 || !strings.Contains(problems[0], "no file") || !strings.Contains(problems[1], "noSuchSymbol") {
		t.Errorf("problems = %v", problems)
	}
}

func TestUpstreamReferenceNeedsAUniqueFile(t *testing.T) {
	idx := fixtureEnv(t).Upstream
	if _, err := idx.resolve("agent-loop.ts"); err != nil {
		t.Errorf("bare file name should resolve by suffix: %v", err)
	}
	if _, err := idx.resolve("packages/agent/src/agent-loop.ts"); err != nil {
		t.Errorf("mirror-relative path should resolve: %v", err)
	}
	if _, err := idx.resolve("src/nothing.ts"); err == nil {
		t.Error("missing file resolved")
	}
}

func TestPortMapExpandsBracesAndBareNames(t *testing.T) {
	root := t.TempDir()
	portMap := "| `packages/agent/src/a.ts` | `agent/harness/env/{env,exec}.go` | ✅ |\n" +
		"| `packages/agent/src/b.ts` | `internal/codingagent/tools/bash.go + bash_executor.go (note)` | ✅ |\n" +
		"| `packages/agent/src/c.ts` | `(not needed)` | n/a |\n"
	if err := os.WriteFile(filepath.Join(root, "PORT_MAP.md"), []byte(portMap), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := loadPortMap(root)
	if err != nil {
		t.Fatal(err)
	}
	for goFile, up := range map[string]string{
		"agent/harness/env/env.go":                    "packages/agent/src/a.ts",
		"agent/harness/env/exec.go":                   "packages/agent/src/a.ts",
		"internal/codingagent/tools/bash.go":          "packages/agent/src/b.ts",
		"internal/codingagent/tools/bash_executor.go": "packages/agent/src/b.ts",
	} {
		if !slices.Contains(m[goFile], up) {
			t.Errorf("%s maps to %v, want %s", goFile, m[goFile], up)
		}
	}
	if len(m) != 4 {
		t.Errorf("map = %v", m)
	}
}

func writeBaseline(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "baseline.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const fixtureFinding = "[[finding]]\nid = \"GUARD-01\"\nowner = \"agent-stall\"\nsummary = \"turn cap\"\n"

func fixtureHit(snippet string) Hit {
	return Hit{Check: magicLiteralName, File: "agent/agent.go", Func: "NewAgent", Snippet: snippet}
}

func TestBaselineRatchetsBothWays(t *testing.T) {
	path := writeBaseline(t, fixtureFinding+`
[[hit]]
check = "magic-literal"
file = "agent/agent.go"
func = "NewAgent"
snippet = "opts.MaxTurns = 100"
finding = "GUARD-01"

[[hit]]
check = "magic-literal"
file = "agent/agent.go"
func = "NewAgent"
snippet = "fixed = 5"
finding = "GUARD-01"
count = 2
`)
	base, err := loadBaseline(path, fixtureEnv(t), checks)
	if err != nil {
		t.Fatal(err)
	}
	hits := []Hit{fixtureHit("opts.MaxTurns = 100"), fixtureHit("opts.MaxTurns = 100"), fixtureHit("fixed = 5"), fixtureHit("new = 7")}
	unknown, stale := compare(hits, base)
	var got []string
	for _, h := range unknown {
		got = append(got, h.Snippet)
	}
	if !slices.Equal(got, []string{"opts.MaxTurns = 100", "new = 7"}) {
		t.Errorf("unknown = %v, want the second MaxTurns hit and the new hit", unknown)
	}
	if len(stale) != 1 || !strings.Contains(stale[0], "fixed = 5") || !strings.Contains(stale[0], "expected 2, found 1") {
		t.Errorf("stale = %v, want the entry whose count dropped", stale)
	}
}

func TestBaselineRejectsUntiedEntries(t *testing.T) {
	entry := "\n[[hit]]\ncheck = \"magic-literal\"\nfile = \"a.go\"\nfunc = \"F\"\nsnippet = \"x = 5\"\nfinding = %q\n"
	for name, tc := range map[string]struct{ body, want string }{
		"undefined finding": {strings.Replace(entry, "%q", `"AGENT-01"`, 1), "has no [[finding]] entry"},
		"bad id":            {fixtureFinding + strings.Replace(entry, "%q", `"TODO"`, 1), "finding must be"},
		"unused finding":    {fixtureFinding + "\n[[finding]]\nid = \"AI-01\"\nowner = \"x\"\nsummary = \"y\"\n" + strings.Replace(entry, "%q", `"GUARD-01"`, 1), "AI-01 has no hits left"},
		"unrecorded DN":     {"[[finding]]\nid = \"D7\"\nowner = \"x\"\nsummary = \"y\"\n" + strings.Replace(entry, "%q", `"D7"`, 1), "not recorded"},
		"unknown check":     {fixtureFinding + strings.Replace(strings.Replace(entry, "%q", `"GUARD-01"`, 1), "magic-literal", "nope", 1), "unknown check"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadBaseline(writeBaseline(t, tc.body), fixtureEnv(t), checks)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestBaselineFileIsValid(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	e := &env{Divergences: map[string]bool{}}
	for _, ledger := range []string{"DIVERGENCES.md", filepath.Join("docs", "additive-features.md")} {
		data, err := os.ReadFile(filepath.Join(root, ledger))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range ledgerHeadingRe.FindAllSubmatch(data, -1) {
			e.Divergences[string(m[1])] = true
		}
	}
	if _, err := loadBaseline("baseline.toml", e, checks); err != nil {
		t.Fatal(err)
	}
}

// TestRatchetKeysTheWholeLine is the CDG-003 regression: a cap changed past
// the display width must change the key, so the old entry goes stale and the
// new value is a new hit.
func TestRatchetKeysTheWholeLine(t *testing.T) {
	scan := func(n string) []Hit {
		src := []byte("package agent; func F(){ cfg := Config{Description: \"" + strings.Repeat("a", 160) + "\", MaxTurns: " + n + "}; use(cfg) }")
		hits, _, err := scanSource("agent/x.go", src, fixtureEnv(t), []check{magicLiteral})
		if err != nil {
			t.Fatal(err)
		}
		return hits
	}
	old, changed := scan("100"), scan("200")
	if len(old) != 1 || len(changed) != 1 {
		t.Fatalf("want one hit each, got %v and %v", old, changed)
	}
	h := old[0]
	base := map[string]baselineEntry{h.Key(): {Check: h.Check, File: h.File, Func: h.Func, Snippet: h.Snippet, Finding: "GUARD-01"}}
	unknown, stale := compare(changed, base)
	if len(unknown) != 1 || len(stale) != 1 {
		t.Fatalf("cap 100 -> 200 passed an unchanged baseline: unknown=%v stale=%v", unknown, stale)
	}
	if !strings.HasSuffix(h.Snippet, "MaxTurns: 100}; use(cfg) }") {
		t.Errorf("snippet was truncated: %q", h.Snippet)
	}
	if d := display(h.Snippet); len([]rune(d)) != 141 {
		t.Errorf("display should shorten long snippets, got %d runes", len([]rune(d)))
	}
}

// TestCapacityOneQueueInALoopIsFlagged is the CDG-002 regression: a
// capacity-1 local fed from a loop loses every event after the first.
func TestCapacityOneQueueInALoopIsFlagged(t *testing.T) {
	src := []byte("package agent\nfunc forward(events []Event) { out := make(chan Event, 1); for _, event := range events { select {case out <- event: default:} } }\n")
	hits, problems, err := scanSource("agent/x.go", src, fixtureEnv(t), []check{defaultDropSend})
	if err != nil || len(problems) > 0 {
		t.Fatalf("parse: %v %v", err, problems)
	}
	if len(hits) != 1 {
		t.Fatalf("capacity-1 event loop was exempted: %v", hits)
	}
}

// TestCapacityOneChannelWithMultipleSendsIsFlagged is the second CDG-002
// regression: one binding does not make multiple sends one-shot. Ordinary
// blocking sends count because they can prefill the channel before the
// non-blocking send.
func TestCapacityOneChannelWithMultipleSendsIsFlagged(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"two non-blocking sends": {body: "select {case out <- first: default:}; select {case out <- second: default:}", want: 2},
		"blocking prefill":       {body: "out <- first; select {case out <- second: default:}", want: 1},
		"alias escapes":          {body: "alias := out; alias <- first; select {case out <- second: default:}", want: 1},
	} {
		t.Run(name, func(t *testing.T) {
			src := []byte("package agent\nfunc forward(first, second Event) { out := make(chan Event, 1); " + tc.body + " }\n")
			hits, problems, err := scanSource("agent/x.go", src, fixtureEnv(t), []check{defaultDropSend})
			if err != nil || len(problems) > 0 {
				t.Fatalf("parse: %v %v", err, problems)
			}
			if len(hits) != tc.want {
				t.Fatalf("capacity-1 channel with multiple sends: got %d hits, want %d: %v", len(hits), tc.want, hits)
			}
		})
	}
}

// TestCapacityOneChannelReturnedFromClosureIsFlagged is PR1-001: a nested
// closure can leak and prefill the channel before the candidate select.
func TestCapacityOneChannelReturnedFromClosureIsFlagged(t *testing.T) {
	src := []byte(`package agent
func forward(first, second Event) {
	out := make(chan Event, 1)
	leak := func() chan Event { return out }
	leak() <- first
	select {
	case out <- second:
	default:
	}
}
`)
	hits, problems, err := scanSource("agent/x.go", src, fixtureEnv(t), []check{defaultDropSend})
	if err != nil || len(problems) > 0 {
		t.Fatalf("scan: %v %v", err, problems)
	}
	if len(hits) != 1 {
		t.Fatalf("nested closure leaked and prefilled the channel, got %d hits, want 1: %v", len(hits), hits)
	}
}

// TestDiscardedPersistenceReturnIsFlagged is the CDG-001 regression: a bare
// persistence call and a blanked trailing error both lose the failure.
func TestDiscardedPersistenceReturnIsFlagged(t *testing.T) {
	for name, body := range map[string]string{"bare": "s.AppendMessage(msg)", "partial": "id, _ := s.AppendMessage(msg); use(id)"} {
		t.Run(name, func(t *testing.T) {
			hits, problems, err := scanSource("coding/x.go", []byte("package coding; func F(){"+body+"}"), fixtureEnv(t), []check{discardedIOError})
			if err != nil || len(problems) > 0 {
				t.Fatalf("parse: %v %v", err, problems)
			}
			if len(hits) != 1 {
				t.Fatalf("discarded persistence error was not flagged: %+v", hits)
			}
		})
	}
}

func TestPersistenceErrorReturnedAfterSuccessBranch(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"unchanged":  {body: "if _, err = s.AppendMessage(msg); err == nil { remember() }; cleanup(); return err", want: 0},
		"nested":     {body: "if ready { if _, err = s.AppendMessage(msg); err == nil { remember() } }; cleanup(); return err", want: 0},
		"reassigned": {body: "if _, err = s.AppendMessage(msg); err == nil { remember() }; err = nil; return err", want: 1},
	} {
		t.Run(name, func(t *testing.T) {
			src := []byte("package coding; func F() error { var err error; " + tc.body + " }")
			hits, problems, err := scanSource("coding/x.go", src, fixtureEnv(t), []check{discardedIOError})
			if err != nil || len(problems) > 0 {
				t.Fatalf("parse: %v %v", err, problems)
			}
			if len(hits) != tc.want {
				t.Fatalf("got %d hits, want %d: %+v", len(hits), tc.want, hits)
			}
		})
	}
}

func TestErrorFuncsIndexesDeclarationsAndFuncFields(t *testing.T) {
	src := "package x\ntype S struct{ AppendText func(string) }\nfunc (S) AppendMessage(string) (string, error) { return \"\", nil }\nfunc SaveAll() {}\n"
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := errorFuncs{}
	m.add(f)
	if !m.returnsError("AppendMessage") || m.returnsNoError("AppendMessage") {
		t.Error("AppendMessage should return error")
	}
	if !m.returnsNoError("AppendText") || !m.returnsNoError("SaveAll") {
		t.Error("AppendText field and SaveAll should be known to return no error")
	}
}
