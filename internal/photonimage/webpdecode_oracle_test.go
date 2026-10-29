// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

package photonimage

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// webpOracleRow is a line of photon-webpdecode-corpus.tsv.
type webpOracleRow struct {
	length    int
	inputHash string
	width     string // "error" when Photon throws
	height    string
	pixels    string
}

func readWebPOracle(t testing.TB) map[string]webpOracleRow {
	t.Helper()
	f, err := os.Open("testdata/photon-webpdecode-corpus.tsv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })

	rows := map[string]webpOracleRow{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 7 || fields[0] != "webp" {
			t.Fatalf("bad line %q", scanner.Text())
		}
		length, err := strconv.Atoi(fields[2])
		if err != nil {
			t.Fatal(err)
		}
		rows[fields[1]] = webpOracleRow{length, fields[3], fields[4], fields[5], fields[6]}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return rows
}

func webpSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestDecodeWebPMatchesPhoton compares DecodeWebP with Photon's `PhotonImage.new_from_byteslice(..).get_raw_pixels()` on the corpus of
// webpdecode_corpus_test.go: the same dimensions and the same pixels (SHA-256) where Photon decodes, an error where it throws.
// testdata/photon-webpdecode-corpus.tsv is what testdata/webpdecode_oracle.mjs printed for the files under both Photon 0.3.3 and
// photon-node 0.3.4 (image 0.24.9); PHOTON_WEBP_CORPUS_DIR=dir writes the files of the corpus to dir for the script.
func TestDecodeWebPMatchesPhoton(t *testing.T) {
	corpus := webpCorpus()

	if dir := os.Getenv("PHOTON_WEBP_CORPUS_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, c := range corpus {
			if err := os.WriteFile(filepath.Join(dir, c.name+".webp"), c.data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("wrote %d files to %s", len(corpus), dir)
	}

	rows := readWebPOracle(t)
	var decoded, failed, bad int
	report := func(format string, args ...any) {
		bad++
		if bad <= 25 {
			t.Errorf(format, args...)
		}
	}
	for _, c := range corpus {
		row, ok := rows[c.name]
		if !ok {
			report("%s: not in the table of Photon's results", c.name)
			continue
		}
		if row.length != len(c.data) || row.inputHash != webpSHA256(c.data) {
			report("%s: the generated input is not the one the table was made from (length %d, table %d)", c.name, len(c.data), row.length)
			continue
		}

		img, err := DecodeWebP(c.data)
		if row.width == "error" {
			failed++
			if err == nil {
				report("%s: Photon throws, DecodeWebP gives a %v image", c.name, img.Rect.Size())
			}
			continue
		}
		decoded++
		if err != nil {
			report("%s: Photon decodes %sx%s, DecodeWebP fails: %v", c.name, row.width, row.height, err)
			continue
		}
		size := img.Rect.Size()
		if got := fmt.Sprintf("%dx%d", size.X, size.Y); got != row.width+"x"+row.height {
			report("%s: size %s, Photon %sx%s", c.name, got, row.width, row.height)
			continue
		}
		if got := webpSHA256(img.Pix); got != row.pixels {
			report("%s: %dx%d pixels differ from Photon's", c.name, size.X, size.Y)
		}
	}
	for name := range rows {
		found := false
		for _, c := range corpus {
			if c.name == name {
				found = true
				break
			}
		}
		if !found {
			report("%s: in the table but not in the corpus", name)
		}
	}
	t.Logf("%d files: %d decode in Photon, %d throw; %d differences", len(corpus), decoded, failed, bad)
	if bad > 0 {
		t.Errorf("%d differences in %d files", bad, len(corpus))
	}
}
