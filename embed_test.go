package pig

import (
	"os"
	"testing"
)

func TestBundledChangelogFrameMatchesSource(t *testing.T) {
	want, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if Changelog() != string(want) {
		t.Fatal("CHANGELOG.md.zst does not decode to exactly CHANGELOG.md; run: go run ./automation/gen/zstdfile CHANGELOG.md CHANGELOG.md.zst (make changelog-fold and set-version do it)")
	}
}
