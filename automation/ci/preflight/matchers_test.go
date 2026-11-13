// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func lineOf(text []byte) func(int) int {
	return func(off int) int { return strings.Count(string(text[:off]), "\n") + 1 }
}

func TestCharacterClasses(t *testing.T) {
	if !mixedCase([]byte("aZ0")) || mixedCase([]byte("aZ")) || mixedCase([]byte("a0")) || mixedCase([]byte("Z0")) {
		t.Error("mixedCase")
	}
	for value, want := range map[string]bool{"a1b2c3d4": true, "a1b2c3d": false, "aaaa1234": false, "abcd123": false, "Ab1-Cd2_Ef3/Gh4": true} {
		if randomLooking(value, 4, 7) != want {
			t.Errorf("randomLooking(%q) = %v", value, !want)
		}
	}
	var lines []string
	eachLine([]byte("a\nbc\n\nd"), func(off int, line []byte) { lines = append(lines, string(line)+"@"+string(rune('0'+off))) })
	if strings.Join(lines, " ") != "a@0 bc@2 @5 d@6" {
		t.Fatalf("eachLine %v", lines)
	}
}

func TestMatchBase64BlobBounds(t *testing.T) {
	p := params{"min": 6}
	text := []byte("short xY1zW2 xx\nhexonly abcdef0123\nlower abcdefgh\nthe end xY9zW8")
	var got []int
	for _, f := range matchBase64Blob("f", text, lineOf(text), p) {
		got = append(got, f.line)
		if f.message != "6 characters" {
			t.Errorf("message %q", f.message)
		}
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 4 {
		t.Fatalf("blobs on lines %v, want 1 and 4", got)
	}
	if len(matchBase64Blob("f", []byte("aB1cD"), lineOf([]byte("aB1cD")), p)) != 0 {
		t.Fatal("a run shorter than min matched")
	}
}

func TestMatchCapturedTimeForms(t *testing.T) {
	text := []byte(`"created": 1731150123` + "\n" + `"created_at": "2026-11-09T10:00:00.123Z"` + "\n" + `"created_at": "2026-11-09T10:00:00.000Z"` + "\n" + `"created": 1731150000`)
	var lines []int
	for _, f := range matchCapturedTime("f", text, lineOf(text), nil) {
		lines = append(lines, f.line)
	}
	if len(lines) != 2 || lines[0] != 1 || lines[1] != 2 {
		t.Fatalf("captured times on lines %v, want 1 and 2", lines)
	}
}

func TestMatchHomePathForms(t *testing.T) {
	p := params{"placeholders": []any{"runner"}}
	text := []byte("x /home/alice y\n" + `C:\Users\Bob\x` + "\n/home/runner/z\n/Users/carol and /home/dave")
	var lines []int
	for _, f := range matchHomePath("f", text, lineOf(text), p) {
		lines = append(lines, f.line)
	}
	slices.Sort(lines)
	if !slices.Equal(lines, []int{1, 2, 4}) {
		t.Fatalf("home paths on lines %v", lines)
	}
}

func toStrings(n []int) string {
	var out []string
	for _, v := range n {
		out = append(out, string(rune('0'+v)))
	}
	return strings.Join(out, " ")
}

func TestMatchProseThresholds(t *testing.T) {
	words := func(n int) string {
		var w []string
		for i := range n {
			w = append(w, "w"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		}
		return strings.Join(w, " ")
	}
	value := words(10)
	line := `{"thinking":"` + value + `"}`
	exact := params{"min-chars": len(value), "min-distinct-words": 10}
	if got := matchProse("f", []byte(line), lineOf([]byte(line)), exact); len(got) != 1 || got[0].message != "39 characters, 10 distinct words" {
		t.Fatalf("at both thresholds: %+v", got)
	}
	for name, p := range map[string]params{
		"one character short": {"min-chars": len(value) + 1, "min-distinct-words": 10},
		"one word short":      {"min-chars": len(value), "min-distinct-words": 11},
	} {
		if got := matchProse("f", []byte(line), lineOf([]byte(line)), p); len(got) != 0 {
			t.Errorf("%s: %+v", name, got)
		}
	}
	text := []byte("Thinking: \"" + words(5) + "\",\nReasoning: `" + words(5)[3:] + " zz`,\n" + `{"reasoning":"more words here"}`)
	if got := matchProse("f", text, lineOf(text), params{"min-chars": 30, "min-distinct-words": 9}); len(got) != 1 || got[0].line != 1 {
		t.Fatalf("over several lines: %+v", got)
	}
}

func TestMatchTermsAndLoadTerms(t *testing.T) {
	file := filepath.Join(t.TempDir(), "terms")
	if err := os.WriteFile(file, []byte("\nabc\n\nAcme Corp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRIVATE", file)
	text := []byte("one abc and ABC\nacme corp")
	var got []string
	for _, f := range matchTerms("dir/x", text, lineOf(text), params{"env": "PRIVATE"}) {
		got = append(got, toStrings([]int{f.line})+":"+f.message)
	}
	if strings.Join(got, ",") != "1:term at line 2 of the terms file,2:term at line 4 of the terms file" {
		t.Fatalf("terms %v", got)
	}
	if err := os.WriteFile(file, []byte("abc\nab\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTerms(file); err == nil || !strings.Contains(err.Error(), "line 2 is shorter") {
		t.Fatalf("err = %v", err)
	}
	if terms, err := loadTerms(""); err != nil || terms != nil {
		t.Fatal("no terms file is no terms")
	}
}

func TestReassemble(t *testing.T) {
	text := []byte(`{"item_id":"a","delta":"he"}` + "\n" + `{"item_id":"b","delta":"XX"}` + "\n" + `{"item_id":"a","delta":"llo"}`)
	var whole, perItem []string
	for _, s := range reassemble(text, lineOf(text)) {
		if s.whole {
			whole = append(whole, string(s.buf))
		} else {
			perItem = append(perItem, string(s.buf))
		}
	}
	if strings.Join(whole, "|") != "heXXllo" || strings.Join(perItem, "|") != "hello|XX" {
		t.Fatalf("whole %v, per item %v", whole, perItem)
	}
	indexed := []byte(`data: {"delta":"x","index":1}` + "\n" + `data: {"index":2,"delta":"y"}`)
	var keys []string
	for _, s := range reassemble(indexed, lineOf(indexed)) {
		keys = append(keys, s.field+"/"+string(s.buf)+"/"+map[bool]string{true: "whole", false: "id"}[s.whole])
	}
	if strings.Join(keys, " ") != "delta/xy/whole delta/y/id" {
		t.Fatalf("streams %v", keys)
	}
	if reassemble([]byte(`{"text":"no stream"}`), nil) != nil {
		t.Fatal("a text without a stream marker was reassembled")
	}
	s := &stream{}
	s.add(3, []byte("ab"))
	s.add(7, []byte("cd"))
	if s.lineAt(0) != 3 || s.lineAt(1) != 3 || s.lineAt(2) != 7 || s.lineAt(3) != 7 {
		t.Fatal("stream lineAt")
	}
	if string(unescapeFragment([]byte("plain"))) != "plain" || string(unescapeFragment([]byte(`\u0041`))) != "A" {
		t.Fatal("unescapeFragment")
	}
}

func TestCheckForbiddenOrderAndValues(t *testing.T) {
	rules, err := loadContentRules([]byte(`
- {id: elsewhere, code: E, description: only in other files, paths: ['other/**'], pattern: 'x'}
- {id: value, code: V, description: a long value, pattern: 'k=(\w+)', also: 'k=', value: {min-length: 4}}
- {id: words, code: W, description: a word, match: base64-blob, with: {min: 4}}
- {id: term, code: T, description: a term, match: terms, with: {env: NO_SUCH_TERMS_VARIABLE}}
`), []byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	files := []textFile{added("a.txt", "plain line", "k=ab k=abcd"), {name: "b.bin", binary: true, lines: []numberedLine{{1, "k=abcdef"}}}}
	got := checkForbidden(rules.Forbidden, files, nil)
	var msgs []string
	for _, f := range got {
		msgs = append(msgs, f.String())
	}
	if strings.Join(msgs, " | ") != "a.txt:2 V: a long value" {
		t.Fatalf("findings %v", msgs)
	}
	with := checkForbidden(rules.Forbidden, []textFile{added("c.txt", "xY1zW2vU")}, nil)
	if len(with) != 1 || with[0].message != "W: a word (8 characters)" {
		t.Fatalf("a matcher's detail: %+v", with)
	}
}

func TestLastMatcherEdges(t *testing.T) {
	if !randomLooking("abc1234", 4, 7) || randomLooking("abc1234", 4, 8) {
		t.Error("randomLooking at its distinct bound")
	}
	iso := []byte(`"created_at": "2026-11-09T10:00:00.123Z"`)
	if len(matchCapturedTime("f", iso, lineOf(iso), nil)) != 1 {
		t.Error("milliseconds of an ISO time were not read")
	}
	two := []byte(`{"thinking":"alpha beta gamma","reasoning":"delta epsilon zeta"}`)
	if got := matchProse("f", two, lineOf(two), params{"min-chars": 30, "min-distinct-words": 6}); len(got) != 1 {
		t.Errorf("the second reasoning field of a line was not counted: %+v", got)
	}
	goTwo := []byte("Thinking: \"alpha beta gamma\", Reasoning: \"delta epsilon zeta\"")
	if got := matchProse("f", goTwo, lineOf(goTwo), params{"min-chars": 30, "min-distinct-words": 6}); len(got) != 1 {
		t.Errorf("the second Go reasoning literal of a line was not counted: %+v", got)
	}
	file := filepath.Join(t.TempDir(), "terms")
	if err := os.WriteFile(file, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRIVATE", file)
	start := []byte("abc at the start")
	if len(matchTerms("f", start, lineOf(start), params{"env": "PRIVATE"})) != 1 {
		t.Error("a term at the very start was missed")
	}
	twice := []byte("abc\nnone\nABC")
	if got := matchTerms("f", twice, lineOf(twice), params{"env": "PRIVATE"}); len(got) != 2 || got[1].line != 3 {
		t.Errorf("a term on two lines: %+v", got)
	}
	line := []byte(`{"index":1,"delta":"a"},{"index":2,"delta":"b"},{"index":1,"delta":"c"}`)
	var streams []string
	for _, s := range reassemble(line, lineOf(line)) {
		streams = append(streams, string(s.buf))
	}
	if strings.Join(streams, " ") != "abc ac b" {
		t.Fatalf("streams %v", streams)
	}
	escaped := []byte(`data: "{\"index\":3,\"delta\":\"x\"}"`)
	var keys []string
	for _, s := range reassemble(escaped, lineOf(escaped)) {
		keys = append(keys, string(s.buf))
	}
	if strings.Join(keys, " ") != "x x" {
		t.Fatalf("escaped stream %v", keys)
	}
}

func TestBinaryFilesGetOnlyByteMatchers(t *testing.T) {
	file := filepath.Join(t.TempDir(), "terms")
	if err := os.WriteFile(file, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_PRIVATE_TERMS_FILE", file)
	rules, err := loadContentRules([]byte(`
- {id: blob, code: B, description: a blob, match: base64-blob, with: {min: 4}}
- {id: term, code: T, description: a term, match: terms, with: {env: PIG_PRIVATE_TERMS_FILE}}
`), []byte(`[]`))
	if err != nil {
		t.Fatal(err)
	}
	got := checkForbidden(rules.Forbidden, []textFile{{name: "b.bin", binary: true, lines: []numberedLine{{1, "xY1zW2vU secret"}}}}, nil)
	if len(got) != 1 || !strings.HasPrefix(got[0].message, "T:") {
		t.Fatalf("a binary file: %+v", got)
	}
}

func TestTermsAreReadOncePerFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "terms")
	if err := os.WriteFile(file, []byte("abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := cachedTerms(file)
	if err != nil || len(first) != 1 {
		t.Fatalf("terms %v, err %v", first, err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if again, err := cachedTerms(file); err != nil || len(again) != 1 || again[0].re != first[0].re {
		t.Fatalf("read again: %v, %v", again, err)
	}
	missing := filepath.Join(t.TempDir(), "none")
	if _, err := cachedTerms(missing); err == nil {
		t.Fatal("a missing terms file was not reported")
	}
	if _, err := cachedTerms(missing); err == nil {
		t.Fatal("a missing terms file was not reported the second time")
	}
}
