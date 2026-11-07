package boltruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSource(t *testing.T, dir, name, source string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A file that finds data beside itself is no longer refused: the build compiles it from a copy that says where it is.
func TestScanPathSensitiveRewritesFilesThatReadBesideThemselves(t *testing.T) {
	dir := t.TempDir()
	plain := writeSource(t, dir, "plain.ts", `export default (pi) => { pi.registerCommand("x", { handler: async () => {} }); };`)
	locale := writeSource(t, dir, "locale.ts", "import { readFileSync } from \"node:fs\";\nconst data = readFileSync(new URL(\"./locales/de.json\", import.meta.url), \"utf8\");")
	cjs := writeSource(t, dir, "cjs.cjs", `const fs = require("fs"); module.exports = fs.readFileSync(__dirname + "/data.txt");`)
	sibling := writeSource(t, dir, "sibling.mjs", "import { readFileSync } from \"node:fs\";\nimport { dirname, join } from \"node:path\";\nimport { fileURLToPath } from \"node:url\";\nconst here = dirname(fileURLToPath(import.meta.url));\nexport const skills = readFileSync(join(here, \"..\", \"skills\", \"a.md\"));")
	dirnameOnly := writeSource(t, dir, "dirname.ts", `const here = __dirname; export const name = here.length;`)
	data := writeSource(t, dir, "data.json", `{"x": "__dirname readFileSync("}`)
	types := writeSource(t, dir, "types.d.ts", "declare const meta: ImportMeta; declare function readFileSync(): void; import.meta.url;")

	plan, err := scanPathSensitive([]string{plain, locale, cjs, sibling, dirnameOnly, data, types})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Finding != nil {
		t.Fatalf("a file whose location the rewrite pins was refused: %v", plan.Finding)
	}
	for file, want := range map[string]string{
		locale:      `new URL("./locales/de.json", "file://` + filepath.ToSlash(dir) + `/locale.ts")`,
		cjs:         `fs.readFileSync("` + filepath.ToSlash(dir) + `" + "/data.txt")`,
		sibling:     `dirname(fileURLToPath("file://` + filepath.ToSlash(dir) + `/sibling.mjs"))`,
		dirnameOnly: `const here = "` + filepath.ToSlash(dir) + `";`,
	} {
		if got := plan.Rewrites[file]; !strings.Contains(got, want) {
			t.Errorf("rewrite of %s = %q, want it to contain %q", file, got, want)
		}
	}
	if len(plan.Rewrites) != 4 {
		t.Errorf("%d rewrites, want 4 (the plain file, the data file and the declarations stay as they are): %v", len(plan.Rewrites), plan.Rewrites)
	}
}

// What stays refused: a file that names its location in a way the rewrite cannot pin to the installed file, and reads files.
func TestScanPathSensitiveRefusesWhatTheRewriteCannotPin(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		name, source, marker string
	}{
		"import.meta as a value":          {"value.ts", "import { readFileSync } from \"node:fs\";\nconst meta = import.meta;\nexport const t = readFileSync(new URL(\"./t.txt\", meta.url));", "import.meta"},
		"import.meta computed member":     {"computed.ts", "import { readFileSync } from \"node:fs\";\nexport const t = readFileSync(new URL(\"./t.txt\", import.meta[\"url\"]));", "import.meta"},
		"import.meta optional chain":      {"optional.mjs", "import { existsSync } from \"node:fs\";\nexport const t = existsSync(new URL(\"./t.txt\", import.meta?.url));", "import.meta"},
		"import.meta passed to a call":    {"passed.ts", "import { spawn } from \"node:child_process\";\nexport const t = spawn(find(import.meta), []);", "import.meta"},
		"a worker started by import.meta": {"worker.ts", "export const w = new Worker(resolve(import.meta), {});", "import.meta"},
	}
	for name, c := range cases {
		path := writeSource(t, dir, c.name, c.source)
		plan, err := scanPathSensitive([]string{path})
		if err != nil || plan.Finding == nil || plan.Finding.File != path || plan.Finding.Marker != c.marker {
			t.Errorf("%s: plan %+v err %v", name, plan, err)
		}
	}
	// The same names without a read of the file system are harmless, and so is a mention in a comment or a string.
	quiet := writeSource(t, dir, "quiet.ts", "const meta = import.meta; // readFileSync(import.meta)\nexport const s = \"readFileSync(\"; export const m = meta;")
	if plan, err := scanPathSensitive([]string{quiet}); err != nil || plan.Finding != nil {
		t.Errorf("a file that reads nothing was refused: %+v %v", plan.Finding, err)
	}
	// A source that is not UTF-8 cannot be rewritten without changing its bytes.
	binary := writeSource(t, dir, "latin1.js", "const here = __dirname; // \xe9\n")
	plan, err := scanPathSensitive([]string{binary})
	if err != nil || plan.Finding == nil || plan.Finding.Marker != "a source that is not UTF-8" {
		t.Errorf("a source that is not UTF-8: %+v %v", plan, err)
	}
	// The finding names the file and the marker, as the status line shows them.
	if got := (pathFinding{File: "/e/x.ts", Marker: "import.meta"}).String(); !strings.Contains(got, "/e/x.ts") || !strings.Contains(got, "import.meta") {
		t.Errorf("String() = %q", got)
	}
}
