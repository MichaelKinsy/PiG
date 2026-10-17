package turn

import (
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func sliceJSON(s OutputSlice) string {
	return jv.Stringify(jobj("text", s.Text, "bytes", float64(s.Bytes), "droppedBytes", float64(s.DroppedBytes), "droppedLines", float64(s.DroppedLines)))
}

func boundedJSON(b BoundedOutput) string {
	return jv.Stringify(jobj("text", b.Text, "droppedBytes", float64(b.DroppedBytes), "droppedLines", float64(b.DroppedLines)))
}

// TestOutputOracle replays src/harness/output.ts (pi-durable main da866ada, oracle/gen_output.mjs): boundOutput over texts
// with multi-byte characters, controls and BOMs at every limit, and OutputBuffer over random string and byte chunks (split
// and invalid UTF-8, skips) with snapshots in the middle and after end().
func TestOutputOracle(t *testing.T) {
	var bounds, buffers, snapshots int
	for n, f := range corpusLines(t, "output.hex") {
		switch f[0] {
		case "B":
			maxBytes, _ := strconv.Atoi(f[2])
			maxLines, _ := strconv.Atoi(f[3])
			got := sliceJSON(BoundOutput(unhex(t, f[1]), OutputLimits{MaxBytes: maxBytes, MaxLines: maxLines, Retain: f[4]}))
			if want := unhex(t, f[5]); got != want {
				t.Fatalf("line %d boundOutput(%q, %s/%s %s):\n got %s\nwant %s", n, unhex(t, f[1]), f[2], f[3], f[4], got, want)
			}
			bounds++
		case "O":
			in := parseObject(t, unhex(t, f[1]))
			lim := objField(in, "limits")
			buf := NewOutputBuffer(OutputLimits{MaxBytes: int(num(lim, "maxBytes")), MaxLines: int(num(lim, "maxLines")), Retain: str(lim, "retain")})
			wantSnaps, _ := jv.ParseString(unhex(t, f[2]))
			want := wantSnaps.([]any)
			var got []any
			ops, _ := arrayField(in, "ops")
			for _, o := range ops {
				op := o.(*jv.Object)
				var skip *Skip
				if s := objField(op, "skip"); s != nil {
					ends, _ := s.Get("endsWithNewline")
					skip = &Skip{Bytes: int(num(s, "bytes")), Newlines: int(num(s, "newlines")), EndsWithNewline: ends == true}
				}
				switch str(op, "k") {
				case "s":
					buf.PushString(str(op, "v"), skip)
				case "b":
					b, _ := hex.DecodeString(str(op, "v"))
					buf.PushBytes(b, skip)
				case "snap":
					got = append(got, parseObject(t, boundedJSON(buf.Snapshot())))
				case "end":
					buf.End()
					got = append(got, parseObject(t, boundedJSON(buf.Snapshot())))
				}
			}
			if jv.Stringify(got) != jv.Stringify(want) {
				t.Fatalf("line %d buffer %s:\n got %s\nwant %s", n, unhex(t, f[1]), jv.Stringify(got), jv.Stringify(want))
			}
			buffers++
			snapshots += len(got)
		}
	}
	if bounds < 400 || buffers < 300 || snapshots < 500 {
		t.Fatalf("corpus covers %d bounds, %d buffers, %d snapshots", bounds, buffers, snapshots)
	}
}
