package env

import (
	"strings"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// Port of Pi main da7e26cc8bca83c15f04e83c9b468e79d8a35cfe (#12451):
// remoteTextLineReader appends to a byte buffer and scans only newly decoded bytes.
func TestRemoteTextLineReaderLongLineScalesLinearly(t *testing.T) {
	payload := []byte(strings.Repeat("x", 16<<20) + "\n")
	run := func(chunk int) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			r := &remoteTextLineReader{
				decoder: durableenv.NewStreamDecoder(),
				readAt: func(offset, _ int) ([]byte, error) {
					if offset >= len(payload) {
						return nil, nil
					}
					end := min(offset+chunk, len(payload))
					return payload[offset:end], nil
				},
			}
			start := time.Now()
			line, err := r.ReadLine(t.Context())
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
		t.Fatalf("16 MiB line in 4 KiB reads took %v vs whole %v (quadratic rescan/copy)", pieces, whole)
	}
}
