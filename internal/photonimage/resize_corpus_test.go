package photonimage

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestResizeLanczos3MatchesPhotonOnStripsAndNearOneRatios resizes the 251 pictures of testdata/resize_oracle.mjs (wide and tall strips
// up to 3,000 pixels, ratios near 1, halves, enlargements; regenerate testdata/photon-resize-corpus.tsv with `node resize_oracle.mjs
// <Photon package directory>`) and compares the SHA-256 of every output with Photon's. The kernel's weights use f32 `sin`: sinf.go
// is why a window of hundreds of weights matches (for a 2023x46 picture resized to 2000x45 `math.Sin` gives one channel of one pixel
// that is one step off).
func TestResizeLanczos3MatchesPhotonOnStripsAndNearOneRatios(t *testing.T) {
	file, err := os.Open("testdata/photon-resize-corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	same, failures := 0, 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 9 || fields[0] != "resize" {
			t.Fatalf("bad line %q", scanner.Text())
		}
		var n [5]int
		for i := range n {
			if n[i], err = strconv.Atoi(fields[2+i]); err != nil {
				t.Fatal(err)
			}
		}
		seed, w, h, toW, toH := n[0], n[1], n[2], n[3], n[4]
		src := corpusImage(fields[1], seed-1, w, h)
		if got := sha256Hex(src.Pix); got != fields[7] {
			t.Fatalf("%s seed %d: the Go generator makes pixels with SHA-256 %s, resize_oracle.mjs %s", fields[1], seed, got, fields[7])
		}
		if got := sha256Hex(ResizeLanczos3(src, toW, toH).Pix); got != fields[8] {
			failures++
			if failures <= 8 {
				t.Errorf("%s seed %d: %dx%d to %dx%d has SHA-256 %s, Photon's %s", fields[1], seed, w, h, toW, toH, got, fields[8])
			}
			continue
		}
		same++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d resizes identical to Photon's, %d differences", same, failures)
}
