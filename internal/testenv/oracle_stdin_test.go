package testenv

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const oracleStdinLoop = "for await (const chunk of process.stdin)"

var oracleStdinUTF8 = regexp.MustCompile(`process\.stdin\.setEncoding\(["']utf-?8["']\)`)

// Node oracle scripts read the probe JSON with `for await (const chunk of process.stdin) input += chunk`. Without a UTF-8 stdin encoding each
// Buffer chunk is decoded on its own, so a multi-byte character split across a pipe read becomes U+FFFD on the Pi side, and the oracle
// comparison fails only when the pipe happens to split there.
func TestOracleScriptsDecodeStdinAsUTF8(t *testing.T) {
	root := ModuleRoot(t)
	var missing []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".upstream", ".git", "build", "target":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".mjs", ".js", ".ts":
		default:
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		loop := strings.Index(text, oracleStdinLoop)
		if loop < 0 {
			return nil
		}
		if at := oracleStdinUTF8.FindStringIndex(text); at == nil || at[0] > loop {
			rel, _ := filepath.Rel(root, path)
			missing = append(missing, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Fatalf("%d scripts read stdin chunks without process.stdin.setEncoding(\"utf8\") first:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}
