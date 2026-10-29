// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// jdcFixtures are real JPEG files of the corpus: three photographs of the jpeg-decoder crate's benches (baseline 4:4:4, grayscale and
// progressive 4:4:4; MIT OR Apache-2.0, see REUSE.toml) and Photon's own encoding of the scene of jpeg_oracle_test.go.
var jdcFixtures = []struct{ name, file string }{
	{"fixture-tower", "photon-jpegdecode-tower.jpg"},
	{"fixture-tower-gray", "photon-jpegdecode-tower-gray.jpg"},
	{"fixture-tower-progressive", "photon-jpegdecode-tower-progressive.jpg"},
	{"fixture-scene-photon", "scene-photon-300x200-q80.jpg"},
}

func (b *jdcBuilder) fixtures() {
	for _, f := range jdcFixtures {
		data, err := os.ReadFile(filepath.Join("testdata", f.file))
		if err != nil {
			panic(err)
		}
		b.add(f.name, data)
	}
}

// jdcMutate is the mutation op of data (a seeded, deterministic one): bit flips and byte changes, truncations (with an EOI after
// some), deleted, inserted, duplicated and swapped ranges, damaged markers and lengths, in the headers or in the entropy-coded data.
func jdcMutate(data []byte, rnd *corpusRNG, op int) []byte {
	out := bytes.Clone(data)
	n := len(out)
	scanStart := bytes.Index(out, []byte{0xFF, 0xDA})
	if scanStart < 0 {
		scanStart = n
	}
	pos := func(lo, hi int) int { return min(lo+int(rnd.next()>>4)%max(hi-lo, 1), n-1) }
	switch op % 14 {
	case 0: // a bit flip anywhere
		i := pos(0, n)
		out[i] ^= 1 << (rnd.next() >> 8 & 7)
	case 1: // a bit flip in the headers
		i := pos(2, scanStart)
		out[i] ^= 1 << (rnd.next() >> 8 & 7)
	case 2: // a bit flip in the entropy-coded data
		i := pos(scanStart, n)
		out[i] ^= 1 << (rnd.next() >> 8 & 7)
	case 3: // a byte set to a value that matters: marker bytes, 0, extremes
		i := pos(2, n)
		out[i] = []byte{0x00, 0xFF, 0xD8, 0xD9, 0xDA, 0xC4, 0xDB, 0xDD, 0xD0, 0x7F, 0x80, 0x01}[rnd.next()>>8%12]
	case 4: // a random byte in the headers
		out[pos(2, scanStart)] = byte(rnd.next() >> 8)
	case 5: // truncated
		out = out[:pos(2, n)]
	case 6: // truncated in the entropy-coded data, followed by an EOI
		out = append(out[:pos(scanStart, n)], 0xFF, 0xD9)
	case 7: // a range deleted
		i := pos(2, n)
		j := min(n, i+1+int(rnd.next()>>8)%9)
		out = append(out[:i], out[j:]...)
	case 8: // bytes inserted
		i := pos(2, n)
		ins := make([]byte, 1+int(rnd.next()>>8)%6)
		for k := range ins {
			ins[k] = byte(rnd.next() >> 8)
		}
		out = append(out[:i], append(ins, out[i:]...)...)
	case 9: // a range copied over another
		i, j := pos(2, n), pos(2, n)
		l := 1 + int(rnd.next()>>8)%24
		for k := 0; k < l && i+k < n && j+k < n; k++ {
			out[j+k] = out[i+k]
		}
	case 10: // a marker length changed
		var marks []int
		for i := 2; i+3 < scanStart; i++ {
			if out[i] == 0xFF && out[i+1] >= 0xC0 && out[i+1] != 0xFF && out[i+1] != 0xD8 && out[i+1] != 0xD9 && out[i+1] != 0x00 {
				marks = append(marks, i)
			}
		}
		if len(marks) > 0 {
			i := marks[int(rnd.next()>>8)%len(marks)]
			out[i+3] += byte(int(rnd.next()>>8)%5) - 2
		}
	case 11: // a marker byte changed to another marker
		var marks []int
		for i := 2; i+1 < n; i++ {
			if out[i] == 0xFF && out[i+1] >= 0xC0 && out[i+1] != 0xFF {
				marks = append(marks, i)
			}
		}
		if len(marks) > 0 {
			i := marks[int(rnd.next()>>8)%len(marks)]
			out[i+1] = byte(0xC0 + int(rnd.next()>>8)%0x3F)
		}
	case 12: // several bit flips in the data
		for range 1 + int(rnd.next()>>8)%6 {
			out[pos(scanStart, n)] ^= 1 << (rnd.next() >> 8 & 7)
		}
	default: // two ranges swapped
		i, j := pos(2, n), pos(2, n)
		l := 1 + int(rnd.next()>>8)%16
		for k := 0; k < l && i+k < n && j+k < n; k++ {
			out[i+k], out[j+k] = out[j+k], out[i+k]
		}
	}
	return out
}

