package codingagent

// pi: packages/coding-agent/src/cli/file-processor.ts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi file-processor.ts:43-46,77-78 skips zero-byte files and always appends a newline after nonempty text, independently of its final character.
func TestCLIFileTextPreservesTrailingNewlines(t *testing.T) {
	for _, body := range []string{"", "notes body", "notes body\n", "notes body\r\n", "\n\n", "\ufeffnotes\n", strings.Repeat("large\n", 10000)} {
		t.Run(fmt.Sprintf("bytes-%d", len(body)), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "notes.txt")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := ProcessCLIFileArguments([]string{path}, dir)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if body != "" {
				want = "<file name=\"" + path + "\">\n" + strings.TrimPrefix(body, "\ufeff") + "\n</file>\n"
			}
			if got.Text != want {
				t.Fatalf("file text differs: got %q; want %q", got.Text, want)
			}
		})
	}
}

// Pi file-processor.ts:77 reads text with readFile(path, "utf-8"), so each maximal ill-formed UTF-8 subpart becomes one U+FFFD before stripBom
// removes a leading mark. Expectations recorded from Pi 1.1.0 processFileArguments.
func TestCLIFileTextDecodesInvalidUTF8LikePi(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"lone-bytes", "a\xffb\xc3\n", "a\ufffdb\ufffd\n"},
		{"truncated-and-surrogate", "x\xe2\x82y\xed\xa0\x80z\xf0\x9f\x98", "x\ufffdy\ufffd\ufffd\ufffdz\ufffd"},
		{"bom-then-invalid", "\ufeff\xffq", "\ufffdq"},
		{"inner-bom-kept", "a\ufeffb", "a\ufeffb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "notes.txt")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := ProcessCLIFileArguments([]string{path}, dir)
			if err != nil {
				t.Fatal(err)
			}
			if want := "<file name=\"" + path + "\">\n" + tc.want + "\n</file>\n"; got.Text != want {
				t.Fatalf("file text differs: got %q; want %q", got.Text, want)
			}
		})
	}
}
