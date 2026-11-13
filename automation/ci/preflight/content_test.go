// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func added(name string, lines ...string) textFile {
	t := textFile{name: name}
	for i, l := range lines {
		t.lines = append(t.lines, numberedLine{i + 1, l})
	}
	return t
}

func shippedContent(t *testing.T) contentRules {
	t.Helper()
	r, err := loadContentRules(forbiddenYAML, mandatoryYAML)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func forbiddenCodes(t *testing.T, files ...textFile) []string {
	t.Helper()
	var codes []string
	for _, f := range checkForbidden(shippedContent(t).Forbidden, files, map[string]string{"go": "1.27.3"}) {
		codes = append(codes, strings.SplitN(f.message, ":", 2)[0]+"@"+f.file)
	}
	return codes
}

func TestContentRulesRejected(t *testing.T) {
	for _, tc := range []struct{ name, forbidden, mandatory, want string }{
		{"no id", `[{code: X, description: d, pattern: x}]`, `[]`, "needs an id"},
		{"twice", `[{id: a, code: X, description: d, pattern: x}, {id: a, code: X, description: d, pattern: x}]`, `[]`, "declared twice"},
		{"shared with mandatory", `[{id: a, code: X, description: d, pattern: x}]`, `[{id: a, description: d, pattern: x}]`, "declared twice"},
		{"no code", `[{id: a, description: d, pattern: x}]`, `[]`, "needs a code"},
		{"pattern and match", `[{id: a, code: X, description: d, pattern: x, match: prose}]`, `[]`, "either a pattern or a match"},
		{"neither", `[{id: a, code: X, description: d}]`, `[]`, "either a pattern or a match"},
		{"broken pattern", `[{id: a, code: X, description: d, pattern: '('}]`, `[]`, "pattern:"},
		{"also without pattern", `[{id: a, code: X, description: d, match: captured-time, also: x}]`, `[]`, "also narrows a pattern"},
		{"broken also", `[{id: a, code: X, description: d, pattern: x, also: '('}]`, `[]`, "also:"},
		{"value without group", `[{id: a, code: X, description: d, pattern: x, value: {min-length: 2}}]`, `[]`, "capture group"},
		{"broken value not", `[{id: a, code: X, description: d, pattern: '(x)', value: {not: '('}}]`, `[]`, "value not:"},
		{"unknown match", `[{id: a, code: X, description: d, match: nope}]`, `[]`, `unknown match "nope"`},
		{"match params", `[{id: a, code: X, description: d, match: prose, with: {min-chars: 1}}]`, `[]`, "min-distinct-words"},
		{"allow without reason", `[{id: a, code: X, description: d, pattern: x, allow: [{path: p}]}]`, `[]`, "path and a reason"},
		{"bad glob", `[{id: a, code: X, description: d, pattern: x, paths: [/abs]}]`, `[]`, "must be relative"},
		{"bad exclude glob", `[{id: a, code: X, description: d, pattern: x, exclude: [/abs]}]`, `[]`, "must be relative"},
		{"bad path-pattern", `[{id: a, code: X, description: d, pattern: x, path-pattern: '('}]`, `[]`, "path-pattern:"},
		{"bad exclude-pattern", `[{id: a, code: X, description: d, pattern: x, exclude-pattern: '('}]`, `[]`, "exclude-pattern:"},
		{"unknown field", `[{id: a, code: X, description: d, pattern: x, extra: 1}]`, `[]`, "field extra not found"},
		{"mandatory without description", `[]`, `[{id: m, pattern: x}]`, "needs a description"},
		{"mandatory pattern and listed-in", `[]`, `[{id: m, description: d, pattern: x, listed-in: {file: f, keys: [k]}}]`, "either a pattern or listed-in"},
		{"mandatory broken pattern", `[]`, `[{id: m, description: d, pattern: '({{ versions.go }}'}]`, "pattern:"},
		{"listed-in without keys", `[]`, `[{id: m, description: d, listed-in: {file: f}}]`, "file and keys"},
		{"mandatory bad glob", `[]`, `[{id: m, description: d, pattern: x, paths: [/abs]}]`, "must be relative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadContentRules([]byte(tc.forbidden), []byte(tc.mandatory))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestFileMatch(t *testing.T) {
	m := fileMatch{Paths: []string{"a/**"}, Exclude: []string{"a/skip/**"}, PathPattern: `\.json$`, ExcludePattern: `secret`}
	if err := m.compile(); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"a/x.go": true, "b/x.json": true, "a/skip/y.go": false, "a/secret.go": false, "b/x.go": false} {
		if m.matches(name) != want {
			t.Errorf("%s: %v, want %v", name, !want, want)
		}
	}
	var all fileMatch
	if err := all.compile(); err != nil || !all.matches("anything") {
		t.Fatal("an empty selector does not match every file")
	}
}

func TestValueRule(t *testing.T) {
	v := valueRule{MinLength: 3, NotIn: []string{"me", "{{ versions.go }}"}, Random: &randomRule{Digits: 2, Distinct: 3}}
	versions := map[string]string{"go": "1.27.3"}
	for _, tc := range []struct {
		value string
		want  bool
	}{{"ab", false}, {"1.27.3", false}, {" ME ", false}, {"a1b2c", true}, {"aaaa11", false}, {"abc12", true}} {
		value, want := tc.value, tc.want
		if v.accepts(value, versions) != want {
			t.Errorf("%q: %v, want %v", value, !want, want)
		}
	}
	not := valueRule{Not: "^[0-9a-f]+$"}
	not.not = pattern(not.Not)
	if not.accepts("deadbeef", nil) || !not.accepts("XYZ", nil) {
		t.Error("value not")
	}
	if firstGroup([]string{"all", "", "second"}) != "second" || firstGroup([]string{"all", ""}) != "" {
		t.Error("firstGroup")
	}
}

func TestShippedForbiddenRules(t *testing.T) {
	long := strings.Repeat("xY9", 30)
	blob := strings.Repeat("xY9", 1400)
	for _, tc := range []struct {
		name string
		file textFile
		want string
	}{
		{"opaque signature", added("x/testdata/s.json", `{"signature":"`+long+`"}`), "FG001@x/testdata/s.json"},
		{"hex signature is fine", added("x/testdata/s.json", `{"signature":"`+strings.Repeat("ab12", 30)+`"}`), ""},
		{"redacted thinking", added("x.json", `{"type":"redacted_thinking","data":"`+long+`"}`), "FG001@x.json"},
		{"data without redacted thinking", added("x.json", `{"data":"`+long+`"}`), ""},
		{"base64 blob", added("x.txt", blob), "FG001@x.txt"},
		{"billing row", added("x.json", `{"copilot_usage": 1}`), "FG002@x.json"},
		{"random request id", added("x.json", `"x-request-id": "a1b2c3d4e5f6g7h8i9j0k"`), "FG002@x.json"},
		{"readable request id", added("x.json", `"x-request-id": "session-affinity-label"`), ""},
		{"har request id", added("x.har", `{"name":"x-request-id","value":"a1b2c3d4e5f6g7h8i9j0k"}`), "FG002@x.har"},
		{"provider id in a fixture", added("a/testdata/r.json", `"id":"chatcmpl-AbCdEfGhIjKlMnOpQr"`), "FG003@a/testdata/r.json"},
		{"provider id outside fixtures", added("a/r.go", `"chatcmpl-AbCdEfGhIjKlMnOpQr"`), ""},
		{"captured time", added("a/testdata/r.json", `"created": 1731150123`), "FG003@a/testdata/r.json"},
		{"round time", added("a/testdata/r.json", `"created": 1731150000`), ""},
		{"home path", added("a/testdata/p.json", `"/home/zerodawn/x"`), "FG004@a/testdata/p.json"},
		{"placeholder home", added("a/testdata/p.json", `"/home/runner/x"`), ""},
		{"allowed home", added("tui/internal/hljs/testdata/corpus.json", `"/home/kew/x"`), ""},
		{"vendored", added("third_party/x/testdata/p.json", `"/home/zerodawn/x"`), ""},
		{"go stray", added("Dockerfile", "FROM golang:1.26.1 AS build"), "PIN@Dockerfile"},
		{"go at the pin", added("Dockerfile", "FROM golang:1.27.3 AS build"), ""},
		{"go stray in go-version", added("ci.env", "GO_VERSION=1.25.0"), "PIN@ci.env"},
		{"workflow literal", added(".github/workflows/x.yml", "          go-version: '1.27.3'"), "PIN@.github/workflows/x.yml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(forbiddenCodes(t, tc.file), ",")
			if got != tc.want {
				t.Fatalf("findings %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMatchProseAndStreams(t *testing.T) {
	var words []string
	for i := range 60 {
		words = append(words, "word"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	sentence := strings.Join(words, " ")
	line := `{"type":"thinking","thinking":"` + sentence + ` ` + strings.Repeat("filler text ", 20) + `"}`
	if got := forbiddenCodes(t, added("a.json", line)); len(got) != 1 || got[0] != "FG005@a.json" {
		t.Fatalf("reasoning on one line: %v", got)
	}
	textOnly := `{"type":"text","text":"` + sentence + ` ` + strings.Repeat("filler text ", 20) + `"}`
	if got := forbiddenCodes(t, added("a.json", textOnly)); len(got) != 0 {
		t.Fatalf("an ordinary text field counted as reasoning: %v", got)
	}
	var stream []string
	for w := range strings.FieldsSeq(sentence + " " + strings.Repeat("filler text ", 20)) {
		stream = append(stream, `data: {"index":0,"delta":{"type":"thinking_delta","thinking":"`+w+` "}}`)
	}
	if got := forbiddenCodes(t, added("s.sse", stream...)); len(got) != 1 || got[0] != "FG005@s.sse" {
		t.Fatalf("reasoning spread over a stream: %v", got)
	}
	split := []string{`data: {"index":0,"delta":{"partial_json":"/ho"}}`, `data: {"index":0,"delta":{"partial_json":"me/zerodawn/x"}}`}
	if got := forbiddenCodes(t, added("a/testdata/s.sse", split...)); len(got) != 1 || got[0] != "FG004@a/testdata/s.sse" {
		t.Fatalf("a home path split over a stream: %v", got)
	}
	if got := unescapeFragment([]byte(`a\"b`)); string(got) != `a"b` {
		t.Fatalf("unescape %q", got)
	}
	if got := unescapeFragment([]byte(`bad\`)); string(got) != `bad\` {
		t.Fatalf("a broken escape changed: %q", got)
	}
}

func TestMatchTerms(t *testing.T) {
	terms := filepath.Join(t.TempDir(), "terms")
	writeFiles(t, filepath.Dir(terms), map[string]string{"terms": "\xef\xbb\xbfAcmeCorp\n\nsecret-host\n"})
	t.Setenv("PIG_PRIVATE_TERMS_FILE", terms)
	got := checkForbidden(shippedContent(t).Forbidden, []textFile{
		added("notes/acmecorp.md", "plain"),
		{name: "bin.dat", binary: true, lines: []numberedLine{{1, "\x00 SECRET-HOST \x00"}}},
	}, nil)
	var where []string
	for _, f := range got {
		where = append(where, f.file+":"+strings.Fields(f.message)[0])
	}
	if strings.Join(where, " ") != "notes/acmecorp.md:FG006: bin.dat:FG006:" {
		t.Fatalf("findings %v", got)
	}
	short := filepath.Join(t.TempDir(), "terms")
	writeFiles(t, filepath.Dir(short), map[string]string{"terms": "ab\n"})
	t.Setenv("PIG_PRIVATE_TERMS_FILE", short)
	if got := checkForbidden(shippedContent(t).Forbidden, []textFile{added("x", "y")}, nil); len(got) != 1 || !strings.Contains(got[0].message, "shorter than 3") {
		t.Fatalf("a short term: %v", got)
	}
	t.Setenv("PIG_PRIVATE_TERMS_FILE", filepath.Join(t.TempDir(), "missing"))
	if got := checkForbidden(shippedContent(t).Forbidden, []textFile{added("x", "y")}, nil); len(got) != 1 || !strings.Contains(got[0].message, "private terms") {
		t.Fatalf("a missing terms file: %v", got)
	}
}

func TestVersions(t *testing.T) {
	head := t.TempDir()
	if _, err := loadVersions(head); err == nil || !strings.Contains(err.Error(), "rebase on main") {
		t.Fatalf("a head without versions.yaml: %v", err)
	}
	writeFiles(t, head, map[string]string{versionsPath: "go: 1.27.3\npi:\n  version: 1.5.0+abc\n"})
	v, err := loadVersions(head)
	if err != nil || v["go"] != "1.27.3" || v["pi.version"] != "1.5.0+abc" {
		t.Fatalf("%v %v", v, err)
	}
	for in, want := range map[string]string{
		"x{{ versions.go }}y":             `x1\.27\.3y`,
		"{{versions.go|minor}}":           `1\.27`,
		"{{ versions.pi.version | url }}": `1\.5\.0%2Babc`,
	} {
		if got, err := expandVersions(in, v); err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", in, got, err, want)
		}
	}
	if _, err := expandVersions("{{ versions.rust }}", v); err == nil || !strings.Contains(err.Error(), "has no rust") {
		t.Fatalf("a missing key: %v", err)
	}
	writeFiles(t, head, map[string]string{versionsPath: "go: [unclosed\n"})
	if _, err := loadVersions(head); err == nil {
		t.Fatal("a broken versions.yaml was read")
	}
	if unquoteMeta(`1\.27\.3`) != "1.27.3" {
		t.Fatal("unquoteMeta")
	}
}

func TestMandatory(t *testing.T) {
	rules, err := loadContentRules([]byte(`[]`), []byte(`
- {id: pin, description: names the pin, paths: ['*.env'], pattern: 'GO={{ versions.go }}'}
- {id: header, description: has a header, paths: ['*.sh'], pattern: '^#!'}
- {id: listed, description: is listed, path-pattern: 'golden', listed-in: {file: list.toml, keys: ['entry[].paths[]']}}
`))
	if err != nil {
		t.Fatal(err)
	}
	head := t.TempDir()
	writeFiles(t, head, map[string]string{
		versionsPath: "go: 1.27.3\n", "a.env": "GO=1.27.3\n", "b.env": "GO=1.26.0\n", "s.sh": "echo\n",
		"list.toml": "[[entry]]\npaths = ['x/*golden*']\n", "x/one.golden": "", "y/two.golden": "",
	})
	tracked := []string{"a.env", "b.env", "s.sh", "x/one.golden", "y/two.golden"}
	targets := mandatoryTargets(rules.Mandatory, []string{"a.env", "s.sh", "x/one.golden", "y/two.golden"}, tracked)
	if strings.Join(targets["pin"], ",") != "a.env" {
		t.Fatalf("without a versions change only the changed copies: %v", targets["pin"])
	}
	var got []string
	for _, f := range checkMandatory(rules.Mandatory, targets, head) {
		got = append(got, f.file+" "+strings.SplitN(f.message, ":", 2)[0])
	}
	if strings.Join(got, ",") != "s.sh header,y/two.golden listed" {
		t.Fatalf("findings %v", got)
	}
	targets = mandatoryTargets(rules.Mandatory, []string{versionsPath}, tracked)
	if strings.Join(targets["pin"], ",") != "a.env,b.env" || len(targets["header"]) != 0 {
		t.Fatalf("a versions change runs the version rules over every file: %v", targets)
	}
	if f := checkMandatory(rules.Mandatory, targets, head); len(f) != 1 || f[0].file != "b.env" {
		t.Fatalf("findings %v", f)
	}
	if f := checkMandatory(rules.Mandatory, map[string][]string{"header": {"gone.sh"}}, head); len(f) != 1 || !strings.Contains(f[0].message, "could not be read") {
		t.Fatalf("an unreadable file: %v", f)
	}
	if err := os.Remove(filepath.Join(head, versionsPath)); err != nil {
		t.Fatal(err)
	}
	if f := checkMandatory(rules.Mandatory, map[string][]string{"pin": {"a.env"}}, head); len(f) != 1 || f[0].file != versionsPath {
		t.Fatalf("a missing versions.yaml: %v", f)
	}
	writeFiles(t, head, map[string]string{versionsPath: "rust: 1\n", "list.toml": "not toml ="})
	if f := checkMandatory(rules.Mandatory, map[string][]string{"pin": {"a.env"}, "listed": {"x/one.golden"}}, head); len(f) != 2 {
		t.Fatalf("a missing key and a broken listing: %v", f)
	}
}

func TestChangedText(t *testing.T) {
	head := t.TempDir()
	writeFiles(t, head, map[string]string{"big.json": "line one\nline two", "image.png": "\x00PNG"})
	d := &data{Files: []prFile{
		{Filename: "a.go", Status: "modified", Patch: "@@ -1 +1,2 @@\n x\n+y"},
		{Filename: "big.json", Status: "modified"},
		{Filename: "image.png", Status: "added"},
		{Filename: "gone.go", Status: "removed"},
	}}
	files, err := changedText(d, head)
	if err != nil || len(files) != 3 {
		t.Fatalf("%v %v", files, err)
	}
	if files[0].lines[0] != (numberedLine{2, "y"}) || len(files[1].lines) != 2 || files[1].binary || !files[2].binary {
		t.Fatalf("files %+v", files)
	}
	d.Files = append(d.Files, prFile{Filename: "missing.json", Status: "added"})
	if _, err := changedText(d, head); err == nil || !strings.Contains(err.Error(), "missing.json has no patch") {
		t.Fatalf("err = %v", err)
	}
	text, lineAt := added("x", "ab", "cd").joined()
	if string(text) != "ab\ncd\n" || lineAt(0) != 1 || lineAt(3) != 2 || lineAt(4) != 2 {
		t.Fatalf("joined %q", text)
	}
}

func TestContentCommand(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote := t.TempDir()
	head := gitRepo(t, remote, map[string]string{versionsPath: "go: 1.27.3\n", ".go-version": "1.26.0\n", "ok.txt": "fine"})
	dir := t.TempDir()
	ws := workspace{dir: dir, head: filepath.Join(dir, "head"), main: t.TempDir(), remote: "file://" + remote}
	d := &data{Repo: "o/r", PR: 1, Files: []prFile{{Filename: versionsPath, Status: "modified", Patch: "@@ -0,0 +1 @@\n+go: 1.27.3"}}}
	d.Pull.Head.SHA = head
	report := reportSpec{In: "stdout", Format: "lines", Pattern: `(?P<message>.+)`}
	scans := []check{scanCheck(t, scanSpec{Input: inputChanged, Run: []string{"x"}, Exit: exitCodes{Clean: []int{0}}, Report: report})}
	if err := prepareWorkspace(context.Background(), d, scans, ws, ioDiscard{}); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(dir, "data.json")
	if err := writeJSON(dataPath, d); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.json")
	var stdout, stderr strings.Builder
	code := run([]string{"content", "--data", dataPath, "--head", ws.head, "--out", out}, &stdout, &stderr)
	raw, _ := os.ReadFile(out)
	var found []map[string]any
	_ = json.Unmarshal(raw, &found)
	if code != 1 || len(found) != 1 || found[0]["file"] != ".go-version" {
		t.Fatalf("exit %d, findings %s, stderr %s", code, raw, stderr.String())
	}
	if code := run([]string{"content", "--data", dataPath}, &stdout, &stderr); code != 2 {
		t.Fatalf("missing --out: %d", code)
	}
	if code := run([]string{"content", "--data", filepath.Join(dir, "none.json"), "--out", out}, &stdout, &stderr); code != 2 {
		t.Fatalf("missing data: %d", code)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func TestContentEdges(t *testing.T) {
	head := t.TempDir()
	writeFiles(t, head, map[string]string{"whole.json": "one\ntwo\nthree", "s.sh": "#!/bin/sh\n"})
	files, err := changedText(&data{Files: []prFile{{Filename: "whole.json", Status: "modified"}}}, head)
	if err != nil || len(files[0].lines) != 3 || files[0].lines[2] != (numberedLine{3, "three"}) {
		t.Fatalf("a whole file's line numbers: %+v %v", files, err)
	}
	rules, err := loadContentRules([]byte(`[]`), []byte(`
- {id: first, description: d, paths: ['*.md'], pattern: x}
- {id: second, description: has a shebang, paths: ['*.sh'], pattern: '^#!'}
`))
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string][]string{"second": {"s.sh"}}
	if f := checkMandatory(rules.Mandatory, map[string][]string{"second": {"s.sh"}, "first": nil}, head); len(f) != 0 {
		t.Fatalf("a rule without targets stopped the next: %v", f)
	}
	targets["second"] = []string{"missing.sh"}
	if f := checkMandatory(rules.Mandatory, targets, head); len(f) != 1 {
		t.Fatalf("a rule after one without targets did not run: %v", f)
	}
	if err := checkOut(head, map[string][]string{"x": {"s.sh"}}); err != nil {
		t.Fatalf("checkOut of present files: %v", err)
	}
	if err := checkOut(head, map[string][]string{"x": {"absent.sh"}}); err == nil {
		t.Fatal("checkOut of an absent file in a non-repository did not fail")
	}
	if files, err := trackedFiles(""); err != nil || files != nil {
		t.Fatal("no head has no tracked files")
	}
	if _, err := trackedFiles(head); err == nil {
		t.Fatal("ls-files in a non-repository did not fail")
	}
}

func TestContentCommandClean(t *testing.T) {
	dir := t.TempDir()
	dataPath := filepath.Join(dir, "data.json")
	if err := writeJSON(dataPath, &data{Files: []prFile{{Filename: "a.txt", Status: "added", Patch: "@@ -0,0 +1 @@\n+fine"}}}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.json")
	var stdout, stderr strings.Builder
	if code := run([]string{"content", "--data", dataPath, "--out", out}, &stdout, &stderr); code != 0 || stdout.String() != "0 finding(s)\n" {
		t.Fatalf("exit %d, %q %q", code, stdout.String(), stderr.String())
	}
	if raw, _ := os.ReadFile(out); strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("report %s", raw)
	}
	if code := run([]string{"content", "--out", out}, &stdout, &stderr); code != 2 {
		t.Fatalf("missing --data: %d", code)
	}
	if code := run([]string{"content", "--nope"}, &stdout, &stderr); code != 2 {
		t.Fatalf("an unknown flag: %d", code)
	}
}

func TestMandatoryAfterListedIn(t *testing.T) {
	head := t.TempDir()
	writeFiles(t, head, map[string]string{"list.toml": "[[e]]\npaths = ['a.golden']\n", "a.golden": "", "s.sh": "no shebang"})
	rules, err := loadContentRules([]byte(`[]`), []byte(`
- {id: listed, description: is listed, paths: ['*.golden'], listed-in: {file: list.toml, keys: ['e[].paths[]']}}
- {id: shebang, description: has a shebang, paths: ['*.sh'], pattern: '^#!'}
`))
	if err != nil {
		t.Fatal(err)
	}
	f := checkMandatory(rules.Mandatory, map[string][]string{"listed": {"a.golden"}, "shebang": {"s.sh"}}, head)
	if len(f) != 1 || f[0].file != "s.sh" {
		t.Fatalf("the rule after a listed-in rule did not run: %v", f)
	}
	var stdout, stderr strings.Builder
	if code := run([]string{"content", "--data", "d.json"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "--data and --out are required") {
		t.Fatalf("missing --out: %d %q", code, stderr.String())
	}
}

func TestTrackedFilesKeepsUnusualNames(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	head := t.TempDir()
	writeFiles(t, head, map[string]string{"a.txt": "", "with space é.txt": "", "untracked": ""})
	for _, args := range [][]string{{"init", "-q"}, {"add", "a.txt", "with space é.txt"}} {
		if out, err := git(context.Background(), append([]string{"-C", head}, args...)...); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if got, err := trackedFiles(head); err != nil || !slices.Equal(got, []string{"a.txt", "with space é.txt"}) {
		t.Fatalf("tracked %q, err %v", got, err)
	}
}
