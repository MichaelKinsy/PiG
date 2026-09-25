package codingagent

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// The compaction page states three defaults and a trigger condition. A reader
// tunes reserveTokens from that page, so a stale number sends them to change a
// value that is already what they wanted.

const compactionDocPath = "../../internal/pigdocs/content/compaction.md"

func compactionDoc(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(compactionDocPath))
	if err != nil {
		t.Fatalf("read the compaction page: %v", err)
	}
	return string(data)
}

func TestDocumentedCompactionDefaultsMatchTheCode(t *testing.T) {
	doc := compactionDoc(t)
	rowRE := regexp.MustCompile("(?m)^\\| `compaction\\.(\\w+)` \\| `([^`]+)` \\|")
	found := map[string]string{}
	for _, m := range rowRE.FindAllStringSubmatch(doc, -1) {
		found[m[1]] = m[2]
	}

	want := map[string]string{
		"enabled":          strconv.FormatBool(defaultCompactionConfig.Enabled),
		"reserveTokens":    strconv.Itoa(defaultCompactionConfig.ReserveTokens),
		"keepRecentTokens": strconv.Itoa(defaultCompactionConfig.KeepRecentTokens),
	}
	for key, expected := range want {
		if got, ok := found[key]; !ok {
			t.Errorf("compaction.%s has no row on the page", key)
		} else if got != expected {
			t.Errorf("compaction.%s: page says %s, code says %s", key, got, expected)
		}
	}
	if len(found) != len(want) {
		t.Errorf("page documents %d compaction keys, code has %d", len(found), len(want))
	}
}
