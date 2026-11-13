package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// A trust store of thirty keys, more than the twelve an insertion sort takes, is written in UTF-16 code unit order,
// whatever order the file held them in. The keys of a map are distinct; the want is package slices' own sort.
func TestProjectTrustStoreWritesManyKeysInUTF16Order(t *testing.T) {
	dir := t.TempDir()
	trustPath := filepath.Join(dir, "trust.json")
	base := normalizeTrustCwd("/p")
	var keys, members []string
	for i := range 29 {
		// 11 and 29 share no factor: every name once, in a scattered order. U+FF01 sorts after U+1F600 in UTF-16 and before it in UTF-8.
		name := string(rune('a'+i*11%29%26)) + string(rune('0'+i*11%29/26))
		switch i % 3 {
		case 1:
			name += "\uff01"
		case 2:
			name += "\U0001F600"
		}
		key := filepath.Join(base, name)
		keys = append(keys, key)
		members = append(members, `"`+jsonStringBody(key)+`":null`)
	}
	if err := os.WriteFile(trustPath, []byte("{"+strings.Join(members, ",")+"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(base, "m&")
	if err := NewProjectTrustStore(dir).Set(added, new(true)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	keys = append(keys, normalizeTrustCwd(added))
	slices.SortFunc(keys, func(a, b string) int { return slices.Compare(jsstring.ToUTF16(a), jsstring.ToUTF16(b)) })
	var lines []string
	for _, key := range keys {
		value := "null"
		if key == normalizeTrustCwd(added) {
			value = "true"
		}
		lines = append(lines, `  "`+jsonStringBody(key)+`": `+value)
	}
	if want := "{\n" + strings.Join(lines, ",\n") + "\n}\n"; string(got) != want {
		t.Fatalf("trust.json = %q\nwant %q", got, want)
	}
}
