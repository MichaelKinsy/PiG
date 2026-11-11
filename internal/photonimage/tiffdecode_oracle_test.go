// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// tiffOracleRow is a line of photon-tiffdecode-corpus.tsv.
type tiffOracleRow struct {
	length    int
	inputHash string
	width     string // "error" when Photon throws
	height    string
	pixels    string
}

func readTIFFOracle(t testing.TB) map[string]tiffOracleRow {
	t.Helper()
	f, err := os.Open("testdata/photon-tiffdecode-corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	rows := map[string]tiffOracleRow{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 7 || fields[0] != "tiff" {
			t.Fatalf("bad line %q", scanner.Text())
		}
		length, err := strconv.Atoi(fields[2])
		if err != nil {
			t.Fatal(err)
		}
		rows[fields[1]] = tiffOracleRow{length, fields[3], fields[4], fields[5], fields[6]}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func tiffSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestDecodeTIFFMatchesPhoton compares Decode with Photon's `PhotonImage.new_from_byteslice(..).get_raw_pixels()` on the corpus of
// tiffdecode_corpus_test.go: the same dimensions and the same pixels (SHA-256) where Photon decodes, an error where it throws.
// testdata/photon-tiffdecode-corpus.tsv is what testdata/tiffdecode_oracle.mjs printed for the files under both Photon 0.3.3 and
// photon-node 0.3.4 (image 0.24.9 over tiff 0.9.1); PHOTON_TIFF_CORPUS_DIR=dir writes the files of the corpus to dir for the script.
func TestDecodeTIFFMatchesPhoton(t *testing.T) {
	corpus := tiffCorpus()

	if dir := os.Getenv("PHOTON_TIFF_CORPUS_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, c := range corpus {
			if err := os.WriteFile(filepath.Join(dir, c.name+".tif"), c.bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("wrote %d files to %s", len(corpus), dir)
	}

	rows := readTIFFOracle(t)
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
		if row.length != len(data) || row.inputHash != tiffSHA256(data) {
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
			report("%s: Decode gives a %T, not the NRGBA of DecodeTIFF", c.name, decodedImage)
			continue
		}
		size := nrgba.Rect.Size()
		if got := fmt.Sprintf("%dx%d", size.X, size.Y); got != row.width+"x"+row.height {
			report("%s: size %s, Photon %sx%s", c.name, got, row.width, row.height)
			continue
		}
		if got := tiffSHA256(nrgba.Pix); got != row.pixels {
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
