package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The committed table is what the generator writes from UnicodeData.txt of Unicode 6.0.0. The file is not in the
// repository: PIG_UNICODE_600_UCD names a copy, whose sha256 selectDiacritics checks. Without it this test proves
// nothing and says so; the table's shape is checked in its own package.
func TestCommittedTableIsGeneratedFromUnicode600(t *testing.T) {
	path := os.Getenv("PIG_UNICODE_600_UCD")
	if path == "" {
		t.Skip("PIG_UNICODE_600_UCD names no UnicodeData.txt of Unicode 6.0.0 (" + unicodeDataURL + "): the table is not generated again here")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	points, err := selectDiacritics(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 297 || points[0] != "0305" || points[len(points)-1] != "1D244" {
		t.Fatalf("the rule selects %d code points from %s to %s, want 297 from 0305 to 1D244", len(points), points[0], points[len(points)-1])
	}
	have, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(tablePath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(have, table(points)) {
		t.Fatalf("%s is not what the generator writes", tablePath)
	}
}

// The rule itself, on lines of the file's shape: a mark of class 230 without a mapping is selected; one that a
// canonical pair ends with, one with a mapping of its own, one of another class and one named only by a compatibility
// mapping's tag are judged as the rule says. The digest check is the caller's, so the rule is exercised below it.
func TestSelectionRule(t *testing.T) {
	lines := "0041;LATIN CAPITAL LETTER A;Lu;0;L;;;;;N;;;;0061;\n" +
		"00C0;LATIN CAPITAL LETTER A WITH GRAVE;Lu;0;L;0041 0300;;;;N;;;;00E0;\n" +
		"00A8;DIAERESIS;Sk;0;ON;<compat> 0020 0305;;;;N;;;;;\n" +
		"0300;COMBINING GRAVE ACCENT;Mn;230;NSM;;;;;N;;;;;\n" +
		"0305;COMBINING OVERLINE;Mn;230;NSM;;;;;N;;;;;\n" +
		"0316;COMBINING GRAVE ACCENT BELOW;Mn;220;NSM;;;;;N;;;;;\n" +
		"0340;COMBINING GRAVE TONE MARK;Mn;230;NSM;0300;;;;N;;;;;\n" +
		"030D;COMBINING VERTICAL LINE ABOVE;Mn;230;NSM;;;;;N;;;;;\n"
	got := selectLines(lines)
	if len(got) != 2 || got[0] != "0305" || got[1] != "030D" {
		t.Fatalf("selected %v, want 0305 and 030D", got)
	}
}
