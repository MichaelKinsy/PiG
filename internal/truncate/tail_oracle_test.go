package truncate

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type tailCase struct {
	Content  string `json:"content"`
	MaxBytes int    `json:"maxBytes"`
	MaxLines int    `json:"maxLines"`
}

type tailWant struct {
	Content         string  `json:"content"`
	Truncated       bool    `json:"truncated"`
	TruncatedBy     *string `json:"truncatedBy"`
	TotalLines      int     `json:"totalLines"`
	TotalBytes      int     `json:"totalBytes"`
	OutputLines     int     `json:"outputLines"`
	OutputBytes     int     `json:"outputBytes"`
	LastLinePartial bool    `json:"lastLinePartial"`
}

// TruncateTail is truncateTail (truncate.ts): the last maxLines lines and maxBytes UTF-8 bytes, the end of a single line that alone exceeds maxBytes cut on a character boundary. Every field of the result against Pi over contents of ASCII, multi-byte, CRLF, blank and newline-only lines and a limit grid that includes zero and one.
func TestTruncateTailMatchesPi(t *testing.T) {
	contents := []string{
		"", "\n", "\n\n", "a", "a\n", "a\nb", "a\nb\n", "\na", "a\n\nb", "a\n\n", "\n\n\nx",
		"line one\nline two\nline three\nline four", "line one\nline two\nline three\nline four\n",
		"é", "éé\nèè", "日本語\n日本語\n日本語", "😀😀😀", "a😀b\n😀😀\nc", "x\u0301y\u0301", "\u2028\u2029", "a\rb\r\nc\r\n", "\r\n\r\n", "tab\tx\n\ty",
		"long line without breaks, long enough to be cut: 0123456789abcdefghij", "日本語日本語日本語日本語", "😀a😀b😀c😀d😀e",
	}
	limits := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 13, 20, 40, 100}
	var cases []tailCase
	for _, content := range contents {
		for _, maxBytes := range limits {
			for _, maxLines := range []int{0, 1, 2, 3, 5, 100} {
				cases = append(cases, tailCase{content, maxBytes, maxLines})
			}
		}
	}
	var want []tailWant
	pioracle.Run(t, `
const { truncateTail } = await load("pi-coding-agent/core/tools/truncate.js");
emit(input.map(({ content, maxBytes, maxLines }) => truncateTail(content, { maxBytes, maxLines })));`, cases, &want)
	if len(want) != len(cases) {
		t.Fatalf("Pi returned %d results for %d cases", len(want), len(cases))
	}
	bad := 0
	for i, c := range cases {
		got := TruncateTail(c.Content, c.MaxBytes, c.MaxLines)
		w := want[i]
		wantBy := ""
		if w.TruncatedBy != nil {
			wantBy = *w.TruncatedBy
		}
		if got.Content != w.Content || got.Truncated != w.Truncated || got.TruncatedBy != wantBy || got.TotalLines != w.TotalLines || got.TotalBytes != w.TotalBytes || got.OutputLines != w.OutputLines || got.OutputBytes != w.OutputBytes || got.LastLinePartial != w.LastLinePartial {
			if bad++; bad <= 8 {
				t.Errorf("TruncateTail(%q, bytes %d, lines %d) = %+v, Pi %+v (truncatedBy %q)", c.Content, c.MaxBytes, c.MaxLines, got, w, wantBy)
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d of %d cases differ from Pi", bad, len(cases))
	}
}
