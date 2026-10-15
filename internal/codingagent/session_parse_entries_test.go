// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"slices"
	"testing"
)

// upstream: packages/coding-agent/src/core/session-manager.ts:355-370 (parseSessionEntries). The expected records were measured by running the function body on Node (content.trim().split("\n"), skip a line whose trim() is empty, JSON.parse inside try/catch) for each input; a record is kept in order, a falsy JSON value is kept, and a malformed line is skipped. Go returns each record's JSON text, so the check compares text where Pi compares parsed values.
func TestParseSessionEntriesMatchesPi(t *testing.T) {
	for _, row := range []struct {
		name    string
		content string
		want    []string
	}{
		{"records in order", "{\"type\":\"session\",\"id\":\"a\"}\n{\"type\":\"message\",\"id\":\"b\"}\n", []string{`{"type":"session","id":"a"}`, `{"type":"message","id":"b"}`}},
		{"falsy values are records", "null\nfalse\n0\n\"\"\n{\"x\":1}\n", []string{`null`, `false`, `0`, `""`, `{"x":1}`}},
		{"malformed lines are skipped", "{\"a\":1}\nnot json\n{\"b\":\n{\"c\":3}", []string{`{"a":1}`, `{"c":3}`}},
		{"blank lines are skipped", "\n\n  \n\t\n{\"a\":1}\n   \n{\"b\":2}\n\n", []string{`{"a":1}`, `{"b":2}`}},
		{"CRLF line ends", "{\"a\":1}\r\n{\"b\":2}\r\n", []string{`{"a":1}`, `{"b":2}`}},
		{"BOM is trimmed from the content", "\uFEFF{\"a\":1}\n{\"b\":2}", []string{`{"a":1}`, `{"b":2}`}},
		{"NBSP before a record is not JSON whitespace", "{\"a\":1}\n\u00a0{\"b\":2}\n{\"c\":3}", []string{`{"a":1}`, `{"c":3}`}},
		{"NBSP-only line is blank", "{\"a\":1}\n\u00a0\n{\"c\":3}", []string{`{"a":1}`, `{"c":3}`}},
		{"empty content", "", nil},
		{"whitespace-only content", " \n\u2003\n", nil},
		{"non-object JSON", "[1,2]\n\"s\"\n7\n", []string{`[1,2]`, `"s"`, `7`}},
	} {
		t.Run(row.name, func(t *testing.T) {
			got := ParseSessionEntries(row.content)
			if got == nil {
				t.Fatal("ParseSessionEntries returned nil, want a (possibly empty) slice")
			}
			texts := make([]string, len(got))
			for i, record := range got {
				texts[i] = string(record.Raw())
			}
			if !slices.Equal(texts, row.want) && (len(texts) != 0 || len(row.want) != 0) {
				t.Fatalf("records = %q, want %q", texts, row.want)
			}
		})
	}
}
