//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi's published CLI enables Node's source-validated compile cache before loading its bundle. PiG enables it at virtual-library import, after the first Jiti transform, while its cache directory is empty or missing, and at process start once an earlier run has populated it; either way it preserves source edits even when file size/mtime are unchanged.
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
const populated=process.argv[4]!=="first";
assert.equal(getCompileCacheDir()!==undefined,populated,"the cache starts with the runtime exactly when an earlier run populated it");
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

// The cache now starts before the runtime's own modules compile, so a bad entry sits behind every start. Node rejects an entry that is corrupt or was written by another Node
// version, recompiles from source and rewrites it; this pins that the extension still registers with the same payload, the process exits 0 and writes no more to stderr than
// a clean warm start, and that the next start reads the rewritten entries (it rewrites nothing).
func TestNodeCompileCacheSurvivesCorruptAndForeignEntries(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	dir := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(dir, "pig"))
	if err := os.Unsetenv("NODE_DISABLE_COMPILE_CACHE"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_COMPILE_CACHE", "")
	t.Setenv("TMPDIR", t.TempDir())
	entry := filepath.Join(dir, "entry.ts")
	write(t, entry, `import {Text} from "@earendil-works/pi-tui";
export default function(pi:any) {
 if(typeof Text!=="function")throw new Error("missing Text");
 pi.registerCommand("cache",{description:"cache probe",handler:()=>{}});
 pi.registerFlag("cache-flag",{description:"flag",type:"boolean"});
}`)
	script := `import {flushCompileCache} from "node:module";
import {pathToFileURL} from "node:url";
import {join} from "node:path";
const {importExtension}=await import(pathToFileURL(join(process.argv[1],"jiti-loader.mjs")));
const calls=[];
const pi=new Proxy({},{get:(_,name)=>(...args)=>{calls.push([String(name),JSON.stringify(args)]);}});
await (await importExtension(process.argv[2]))(pi);
flushCompileCache();
process.stdout.write(JSON.stringify(calls));
`
	run := func(label string) (string, string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "node", "--import", registerLoaderURL(t, root), "--input-type=module", "-e", script, root, entry)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("%s: exit %v\nstderr: %s", label, err, stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	cacheRoot := filepath.Join(dir, "pig", "cache", "node-compile")
	// entries lists every cache file under the Node version's own directories, by path.
	entries := func() map[string][]byte {
		t.Helper()
		found := map[string][]byte{}
		err := filepath.WalkDir(cacheRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, readErr := os.ReadFile(path)
			found[path] = data
			return readErr
		})
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	run("populate")
	cleanOut, cleanErr := run("clean warm start")
	if !strings.Contains(cleanOut, "registerCommand") {
		t.Fatalf("the clean run registered nothing: %s", cleanOut)
	}
	warm := entries()
	if len(warm) == 0 {
		t.Fatal("a real run left no bytecode entries")
	}
	header := map[string]bool{}
	for _, data := range warm {
		if len(data) < 4 {
			t.Fatalf("entry shorter than its header: %d bytes", len(data))
		}
		header[string(data[:4])] = true
	}
	if len(header) != 1 {
		t.Fatalf("entries carry %d different headers, want one magic number", len(header))
	}
	for path := range warm {
		if err := os.WriteFile(path, []byte("garbage that is not a compile cache entry"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	foreign := filepath.Join(cacheRoot, "v22.1.0-x64-deadbeef-1000")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(foreign, "abcd1234"), "foreign entry")
	badOut, badErr := run("start over corrupt and foreign entries")
	if badOut != cleanOut || badErr != cleanErr {
		t.Fatalf("a start over bad entries differs from the clean warm start\nstdout %q want %q\nstderr %q want %q", badOut, cleanOut, badErr, cleanErr)
	}
	rewritten := entries()
	for path := range warm {
		data := rewritten[path]
		if len(data) < 4 || !header[string(data[:4])] {
			t.Fatalf("%s was not rewritten with a valid entry: %q", path, data)
		}
	}
	if got := string(rewritten[filepath.Join(foreign, "abcd1234")]); got != "foreign entry" {
		t.Fatalf("the foreign version's directory was touched: %q", got)
	}
	againOut, againErr := run("start over the rewritten entries")
	if againOut != cleanOut || againErr != cleanErr {
		t.Fatalf("the start after the rewrite differs from the clean warm start\nstdout %q want %q\nstderr %q want %q", againOut, cleanOut, againErr, cleanErr)
	}
	for path, data := range entries() {
		if !bytes.Equal(data, rewritten[path]) {
			t.Fatalf("%s changed on a start that should read valid entries: the rewrite did not leave a warm cache", path)
		}
	}
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

// pig additive (D20): the bytecode flush is a synchronous pass over every module the runtime compiled, so a Node extension makes it before its register frame, not after the host's ready message. Once registered the host sends requests under the renderer inactivity lease (D56), and a flush that follows ready holds the first request, a Markdown transform or a render, until the flush ends: 4.6 s on a starved machine, which is most of the 5 s lease. The register frame is where the host first learns of the extension, so the artifacts must exist when it is read.
func TestNodeFlushesCompileCacheBeforeItRegisters(t *testing.T) {
	t.Setenv("NODE_DISABLE_COMPILE_CACHE", "")
	if err := os.Unsetenv("NODE_DISABLE_COMPILE_CACHE"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NODE_COMPILE_CACHE", "")
	root := t.TempDir()
	pigHome := filepath.Join(root, "pig")
	t.Setenv("PIG_HOME", pigHome)
	entry := filepath.Join(root, "cache.ts")
	write(t, entry, `import {Text} from "@earendil-works/pi-tui";
export default function(pi:any) {
 if(typeof Text!=="function")throw new Error("missing Text");
 pi.registerCommand("cache",{handler:()=>{}});
}`)
	published := make(chan int, 1)
	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	host.SetInboundObserver(func(_ string, env *Envelope) {
		if env.Type != MsgRegister {
			return
		}
		files := 0
		_ = filepath.WalkDir(filepath.Join(pigHome, "cache", "node-compile"), func(_ string, entry os.DirEntry, err error) error {
			if err == nil && entry.Type().IsRegular() {
				files++
			}
			return nil
		})
		select {
		case published <- files:
		default:
		}
	})
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "cache", Source: entry, Enabled: true}})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("load = %v, %v", loaded, errs)
	}
	if files := <-published; files == 0 {
		t.Fatal("the extension registered before it flushed its compiled bytecode: the first request waits for the flush")
	}
}
