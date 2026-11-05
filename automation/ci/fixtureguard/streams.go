// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"regexp"
	"sort"
)

// A streamed value arrives as many small string fragments, so a path or a term can straddle fragments and escape every line-level match. reassemble joins the string fields of a stream so the same detectors can run over the text a client would see.
//
// Fragments are grouped by field name and by the stream they belong to: the nearest preceding index, output_index, item_id or call_id on the same line (a tool call's index, a content block's index). Every field is also joined across the whole file, which covers providers that interleave streams or omit the index.

const streamFields = `arguments|partial_json|text|content|reasoning_text|reasoning_content|reasoning|thinking|summary_text|thought|delta`

var (
	streamValue    = regexp.MustCompile(`(?:^|[^\\])"(` + streamFields + `)"\s*:\s*"((?:[^"\\]|\\.)*)"`)
	streamValueEsc = regexp.MustCompile(`\\"(` + streamFields + `)\\"\s*:\s*\\"((?:[^"\\]|\\[^"])*)\\"`)
	streamID       = regexp.MustCompile(`\\*"(?:output_|content_)?index\\*"\s*:\s*([0-9]+)|\\*"(?:item_id|call_id)\\*"\s*:\s*\\*"([^"\\]*)`)

	preStream = [][]byte{[]byte(`"delta"`), []byte(`\"delta\"`), []byte("partial_json"), []byte("\ndata:")}
)

// reasoningFields are the stream fields that hold model reasoning.
var reasoningFields = map[string]bool{"reasoning_text": true, "reasoning_content": true, "reasoning": true, "thinking": true, "summary_text": true, "thought": true}

type stream struct {
	field string
	whole bool // joined across the whole file rather than one index or id
	buf   []byte
	offs  []int // start offset of each fragment in buf
	lines []int // source line of each fragment
}

func (s *stream) add(line int, fragment []byte) {
	s.offs = append(s.offs, len(s.buf))
	s.lines = append(s.lines, line)
	s.buf = append(s.buf, fragment...)
}

// lineAt returns the source line of the fragment that holds buf[off].
func (s *stream) lineAt(off int) int {
	i := max(sort.Search(len(s.offs), func(i int) bool { return s.offs[i] > off })-1, 0)
	return s.lines[i]
}

// streamy reports files that carry server-sent events or delta chunks, where reassembly applies.
func streamy(path string, data []byte) bool {
	if bytes.HasSuffix([]byte(path), []byte(".sse")) || bytes.HasPrefix(data, []byte("data:")) {
		return true
	}
	return containsAny(data, preStream)
}

func unescapeFragment(raw []byte) []byte {
	if bytes.IndexByte(raw, '\\') < 0 {
		return raw
	}
	quoted := make([]byte, 0, len(raw)+2)
	quoted = append(quoted, '"')
	quoted = append(quoted, raw...)
	quoted = append(quoted, '"')
	var out string
	if err := json.Unmarshal(quoted, &out); err != nil {
		return raw
	}
	return []byte(out)
}

// reassemble returns the joined streams of a file in deterministic order, or nil when the file is not a stream.
func reassemble(path string, data []byte) []*stream {
	if !streamy(path, data) {
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
	number := 0
	for rest := data; len(rest) > 0; {
		number++
		var line []byte
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			line, rest = rest, nil
		}
		if len(line) < 12 {
			continue
		}
		var ids [][]int
		var matches [][]int
		for _, re := range []*regexp.Regexp{streamValue, streamValueEsc} {
			matches = append(matches, re.FindAllSubmatchIndex(line, -1)...)
		}
		if len(matches) == 0 {
			continue
		}
		sort.Slice(matches, func(a, b int) bool { return matches[a][2] < matches[b][2] })
		ids = streamID.FindAllSubmatchIndex(line, -1)
		for _, m := range matches {
			field := string(line[m[2]:m[3]])
			fragment := unescapeFragment(line[m[4]:m[5]])
			id := ""
			for _, c := range ids {
				if c[0] >= m[2] {
					break
				}
				if c[2] >= 0 {
					id = "i" + string(line[c[2]:c[3]])
				} else {
					id = "d" + string(line[c[4]:c[5]])
				}
			}
			get(field+"|*", field, true).add(number, fragment)
			if id != "" {
				get(field+"|"+id, field, false).add(number, fragment)
			}
		}
	}
	return order
}
