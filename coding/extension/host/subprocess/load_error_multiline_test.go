package subprocess

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStderrCauseKeepsMultilinePackedSyntaxErrorOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stderr.log")
	log := `extension "syntax" failed to load: /extensions/syntax.ts:2
export default function(pi) {
  foo(, )
      ^
}

SyntaxError [ERR_INVALID_TYPESCRIPT_SYNTAX]: Expression expected
    at parseTypeScript (node:internal/modules/typescript:72:36)
extension "sibling" failed to load: /extensions/other.ts:3
bad source
SyntaxError: sibling error
(node:1) ExperimentalWarning: stripTypeScriptTypes is an experimental feature
`
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := stderrCause(path, "syntax"); got != "SyntaxError [ERR_INVALID_TYPESCRIPT_SYNTAX]: Expression expected" {
		t.Fatalf("owned syntax error=%q", got)
	}
	if got := stderrCause(path, "sibling"); got != "SyntaxError: sibling error" {
		t.Fatalf("owned sibling error=%q", got)
	}
}
