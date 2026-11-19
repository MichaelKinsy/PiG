// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type matcher struct {
	validate func(params) error
	run      func(name string, text []byte, lineAt func(int) int, p params) []finding
	binary   bool
}

var matchers map[string]matcher

func init() {
	matchers = map[string]matcher{
		"base64-blob":   {validate: needInts("min"), run: matchBase64Blob},
		"captured-time": {validate: noParams, run: matchCapturedTime},
		"home-path":     {validate: func(p params) error { return needList(p, "placeholders") }, run: matchHomePath},
		"prose":         {validate: needInts("min-chars", "min-distinct-words"), run: matchProse},
		"terms":         {validate: func(p params) error { return p.need("env") }, run: matchTerms, binary: true},
	}
}

func (p params) integer(key string) int {
	switch v := p[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}

func needInts(keys ...string) func(params) error {
	return func(p params) error {
		for _, k := range keys {
			if p.integer(k) <= 0 {
				return fmt.Errorf("needs a positive %s", k)
			}
		}
		return nil
	}
}

func eachLine(text []byte, fn func(off int, line []byte)) {
	off := 0
	for line := range bytes.Lines(text) {
		fn(off, bytes.TrimSuffix(line, []byte("\n")))
		off += len(line)
	}
}

var (
	base64RunRE = regexp.MustCompile(`[A-Za-z0-9+/_-]+`)
	hexOnlyRE   = regexp.MustCompile(`^[0-9a-fA-F]+$`)
	lowerRE     = regexp.MustCompile(`[a-z]`)
	upperRE     = regexp.MustCompile(`[A-Z]`)
	digitRE     = regexp.MustCompile(`[0-9]`)
	alnumRE     = regexp.MustCompile(`[a-zA-Z0-9]`)
)

func matchBase64Blob(name string, text []byte, lineAt func(int) int, p params) []finding {
	var found []finding
	for _, at := range base64RunRE.FindAllIndex(text, -1) {
		run := text[at[0]:at[1]]
		if len(run) >= p.integer("min") && !hexOnlyRE.Match(run) && mixedCase(run) {
			found = append(found, finding{file: name, line: lineAt(at[0]), message: fmt.Sprintf("%d characters", len(run))})
		}
	}
	return found
}

func mixedCase(b []byte) bool {
	return lowerRE.Match(b) && upperRE.Match(b) && digitRE.Match(b)
}

func randomLooking(value string, digits, distinct int) bool {
	seen := map[string]bool{}
	for _, c := range alnumRE.FindAllString(value, -1) {
		seen[strings.ToLower(c)] = true
	}
	return len(digitRE.FindAllString(value, -1)) >= digits && len(seen) >= distinct
}

var capturedTimeRE = regexp.MustCompile(`"(?:created|created_at)"\s*:\s*(?:(1[6-9][0-9]{8})\b|"20[2-3][0-9]-[0-9]{2}-[0-9]{2}T[0-9:]{8}\.([0-9]{3})[0-9]*)`)

func matchCapturedTime(name string, text []byte, lineAt func(int) int, _ params) []finding {
	var found []finding
	at := capturedTimeRE.FindAllIndex(text, -1)
	for i, m := range capturedTimeRE.FindAllSubmatch(text, -1) {
		epoch, millis := m[1], m[2]
		seconds, err := strconv.ParseInt(string(epoch), 10, 64)
		if (err == nil && seconds%1000 != 0) || (len(millis) > 0 && string(millis) != "000") {
			found = append(found, finding{file: name, line: lineAt(at[i][0])})
		}
	}
	return found
}

var (
	posixHomeRE   = regexp.MustCompile(`(?:^|[^A-Za-z0-9_])/(?:Users|home)/([A-Za-z0-9._-]+)`)
	windowsHomeRE = regexp.MustCompile(`[A-Za-z]:\\+Users\\+([A-Za-z0-9._ -]+)`)
)

func matchHomePath(name string, text []byte, lineAt func(int) int, p params) []finding {
	placeholders := p.strs("placeholders")
	homes := func(b []byte, at func(int) int) []finding {
		var found []finding
		for _, re := range []*regexp.Regexp{posixHomeRE, windowsHomeRE} {
			for _, m := range re.FindAllSubmatchIndex(b, -1) {
				account := strings.TrimSpace(string(b[m[2]:m[3]]))
				if !slices.ContainsFunc(placeholders, func(s string) bool { return strings.EqualFold(s, account) }) {
					found = append(found, finding{file: name, line: at(m[2])})
				}
			}
		}
		return found
	}
	found := homes(text, lineAt)
	for _, s := range reassemble(text, lineAt) {
		found = append(found, homes(s.buf, s.lineAt)...)
	}
	return dedupeFindings(found)
}

var (
	reasoningLineRE   = regexp.MustCompile(`\\*"(?:reasoning_text|reasoning_content|reasoning|thinking|summary_text|reasoning_summary|thought|text|delta)\\*"\s*:\s*\\*"((?:[^"\\]|\\[^"])*)`)
	goReasoningLineRE = regexp.MustCompile("\\b(?:Thinking|Reasoning|ReasoningText|ReasoningContent|SummaryText)\\s*:\\s*(?:\"((?:[^\"\\\\]|\\\\.)*)\"|`([^`]*)`)")
	wordRE            = regexp.MustCompile(`[A-Za-z]{2,}`)
	reasoningFields   = []string{"reasoning_text", "reasoning_content", "reasoning", "thinking", "summary_text", "thought"}
	// Only lines that also name reasoning count, or every text field of a fixture would.
	reasoningWordsRE = regexp.MustCompile(`reasoning|thinking|thought|summary_text|Thinking|Reasoning|SummaryText`)
)

type tally struct {
	chars, first int
	words        map[string]bool
}

func (t *tally) add(line int, value []byte) {
	words := wordRE.FindAll(value, -1)
	if len(words) == 0 {
		return
	}
	if t.words == nil {
		t.first, t.words = line, map[string]bool{}
	}
	for _, w := range words {
		t.words[strings.ToLower(string(w))] = true
	}
	t.chars += len(value)
}

func matchProse(name string, text []byte, lineAt func(int) int, p params) []finding {
	var lines, streams tally
	eachLine(text, func(off int, line []byte) {
		if !reasoningWordsRE.Match(line) {
			return
		}
		for _, m := range reasoningLineRE.FindAllSubmatch(line, -1) {
			lines.add(lineAt(off), m[1])
		}
		for _, m := range goReasoningLineRE.FindAllSubmatch(line, -1) {
			lines.add(lineAt(off), append(m[1], m[2]...))
		}
	})
	for _, s := range reassemble(text, lineAt) {
		if s.whole && slices.Contains(reasoningFields, s.field) {
			streams.add(s.lines[0], s.buf)
		}
	}
	var found []finding
	for _, t := range []tally{lines, streams} {
		if t.chars >= p.integer("min-chars") && len(t.words) >= p.integer("min-distinct-words") {
			found = append(found, finding{file: name, line: t.first, message: fmt.Sprintf("%d characters, %d distinct words", t.chars, len(t.words))})
		}
	}
	return found
}

func matchTerms(name string, text []byte, lineAt func(int) int, p params) []finding {
	terms, err := cachedTerms(os.Getenv(p.str("env")))
	if err != nil {
		return []finding{{file: name, message: err.Error()}}
	}
	var found []finding
	search := func(b []byte, at func(int) int) {
		for _, t := range terms {
			for _, m := range t.re.FindAllIndex(b, -1) {
				found = append(found, finding{file: name, line: at(m[0]), message: fmt.Sprintf("term at line %d of the terms file", t.line)})
			}
		}
	}
	search([]byte(name), func(int) int { return 0 })
	search(text, lineAt)
	for _, s := range reassemble(text, lineAt) {
		search(s.buf, s.lineAt)
	}
	return dedupeFindings(found)
}

type term struct {
	line int
	re   *regexp.Regexp
}

type loadedTerms struct {
	terms []term
	err   error
}

var termsByFile sync.Map

func cachedTerms(file string) ([]term, error) {
	if v, ok := termsByFile.Load(file); ok {
		l := v.(loadedTerms)
		return l.terms, l.err
	}
	terms, err := loadTerms(file)
	termsByFile.Store(file, loadedTerms{terms, err})
	return terms, err
}

func loadTerms(file string) ([]term, error) {
	if file == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("private terms: %w", err)
	}
	var terms []term
	for i, line := range strings.Split(string(bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len([]rune(line)) < 3 {
			return nil, errors.New("private terms: line " + strconv.Itoa(i+1) + " is shorter than 3 characters")
		}
		terms = append(terms, term{line: i + 1, re: regexp.MustCompile("(?i)" + regexp.QuoteMeta(line))})
	}
	return terms, nil
}

const streamFields = `arguments|partial_json|text|content|reasoning_text|reasoning_content|reasoning|thinking|summary_text|thought|delta`

var (
	streamTokenRE = regexp.MustCompile(`\\*"(?:output_|content_)?index\\*"\s*:\s*([0-9]+)|\\*"(?:item_id|call_id)\\*"\s*:\s*\\*"([^"\\]*)|(?:^|[^\\])"(` + streamFields + `)"\s*:\s*"((?:[^"\\]|\\.)*)"|\\"(` + streamFields + `)\\"\s*:\s*\\"((?:[^"\\]|\\[^"])*)\\"`)
	streamMarkers = [][]byte{[]byte(`"delta"`), []byte(`\"delta\"`), []byte("partial_json"), []byte("\ndata:")}
)

type stream struct {
	field string
	whole bool
	buf   []byte
	offs  []int
	lines []int
}

func (s *stream) add(line int, fragment []byte) {
	s.offs = append(s.offs, len(s.buf))
	s.lines = append(s.lines, line)
	s.buf = append(s.buf, fragment...)
}

func (s *stream) lineAt(off int) int {
	i := max(sort.Search(len(s.offs), func(i int) bool { return s.offs[i] > off })-1, 0)
	return s.lines[i]
}

func unescapeFragment(raw []byte) []byte {
	if bytes.IndexByte(raw, '\\') < 0 {
		return raw
	}
	var out string
	if err := json.Unmarshal(append(append([]byte{'"'}, raw...), '"'), &out); err != nil {
		return raw
	}
	return []byte(out)
}

func reassemble(text []byte, lineAt func(int) int) []*stream {
	if !bytes.HasPrefix(text, []byte("data:")) && !slices.ContainsFunc(streamMarkers, func(m []byte) bool { return bytes.Contains(text, m) }) {
		return nil
	}
	byKey := map[string]*stream{}
	var order []*stream
	get := func(key, field string, whole bool) *stream {
		s := byKey[key]
		if s == nil {
			s = &stream{field: field, whole: whole}
			byKey[key] = s
			order = append(order, s)
		}
		return s
	}
	eachLine(text, func(off int, line []byte) {
		id := ""
		for _, m := range streamTokenRE.FindAllSubmatch(line, -1) {
			switch {
			case len(m[1]) > 0:
				id = "i" + string(m[1])
			case len(m[3]) > 0 || len(m[5]) > 0:
				field := string(m[3]) + string(m[5])
				fragment := unescapeFragment(slices.Concat(m[4], m[6]))
				get(field+"|*", field, true).add(lineAt(off), fragment)
				if id != "" {
					get(field+"|"+id, field, false).add(lineAt(off), fragment)
				}
			default:
				id = "d" + string(m[2])
			}
		}
	})
	return order
}
