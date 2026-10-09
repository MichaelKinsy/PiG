package tools

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/tools/truncate.ts

// truncateHead, truncateTail, truncateLine, truncateMiddle and formatSize against pinned Pi over seeded content (ASCII, multi-byte, astral
// characters, CRLF, missing trailing newline, one huge line) and limits around every boundary.
func TestTruncateMatchesPi(t *testing.T) {
	type call struct {
		Fn       string `json:"fn"`
		Content  string `json:"content"`
		MaxLines *int   `json:"maxLines"`
		MaxBytes *int   `json:"maxBytes"`
		N        int    `json:"n"`
	}
	state := uint64(0x9e3779b97f4a7c15)
	next := func(n int) int {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return int(state % uint64(n))
	}
	pieces := []string{"a", "bc", "def", " ", "é", "日本", "🙂", "\t", "x_y", "0123456789"}
	content := func() string {
		var b strings.Builder
		for range next(40) {
			for range next(12) {
				b.WriteString(pieces[next(len(pieces))])
			}
			switch next(6) {
			case 0:
				b.WriteString("\r\n")
			case 1:
				// no newline: lines run together
			default:
				b.WriteString("\n")
			}
		}
		return b.String()
	}
	fixed := []string{"", "\n", "one", "one\n", "a\nb\nc", "a\r\nb\r\n", strings.Repeat("x", 3000), strings.Repeat("日", 700) + "\nshort", strings.Repeat("🙂", 600),
		strings.Repeat("line\n", 2500), "no trailing newline\nsecond"}
	contents := append([]string{}, fixed...)
	for range 60 {
		contents = append(contents, content())
	}
	ints := func(v ...int) []*int {
		out := []*int{nil}
		for _, x := range v {
			out = append(out, &x)
		}
		return out
	}
	var calls []call
	for _, c := range contents {
		for _, lines := range ints(0, 1, 2, 3, 10) {
			for _, bytes := range ints(0, 1, 5, 17, 64, 200, 1000) {
				calls = append(calls, call{"head", c, lines, bytes, 0}, call{"tail", c, lines, bytes, 0})
			}
		}
		for _, n := range []int{0, 1, 2, 7, 33, 100, 500, 2000} {
			// TruncateLine's Go signature has no optional argument, so maxChars <= 0 stands for upstream's omitted default; only explicit
			// limits are comparable.
			if n > 0 {
				calls = append(calls, call{"line", c, nil, nil, n})
			}
			calls = append(calls, call{"middle", c, nil, nil, n})
		}
	}
	for _, n := range []int{0, 1, 1023, 1024, 1025, 1536, 51200, 1048575, 1048576, 1048577, 5 * 1048576, 1 << 40} {
		calls = append(calls, call{"size", "", nil, nil, n})
	}
	input, err := json.Marshal(calls)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/truncate.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	failures := 0
	fail := func(format string, args ...any) {
		if failures++; failures <= 6 {
			t.Errorf(format, args...)
		}
	}
	for i, c := range calls {
		opts := TruncationOptions{MaxLines: c.MaxLines, MaxBytes: c.MaxBytes}
		var got, want any
		switch c.Fn {
		case "head":
			r := TruncateHead(c.Content, opts)
			got, want = r, new(TruncationResult)
		case "tail":
			r := TruncateTail(c.Content, opts)
			got, want = r, new(TruncationResult)
		case "line":
			r := TruncateLine(c.Content, c.N)
			got, want = struct {
				Text         string `json:"text"`
				WasTruncated bool   `json:"wasTruncated"`
			}{r.Text, r.WasTruncated}, new(struct {
				Text         string `json:"text"`
				WasTruncated bool   `json:"wasTruncated"`
			})
		case "middle":
			r := TruncateMiddle(c.Content, c.N)
			got, want = struct {
				Content      string `json:"content"`
				Truncated    bool   `json:"truncated"`
				RemovedChars int    `json:"removedChars"`
				TotalBytes   int    `json:"totalBytes"`
				TotalLines   int    `json:"totalLines"`
			}{r.Content, r.Truncated, r.RemovedChars, r.TotalBytes, r.TotalLines}, new(struct {
				Content      string `json:"content"`
				Truncated    bool   `json:"truncated"`
				RemovedChars int    `json:"removedChars"`
				TotalBytes   int    `json:"totalBytes"`
				TotalLines   int    `json:"totalLines"`
			})
		case "size":
			var s string
			if err := json.Unmarshal(expected[i], &s); err != nil {
				t.Fatal(err)
			}
			if g := FormatSize(c.N); g != s {
				fail("FormatSize(%d) = %q, Pi %q", c.N, g, s)
			}
			continue
		}
		if err := json.Unmarshal(expected[i], want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, reflect.ValueOf(want).Elem().Interface()) {
			fail("%s(%q, lines=%v bytes=%v n=%d):\n  PiG %+v\n  Pi  %+v", c.Fn, c.Content, optInt(c.MaxLines), optInt(c.MaxBytes), c.N, got, want)
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d calls differ from Pi", failures, len(calls))
	}
}

func optInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
