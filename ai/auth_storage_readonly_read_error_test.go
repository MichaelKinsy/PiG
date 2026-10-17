package ai

import (
	"os"
	"path/filepath"
	"testing"
)

// auth-storage.ts ReadOnlyAuthStorage.load reports a readFileSync failure other than ENOENT and a JSON.parse failure as
// `Failed to read auth.json: ${error.message}`, with Node's errno text and V8's SyntaxError text.
func TestReadOnlyAuthStorageReportsReadAndParseErrorsWithNodeMessages(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, path, content, want string
	}{
		// node -e 'require("fs").readFileSync(dir, "utf-8")' throws this message.
		{"directory", dir, "", "Failed to read auth.json: EISDIR: illegal operation on a directory, read"},
		{"empty", filepath.Join(dir, "empty.json"), "", "Failed to read auth.json: Unexpected end of JSON input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.path != dir {
				if err := os.WriteFile(tc.path, []byte(tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := NewReadOnlyAuthStorage(tc.path).Read(t.Context(), "openai")
			if err == nil || err.Error() != tc.want {
				t.Fatalf("Read error = %v, want %q", err, tc.want)
			}
		})
	}
}
