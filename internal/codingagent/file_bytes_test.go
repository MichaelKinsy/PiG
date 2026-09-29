package codingagent

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
