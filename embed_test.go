package pig

import (
	"os"
	"testing"
)

func TestBundledChangelogGzipMatchesSource(t *testing.T) {
	want, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if Changelog() != string(want) {
		t.Fatal("CHANGELOG.md.gz does not inflate to exactly CHANGELOG.md; run: gzip -9nc CHANGELOG.md > CHANGELOG.md.gz (make changelog-fold and set-version do it)")
	}
}
