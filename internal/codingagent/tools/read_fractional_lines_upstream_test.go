package tools

import (
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// read.ts:160-199 keeps offset and limit as JavaScript numbers: slice truncates them when selecting lines, but the
// continuation notices print the untruncated arithmetic, and a fractional start makes allLines[startLine] undefined,
// so an over-long first line throws from Buffer.byteLength. Expectations are Pi 1.1.0's tool results for the same calls
// (faux provider "Run: ext tool read {...}", --mode json).
func TestReadFractionalOffsetAndLimitLikePi(t *testing.T) {
	five := "a\nb\nc\nd\ne\n"
	var big strings.Builder
	for i := 1; i <= 3000; i++ {
		big.WriteString(strconv.Itoa(i) + "\n")
	}
	long := strings.Repeat("a", 60000) + "\n"
	num := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name, text     string
		offset, limit  *float64
		wantTail, want string
	}{
		{name: "limit 1.5", text: five, limit: num(1.5), want: "a\n\n[4.5 more lines in file. Use offset=2.5 to continue.]"},
		{name: "offset 2.5 limit 1", text: five, offset: num(2.5), limit: num(1), want: "b\n\n[3.5 more lines in file. Use offset=3.5 to continue.]"},
		{name: "offset 0.5 limit 0.25", text: five, offset: num(0.5), limit: num(0.25), want: "\n\n[5.75 more lines in file. Use offset=1.25 to continue.]"},
		{name: "limit -2.5", text: five, limit: num(-2.5), want: "a\nb\nc\nd\n\n[8.5 more lines in file. Use offset=-1.5 to continue.]"},
		{name: "offset 10.5 truncated by lines", text: big.String(), offset: num(10.5), wantTail: "\n\n[Showing lines 10.5-2009.5 of 3001. Use offset=2010.5 to continue.]"},
		{name: "offset 1.5 truncated by bytes", text: "s\n" + long, offset: num(1.5), want: "s\n\n[Showing lines 1.5-1.5 of 3 (50.0KB limit). Use offset=2.5 to continue.]"},
		{name: "offset 2.5 over-long first line", text: "s\n" + long, offset: num(2.5), want: `error: The "string" argument must be of type string or an instance of Buffer or ArrayBuffer. Received undefined`},
		{name: "offset 2 over-long first line", text: "s\n" + long, offset: num(2), want: "[Line 2 is 58.6KB, exceeds 50.0KB limit. Use bash: sed -n '2p' f.txt | head -c 51200]"},
		{name: "offset 7.5 beyond end", text: five, offset: num(7.5), want: "error: Offset 7.5 is beyond end of file (6 lines total)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := readTextResult(readParams{Path: "f.txt", Offset: tc.offset, Limit: tc.limit}, []byte(tc.text))
			got := ""
			if err != nil {
				got = "error: " + err.Error()
			} else {
				got = result.Content[0].(ai.TextContent).Text
			}
			if tc.wantTail != "" {
				if !strings.HasSuffix(got, tc.wantTail) {
					t.Fatalf("text ends %q, want suffix %q", got[max(0, len(got)-120):], tc.wantTail)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("text = %q\nwant   %q", got, tc.want)
			}
		})
	}
}