// mutants: the mutations of a few of the corpus' inputs.
func (b *jdcBuilder) mutants() {
	bases := []string{
		"seq-420-33x31", "seq-444-16x16", "seq-411-9x17", "seq-h3-33x31", "seq-mixed-65x20", "seqsep-420-9x17", "gray-1x1-9x17",
		"prog-0-420-33x31", "prog-4-444-9x17", "prog-3-420-65x20", "prog-2-mixed-33x31", "rst-420-33x31-3", "rstprog-0-420-2",
		"ll-8-p-1-7x5", "ll-12-p-4", "cs4-adobe2-1", "cs3-ids789-adobe0-420", "st-base", "st-mjpeg-avi1",
	}
	byName := map[string][]byte{}
	for _, c := range b.cases {
		byName[c.name] = c.data
	}
	for bi, name := range bases {
		data, ok := byName[name]
		if !ok {
			panic("mutation base " + name + " is not in the corpus")
		}
		for i := range 84 {
			rnd := corpusRNG(uint32(bi+1)*0x9e3779b1 ^ uint32(i+1)*0x85ebca6b)
			b.add(jdcName("mut", name, i), jdcMutate(data, &rnd, i))
		}
	}
}

var (
	jdcOnce  sync.Once
	jdcCases []jdcCase
)

// jdcCorpus is the corpus of TestDecodeJPEGMatchesPhoton.
func jdcCorpus() []jdcCase {
	jdcOnce.Do(func() {
		var b jdcBuilder
		jdcCases = b.all()
	})
	return jdcCases
}

var jpegDecodeDump = flag.String("jpegdecode.dump", "", "write the corpus to this directory as <name>.jpg (the input of testdata/jpegdecode_oracle.mjs) and stop")

// jdcRow is a row of testdata/photon-jpegdecode-corpus.tsv.
type jdcRow struct {
	length    string
	inputHash string
	width     string // "error" where Photon throws
	height    string
	pixelHash string
}

func jdcLoadRows(t *testing.T) map[string]jdcRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "photon-jpegdecode-corpus.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]jdcRow{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 7 || f[0] != "jpeg" {
			t.Fatalf("bad row %q", line)
		}
		rows[f[1]] = jdcRow{f[2], f[3], f[4], f[5], f[6]}
	}
	return rows
}

// TestDecodeJPEGMatchesPhoton decodes the corpus of jpegdecode_corpus_test.go, which Photon's `PhotonImage.new_from_byteslice(bytes)`
// decoded (@silvia-odwyer/photon 0.3.3 and @silvia-odwyer/photon-node 0.3.4, both image 0.24.9 over jpeg-decoder 0.3.1; regenerate
// testdata/photon-jpegdecode-corpus.tsv with `go test -run TestDecodeJPEGMatchesPhoton -jpegdecode.dump=<directory>` and then
// `node jpegdecode_oracle.mjs <package directory> <directory>`): baseline, extended sequential, progressive and lossless streams of
// every sampling, size, restart interval, colour space and scan script; tables and markers in every place; streams cut short or
// damaged. Every width, height and SHA-256 of the RGBA pixels must be Photon's, and where Photon throws DecodeJPEG must fail.
func TestDecodeJPEGMatchesPhoton(t *testing.T) {
	cases := jdcCorpus()
	if dir := *jpegDecodeDump; dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			if err := os.WriteFile(filepath.Join(dir, c.name+".jpg"), c.data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("wrote %d files to %s", len(cases), dir)
		return
	}
	rows := jdcLoadRows(t)
	if len(rows) != len(cases) {
		t.Errorf("the table has %d rows for %d inputs", len(rows), len(cases))
	}
	failures, identical, errored := 0, 0, 0
	for _, c := range cases {
		if failures >= 30 {
			t.Fatalf("stopping after %d failures", failures)
		}
		want, ok := rows[c.name]
		if !ok {
			t.Errorf("%s: no row", c.name)
			failures++
			continue
		}
		if got := sha256Hex(c.data); got != want.inputHash || fmt.Sprint(len(c.data)) != want.length {
			t.Errorf("%s: the input drifted: %d bytes %s, the table has %s bytes %s", c.name, len(c.data), got, want.length, want.inputHash)
			failures++
			continue
		}
		img, err := DecodeJPEG(c.data)
		switch {
		case want.width == "error":
			if err == nil {
				t.Errorf("%s: Photon throws, DecodeJPEG returned %dx%d", c.name, img.Rect.Dx(), img.Rect.Dy())
				failures++
				continue
			}
			errored++
		case err != nil:
			t.Errorf("%s: Photon decodes %sx%s, DecodeJPEG failed: %v", c.name, want.width, want.height, err)
			failures++
			continue
		default:
			got := fmt.Sprintf("%dx%d %s", img.Rect.Dx(), img.Rect.Dy(), sha256Hex(img.Pix))
			if exp := fmt.Sprintf("%sx%s %s", want.width, want.height, want.pixelHash); got != exp {
				t.Errorf("%s: got %s, Photon gives %s", c.name, got, exp)
				failures++
				continue
			}
		}
		identical++
	}
	t.Logf("%d inputs, %d identical to Photon (%d of them rejected as Photon rejects them), %d different", len(cases), identical, errored, failures)
}
