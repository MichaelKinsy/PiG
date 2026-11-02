// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bufio"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// formatCase is a file of the corpus of one of the formats (ICO, PNM, DDS, HDR, farbfeld) compared with Photon.
type formatCase struct {
	name string
	data []byte
	// build makes the data of a file too big to keep with the others (data is nil).
	build func() []byte
}

// bytes is the file's data.
func (c formatCase) bytes() []byte {
	if c.build != nil {
		return c.build()
	}
	return c.data
}

// formatCorpusBuilder collects the cases of a corpus and refuses duplicate names (the names are the keys of the table of Photon's results).
type formatCorpusBuilder struct {
	cases []formatCase
	seen  map[string]bool
}

func (b *formatCorpusBuilder) claim(name string) {
	if b.seen == nil {
		b.seen = map[string]bool{}
	}
	if b.seen[name] {
		panic("duplicate corpus name " + name)
	}
	b.seen[name] = true
}

func (b *formatCorpusBuilder) add(name string, data []byte) {
	b.claim(name)
	b.cases = append(b.cases, formatCase{name: name, data: data})
}

func (b *formatCorpusBuilder) addLazy(name string, build func() []byte) {
	b.claim(name)
	b.cases = append(b.cases, formatCase{name: name, build: build})
}

// photonRow is a line of photon-<label>decode-corpus.tsv.
type photonRow struct {
	length    int
	inputHash string
	width     string // "error" when Photon throws
	height    string
	pixels    string
}

func readPhotonTable(t testing.TB, label string) map[string]photonRow {
	t.Helper()
	f, err := os.Open("testdata/photon-" + label + "decode-corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	rows := map[string]photonRow{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 7 || fields[0] != label {
			t.Fatalf("bad line %q", scanner.Text())
		}
		length, err := strconv.Atoi(fields[2])
		if err != nil {
			t.Fatal(err)
		}
		rows[fields[1]] = photonRow{length, fields[3], fields[4], fields[5], fields[6]}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

// comparePhotonCorpus is the body of the TestDecode<Format>MatchesPhoton tests: Decode against Photon's
// `PhotonImage.new_from_byteslice(..).get_raw_pixels()` for every file of the corpus, the same dimensions and the same pixels (SHA-256)
// where Photon decodes, an error where it throws. testdata/photon-<label>decode-corpus.tsv is what testdata/decodeformats_oracle.mjs printed
// for the files under both Photon 0.3.3 and photon-node 0.3.4 (image 0.24.9); PHOTON_<LABEL>_CORPUS_DIR=dir writes the files to dir for
// the script.
func comparePhotonCorpus(t *testing.T, label, extension string, corpus []formatCase) {
	t.Helper()
	if dir := os.Getenv("PHOTON_" + strings.ToUpper(label) + "_CORPUS_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, c := range corpus {
			if err := os.WriteFile(filepath.Join(dir, c.name+extension), c.bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("wrote %d files to %s", len(corpus), dir)
	}

	rows := readPhotonTable(t, label)
	var decoded, failed, bad int
	report := func(format string, args ...any) {
		bad++
		if bad <= 40 {
			t.Errorf(format, args...)
		}
	}
	names := map[string]bool{}
	for _, c := range corpus {
		names[c.name] = true
		row, ok := rows[c.name]
		if !ok {
			report("%s: not in the table of Photon's results", c.name)
			continue
		}
		data := c.bytes()
		if row.length != len(data) || row.inputHash != sha256Hex(data) {
			report("%s: the generated input is not the one the table was made from (length %d, table %d)", c.name, len(data), row.length)
			continue
		}

		decodedImage, err := Decode(data)
		if row.width == "error" {
			failed++
			if err == nil {
				report("%s: Photon throws, Decode gives a %v image", c.name, decodedImage.Bounds().Size())
			}
			continue
		}
		decoded++
		if err != nil {
			report("%s: Photon decodes %sx%s, Decode fails: %v", c.name, row.width, row.height, err)
			continue
		}
		nrgba, isNRGBA := decodedImage.(*image.NRGBA)
		if !isNRGBA {
			report("%s: Decode gives a %T, not an NRGBA", c.name, decodedImage)
			continue
		}
		size := nrgba.Rect.Size()
		if got := fmt.Sprintf("%dx%d", size.X, size.Y); got != row.width+"x"+row.height {
			report("%s: size %s, Photon %sx%s", c.name, got, row.width, row.height)
			continue
		}
		if got := sha256Hex(nrgba.Pix); got != row.pixels {
			report("%s: %dx%d pixels differ from Photon's", c.name, size.X, size.Y)
		}
	}
	for name := range rows {
		if !names[name] {
			report("%s: in the table but not in the corpus", name)
		}
	}
	t.Logf("%d files: %d decode in Photon, %d throw; %d differences", len(corpus), decoded, failed, bad)
	if bad > 0 {
		t.Errorf("%d differences in %d files", bad, len(corpus))
	}
}
