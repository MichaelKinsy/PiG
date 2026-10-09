//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's published CLI enables Node's source-validated compile cache before loading its bundle. PiG enables it at virtual-library import, after the first Jiti transform, and preserves source edits even when file size/mtime are unchanged.
func TestNodeCompileCacheStartsAtLibraryImportAndChecksSourceBytes(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	dir := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(dir, "pig"))
	t.Setenv("NODE_DISABLE_COMPILE_CACHE", "")
	if err := os.Unsetenv("NODE_DISABLE_COMPILE_CACHE"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_COMPILE_CACHE", "")
	t.Setenv("TMPDIR", t.TempDir())
	entry := filepath.Join(dir, "entry.ts")
	write(t, entry, `import {Text} from "@earendil-works/pi-tui"; export default () => new Text("cache",0,0).render(5);`)
	native := filepath.Join(dir, "native.mjs")
	write(t, native, `export default "first";`)
	script := `import assert from "node:assert/strict";
import {registerHooks,getCompileCacheDir,flushCompileCache} from "node:module";
import {pathToFileURL} from "node:url";
import {readdirSync} from "node:fs";
import {join} from "node:path";
let babel=false;
registerHooks({load(url,context,next){
 if(url.endsWith("/jiti/dist/babel.cjs")){assert.equal(getCompileCacheDir(),undefined,"Babel bytecode cache generation moved onto the cold path");babel=true;}
 return next(url,context);
}});
const {importExtension}=await import(pathToFileURL(join(process.argv[1],"jiti-loader.mjs")));
assert.equal(getCompileCacheDir(),undefined);
assert.deepEqual((await importExtension(process.argv[2]))(),["cache"]);
assert.equal(babel,process.argv[4]==="first","Jiti transform cache reuse changed");
assert.ok(getCompileCacheDir()?.startsWith(join(process.env.PIG_HOME,"cache","node-compile")));
assert.equal((await import(pathToFileURL(process.argv[3]))).default,process.argv[4]);
flushCompileCache();
assert.ok(readdirSync(getCompileCacheDir()).length>0,"no bytecode persisted");
`
	run := func(value string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "node", "--import", registerLoaderURL(t, root), "--input-type=module", "-e", script, root, entry, native, value)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("native cache: %v\n%s", err, output)
		}
	}
	run("first")
	info, err := os.Stat(native)
	if err != nil {
		t.Fatal(err)
	}
	write(t, native, `export default "later";`)
	if err := os.Chtimes(native, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	run("later")
}

func TestNodeReadyPublishesNativeCompileCache(t *testing.T) {
	for _, disabled := range []string{"", "1"} {
		t.Run("disabled="+disabled, func(t *testing.T) {
			t.Setenv("NODE_DISABLE_COMPILE_CACHE", disabled)
			if disabled == "" {
				if err := os.Unsetenv("NODE_DISABLE_COMPILE_CACHE"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("NODE_COMPILE_CACHE", "")
			root := t.TempDir()
			t.Setenv("PIG_HOME", filepath.Join(root, "pig"))
			report := filepath.Join(root, "report.json")
			t.Setenv("COMPILE_CACHE_REPORT", report)
			entry := filepath.Join(root, "cache.ts")
			write(t, entry, `import {Text} from "@earendil-works/pi-tui";
import {getCompileCacheDir} from "node:module";
import {readdirSync,writeFileSync} from "node:fs";
export default function(pi:any) {
 if(typeof Text!=="function")throw new Error("missing Text");
 pi.registerCommand("cache",{handler:()=>{
  const dir=getCompileCacheDir();
  writeFileSync(process.env.COMPILE_CACHE_REPORT,JSON.stringify({directory:dir??null,files:dir?readdirSync(dir):[]}));
 }});
}`)
			host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			t.Cleanup(func() { host.Shutdown("test done") })
			loaded, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "cache", Source: entry, Enabled: true}})
			if len(errs) != 0 || len(loaded) != 1 {
				t.Fatalf("load = %v, %v", loaded, errs)
			}
			if err := loaded[0].Commands["cache"].Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(report)
			if err != nil {
				t.Fatal(err)
			}
			var state struct {
				Directory *string  `json:"directory"`
				Files     []string `json:"files"`
			}
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if disabled == "1" {
				if state.Directory != nil || len(state.Files) != 0 {
					t.Fatalf("disabled cache was activated: %s", data)
				}
			} else if state.Directory == nil || len(state.Files) == 0 {
				t.Fatalf("ready did not publish compiler artifacts: %s", data)
			}
		})
	}
}
