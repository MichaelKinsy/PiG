package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's loader.ts:496-501 disables evaluated-module caching but leaves jiti's source-validated filesystem cache enabled. A warm transform must survive a changed TMPDIR without retaining factory or dependency state.
func TestNodeJitiCachePersistsWithoutStaleSource(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	t.Setenv("JITI_FS_CACHE", "true")
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.ts")
	dependency := filepath.Join(dir, "dependency.ts")
	write(t, dependency, `export const value: string = "before";`)
	write(t, entry, `import { value } from "./dependency.ts";
export default (): string => "entry1:" + value;`)
	script := `import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import { pathToFileURL } from "node:url";
let compiled = false;
registerHooks({load(url, context, next) {
  if (url.endsWith("/jiti/dist/babel.cjs")) {
    assert.notEqual(process.argv[3], "hit", "warm startup loaded Babel instead of its persistent transform");
    compiled = true;
  }
  return next(url, context);
}});
const { importExtension } = await import(pathToFileURL(process.argv[1]));
if (process.argv[3] === "reject") {
  await assert.rejects(importExtension(process.argv[2]), /Unexpected token/);
  assert.equal(compiled, true, "invalid source must reach the compiler");
} else {
  const factory = await importExtension(process.argv[2]);
  assert.equal(factory(), process.argv[4]);
  assert.notEqual(await importExtension(process.argv[2]), factory);
  assert.equal(compiled, process.argv[3] === "miss");
}
`
	run := func(expectCache, want string) {
		t.Helper()
		t.Setenv("TMPDIR", t.TempDir())
		cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script, filepath.Join(root, "jiti-loader.mjs"), entry, expectCache, want)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cache %s: %v\n%s", expectCache, err, output)
		}
	}
	run("miss", "entry1:before")
	cache := filepath.Join(home, "cache", "jiti")
	if entries, err := os.ReadDir(cache); err != nil || len(entries) == 0 {
		t.Fatalf("persistent PiG transform cache missing: %v", err)
	}
	run("hit", "entry1:before")
	// Same-sized changes with restored timestamps must invalidate both the entry and an imported dependency.
	for _, change := range []struct{ path, source, want string }{
		{dependency, `export const value: string = "after!";`, "entry1:after!"},
		{entry, "import { value } from \"./dependency.ts\";\nexport default (): string => \"entry2:\" + value;", "entry2:after!"},
	} {
		info, err := os.Stat(change.path)
		if err != nil {
			t.Fatal(err)
		}
		write(t, change.path, change.source)
		if err := os.Chtimes(change.path, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
		run("miss", change.want)
		run("hit", change.want)
	}
	// A rejected transform must never become a successful cached factory.
	write(t, dependency, `export const value: = ;`)
	run("reject", "")
	write(t, dependency, `export const value: string = "fixed!";`)
	run("miss", "entry2:fixed!")
	run("hit", "entry2:fixed!")
	// Compiler options must not reuse an artifact produced with different settings.
	t.Setenv("JITI_SOURCE_MAPS", "true")
	run("miss", "entry2:fixed!")
	run("hit", "entry2:fixed!")
	t.Setenv("JITI_FS_CACHE", "false")
	run("miss", "entry2:fixed!")
	run("miss", "entry2:fixed!")
	t.Setenv("JITI_FS_CACHE", "true")

	// Change the loader and the pinned compiler identity independently. Neither may consume the prior release's transform, even with identical extension bytes.
	changedRoot := t.TempDir()
	for _, name := range []string{"jiti-loader.mjs", "loader.mjs", "compile-cache.mjs", "shims/pig-config.mjs", "shims/jiti/package.json", "shims/jiti/lib/jiti.cjs", "shims/jiti/dist/jiti.cjs", "shims/jiti/dist/babel.cjs"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(changedRoot, filepath.FromSlash(name)), string(data))
	}
	root = changedRoot
	loader := filepath.Join(root, "jiti-loader.mjs")
	data, err := os.ReadFile(loader)
	if err != nil {
		t.Fatal(err)
	}
	write(t, loader, string(data)+"\n// Changed runtime implementation.\n")
	run("miss", "entry2:fixed!")
	run("hit", "entry2:fixed!")
	write(t, filepath.Join(root, "shims", "jiti", "package.json"), `{"name":"jiti","version":"cache-identity-test"}`)
	run("miss", "entry2:fixed!")
	run("hit", "entry2:fixed!")
}
