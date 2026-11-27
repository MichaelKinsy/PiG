package node

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// Port of Pi main da7e26cc8bca83c15f04e83c9b468e79d8a35cfe (#12451):
// nodeTextLineReader scans only newly read bytes so a long line delivered in 4 KiB reads scales linearly.
func TestNodeTextLineReaderLongLineScalesLinearly(t *testing.T) {
	payload := strings.Repeat("x", 16<<20) + "\n"
	run := func(chunk int) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			orig := readFileAt
			readFileAt = func(_ *os.File, p []byte, off int64) (int, error) {
				if int(off) >= len(payload) {
					return 0, io.EOF
				}
				n := min(len(p), chunk, len(payload)-int(off))
				copy(p[:n], payload[off:int(off)+n])
				return n, nil
			}
			start := time.Now()
			r := &nodeTextLineReader{path: "mem", chunk: make([]byte, chunk)}
			line, err := r.ReadLine(t.Context())
			readFileAt = orig
			if err != nil || line == nil || !line.Terminated || len(line.Text) != 16<<20 {
				t.Fatalf("chunk=%d line=%v err=%v", chunk, line != nil, err)
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	whole := run(len(payload))
	pieces := run(4 << 10)
	if pieces > 10*whole+60*time.Millisecond {
		t.Fatalf("16 MiB line in 4 KiB reads took %v vs whole %v (quadratic rescan)", pieces, whole)
	}
}
