package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// path-utils.ts resolveReadPathAsync (:86) awaits its existence probes one after another, in a fixed order, and the first hit wins: the path as given, the AM/PM
// variant, the NFD variant, the curly-quote variant, then the NFD plus curly-quote variant. Each case creates two candidates and pins which one wins, so a probe
// reordered, run ahead of the exact one or dropped changes the answer. The spellings are told apart only on a file system that keeps NFC and NFD names apart
// (the tests below skip on one that folds them, where the order between those two is not observable).
func TestResolveReadPathFirstProbeThatExistsWins(t *testing.T) {
	const nnbsp = "\u202f"
	const nfc = "caf\u00e9"
	const nfd = "cafe\u0301"
	cwd := t.TempDir()
	create := func(name string) string {
		t.Helper()
		path := filepath.Join(cwd, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	probe := create("probe-nfc")
	_ = os.Remove(probe)
	create(nfd + "-probe")
	if _, err := os.Stat(filepath.Join(cwd, nfc+"-probe")); err == nil {
		t.Skip("the file system folds NFC and NFD names")
	}
	want := func(query, winner string) {
		t.Helper()
		if got, err := resolveReadPath(query, cwd); err != nil || got != filepath.Join(cwd, winner) {
			t.Errorf("resolveReadPath(%q) = %q, %v; want %q", query, got, err, winner)
		}
	}
	// The path as given beats every variant.
	create("q AM.png")
	create("q" + nnbsp + "AM.png")
	want("q AM.png", "q AM.png")
	// AM/PM variant before NFD variant.
	create(nfc + nnbsp + "AM.png")
	create(nfd + " AM.png")
	want(nfc+" AM.png", nfc+nnbsp+"AM.png")
	// NFD variant before curly-quote variant.
	create("d\u2019" + nfc + ".txt")
	create("d'" + nfd + ".txt")
	want("d'"+nfc+".txt", "d'"+nfd+".txt")
	// Curly-quote variant before the NFD plus curly-quote variant.
	create("e\u2019" + nfc + ".txt")
	create("e\u2019" + nfd + ".txt")
	want("e'"+nfc+".txt", "e\u2019"+nfc+".txt")
}
