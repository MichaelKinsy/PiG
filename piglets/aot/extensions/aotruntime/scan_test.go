package aotruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanPathSensitiveRefusesFilesThatReadBesideThemselves(t *testing.T) {
	dir := t.TempDir()
	write := func(name, source string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	plain := write("plain.ts", `export default (pi) => { pi.registerCommand("x", { handler: async () => {} }); };`)
	dirnameOnly := write("dirname.ts", `const here = __dirname; export const name = here.length;`)
	readsOnly := write("reads.ts", `import { readFileSync } from "node:fs"; export const f = () => readFileSync("/etc/hostname");`)
	locale := write("locale.ts", "import { readFileSync } from \"node:fs\";\nconst data = readFileSync(new URL(\"./locales/de.json\", import.meta.url), \"utf8\");")
	cjs := write("cjs.cjs", `const fs = require("fs"); module.exports = fs.readFileSync(__dirname + "/data.txt");`)
	data := write("data.json", `{"x": "__dirname readFileSync("}`)

	if finding, found, err := scanPathSensitive([]string{plain, dirnameOnly, readsOnly, data}); err != nil || found {
		t.Fatalf("files that do not read beside themselves were refused: %v %v", finding, err)
	}
	for name, path := range map[string]string{"import.meta.url": locale, "__dirname": cjs} {
		finding, found, err := scanPathSensitive([]string{plain, path})
		if err != nil || !found || finding.File != path || finding.Marker != name {
			t.Errorf("%s: finding %+v found=%v err=%v", name, finding, found, err)
		}
	}
}
