package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTrustFile (trust-manager.ts:126-135) sorts the keys with Array.prototype.sort, which compares UTF-16 code units,
// and writes JSON.stringify(sorted, null, 2) + "\n", which leaves <, > and & unescaped. Node, for "/p/！": null and
// "/p/😀": false with "/p/a&b" set to true, writes "/p/a&b", "/p/😀", "/p/！" in that order. Go's byte order puts U+FF01
// before U+1F600, and encoding/json writes "/p/a\u0026b".
func TestProjectTrustStoreWritesJSONStringifyInUTF16KeyOrder(t *testing.T) {
	dir := t.TempDir()
	trustPath := filepath.Join(dir, "trust.json")
	// Every key shares the canonical prefix, so the suffixes decide the order on every platform.
	base := normalizeTrustCwd("/p")
	emoji, fullwidth, amp := jsonStringBody(filepath.Join(base, "\U0001F600")), jsonStringBody(filepath.Join(base, "\uff01")), filepath.Join(base, "a&b")
	if err := os.WriteFile(trustPath, []byte(`{"`+fullwidth+`":null,"`+emoji+`":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewProjectTrustStore(dir).Set(amp, new(true)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"" + jsonStringBody(normalizeTrustCwd(amp)) + "\": true,\n  \"" + emoji + "\": false,\n  \"" + fullwidth + "\": null\n}\n"
	if string(got) != want {
		t.Fatalf("trust.json = %q\nwant %q", got, want)
	}
}

// jsonStringBody is s as the body of a JSON string literal; it escapes only the Windows separator.
func jsonStringBody(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }
