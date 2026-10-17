package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Pi #10015: when a command's last line is over 50 KB and ends with a newline, the truncation notice names the
// bytes after the final newline: "(line is 0B)". The notice names the size of the line that was cut.
func TestBashLongLastLineNoticeNamesTheLineSize(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	for name, command := range map[string]string{
		"newline":    `head -c 60000 /dev/zero | tr '\0' a; echo`,
		"no newline": `head -c 60000 /dev/zero | tr '\0' a`,
	} {
		t.Run(name, func(t *testing.T) {
			args, _ := json.Marshal(bashParams{Command: command})
			res, err := bt.Execute(context.Background(), "", args, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(res.Text(), "of line 1 (line is 58.6KB).") {
				t.Fatalf("notice = %q", res.Text()[len(res.Text())-120:])
			}
		})
	}
}

func TestOutputAccumulatorLastLineBytes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   int
	}{
		{"empty", nil, 0},
		{"open line", []string{"abc"}, 3},
		{"open line over chunks", []string{"ab", "cde"}, 5},
		{"newline ends the line", []string{"abcd\n"}, 4},
		{"newline in a later chunk", []string{"abc", "d\n"}, 4},
		{"newline alone in a later chunk", []string{"abcd", "\n"}, 4},
		{"two lines in one chunk", []string{"ab\ncdefg\n"}, 5},
		{"line after a line", []string{"ab\ncd"}, 2},
		{"blank last line", []string{"abc\n\n"}, 0},
		{"open line after many lines", []string{"a\nbb\nccc\nd"}, 1},
		{"split multibyte line", []string{"é\xc3", "\xa9\n"}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewOutputAccumulator("")
			for _, chunk := range tc.chunks {
				if err := a.Append([]byte(chunk)); err != nil {
					t.Fatal(err)
				}
			}
			a.Finish()
			if got := a.LastLineBytes(); got != tc.want {
				t.Fatalf("LastLineBytes() = %d, want %d", got, tc.want)
			}
		})
	}
}
