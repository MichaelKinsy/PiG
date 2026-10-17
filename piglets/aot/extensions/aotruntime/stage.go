package aotruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Member is one extension of an extension set, as PiG records it in a build request.
type Member struct {
	Name  string `json:"name"`
	Entry string `json:"entry"`
}

// Request is an extension set PiG ran without a compiled host: <runtime dir>/requests/<key>.json.
type Request struct {
	Key         string   `json:"key"`
	Members     []Member `json:"members"`
	RuntimeTree string   `json:"runtimeTree"`
}

// stage is a directory the compiler builds from: a copy of the runtime tree, a tsconfig that maps the Pi package names to the tree's shims, and the
// generated entry module that starts the host with the set's extension factories compiled in.
type stage struct {
	dir string
}

// stageDirs are the names inside a stage.
const (
	stageTree   = "runtime"
	stageEntry  = "aot-entry.mjs"
	stageConfig = "tsconfig.json"
)

// stageRuntime copies the runtime tree into dir/runtime, linking files where the filesystem allows it, and rewrites the declarations that
// would stop the ahead-of-time compiler (splitLargeLiterals).
func stageRuntime(tree, dir string) (stage, error) {
	dst := filepath.Join(dir, stageTree)
	err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(tree, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			link, linkErr := os.Readlink(path)
			if linkErr != nil {
				return linkErr
			}
			return os.Symlink(link, target)
		case !d.Type().IsRegular():
			return nil
		}
		if os.Link(path, target) == nil {
			return nil
		}
		return copyFile(path, target)
	})
	if err != nil {
		return stage{}, fmt.Errorf("stage the runtime tree: %w", err)
	}
	return stage{dir: dir}, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

// largeLiteral matches a one-line top-level declaration of a large object or array literal, as a bundler emits generated data.
var largeLiteral = regexp.MustCompile(`^(var|let|const) ([A-Za-z_$][\w$]*) = ([\[{].*[\]}]);$`)

// largeLiteralBytes is the line length from which splitLargeLiterals wraps a declaration. The compiler declines a function whose bytecode exceeds
// 262,144 bytes and one module function holds every top-level literal of a bundled chunk, so a chunk of model catalogs would stop the build.
const largeLiteralBytes = 16 << 10

// splitLargeLiterals wraps each large one-line literal declaration of the staged runtime tree's own files in an immediately invoked function. The
// declaration evaluates in the same order to the same value; each literal then compiles as a function of its own instead of as part of the chunk's
// module function. It returns the number of declarations it wrapped.
func (s stage) splitLargeLiterals() (int, error) {
	wrapped := 0
	root := filepath.Join(s.dir, stageTree)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		if ext := filepath.Ext(path); ext != ".js" && ext != ".mjs" {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() < largeLiteralBytes {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := strings.Split(string(data), "\n")
		changed := false
		for i, line := range lines {
			if len(line) < largeLiteralBytes {
				continue
			}
			if m := largeLiteral.FindStringSubmatch(line); m != nil {
				lines[i] = fmt.Sprintf("%s %s = (() => (%s))();", m[1], m[2], m[3])
				wrapped++
				changed = true
			}
		}
		if !changed {
			return nil
		}
		// The staged file may be a hard link to the runtime tree: replace it, never write through it.
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(strings.Join(lines, "\n")), info.Mode().Perm())
	})
	return wrapped, err
}

var shimEntry = regexp.MustCompile(`\["([^"]+)", new URL\("\./([^"]+)", import\.meta\.url\)\.href\]`)

// shimPaths reads the specifiers the runtime serves from its own shims (loader.mjs) and the shim file for each.
func (s stage) shimPaths() (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, stageTree, "loader.mjs"))
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for _, m := range shimEntry.FindAllStringSubmatch(string(data), -1) {
		paths[m[1]] = filepath.Join(s.dir, stageTree, filepath.FromSlash(m[2]))
	}
	if len(paths) == 0 {
		return nil, errors.New("loader.mjs lists no shim specifiers")
	}
	return paths, nil
}

// writeConfig writes the tsconfig whose paths send the Pi package names to the runtime's shims, wherever the importing file lives: an extension
// that carries its own copy of a Pi package would otherwise bundle a second Pi (duplicate classes and registries).
func (s stage) writeConfig() (string, error) {
	paths, err := s.shimPaths()
	if err != nil {
		return "", err
	}
	mapped := map[string][]string{}
	for spec, file := range paths {
		mapped[spec] = []string{file}
	}
	data, err := json.MarshalIndent(map[string]any{"compilerOptions": map[string]any{"baseUrl": s.dir, "paths": mapped}}, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, stageConfig)
	return path, os.WriteFile(path, data, 0o644)
}

var moduleList = regexp.MustCompile(`(?s)export const (RUNTIME_REQUIRES|RUNTIME_SOURCES|RUNTIME_WORKERS) = Object\.freeze\(\[(.*?)\]\)`)

// runtimeModules are the specifiers runtime-modules.mjs lists for a bundling host to serve.
type runtimeModules struct {
	requires, sources, workers []string
}

func (s stage) runtimeModules() (runtimeModules, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, stageTree, "runtime-modules.mjs"))
	if err != nil {
		return runtimeModules{}, err
	}
	var lists runtimeModules
	for _, m := range moduleList.FindAllStringSubmatch(string(data), -1) {
		var items []string
		for _, quoted := range regexp.MustCompile(`"[^"]*"`).FindAllString(m[2], -1) {
			item, err := strconv.Unquote(quoted)
			if err != nil {
				return runtimeModules{}, err
			}
			items = append(items, item)
		}
		switch m[1] {
		case "RUNTIME_REQUIRES":
			lists.requires = items
		case "RUNTIME_SOURCES":
			lists.sources = items
		case "RUNTIME_WORKERS":
			lists.workers = items
		}
	}
	if len(lists.requires) == 0 || len(lists.workers) == 0 {
		return runtimeModules{}, errors.New("runtime-modules.mjs lists no modules")
	}
	return lists, nil
}

// notCompiledIn are the listed requires a compiled host serves nothing for: jiti loads extensions from source at run time, and a host that compiled its
// extensions in has no such extension. Leaving jiti out keeps its transform of 1.5 MB of generated code out of the image.
var notCompiledIn = map[string]bool{
	"./shims/jiti/lib/jiti.cjs": true,
	"./shims/jiti/package.json": true,
}

// writeEntry writes the entry module of the compiled host and the two modules it imports first. The host starts as `<executable> cli|cell <args>`:
// aot-start.mjs answers --version, gives extensions that start `process.execPath` a Node and serves the runtime modules; aot-factories.mjs imports
// the compiled-in extensions and registers their factories, so that no extension code runs before the first two are in place; the entry then starts
// the runtime tree's cli or cell module with the arguments Node would pass it. A dynamic host compiles jiti in as well and loads any extension from source
// at run time: the conformance suite runs on it, since its extensions are not known at build time.
func (s stage) writeEntry(members []Member, version string, dynamic bool) error {
	lists, err := s.runtimeModules()
	if err != nil {
		return err
	}
	var start strings.Builder
	start.WriteString("// Generated by the PiG aot runtime Piglet. Do not edit.\n")
	fmt.Fprintf(&start, "if (process.argv[2] === \"--version\") {\n  process.stdout.write(%s + \"\\n\");\n  process.exit(0);\n}\n", strconv.Quote(version))
	start.WriteString(startNode)
	start.WriteString("const compiledRequires = {\n")
	for _, spec := range lists.requires {
		if notCompiledIn[spec] && !dynamic {
			continue
		}
		fmt.Fprintf(&start, "  %s: () => require(%s),\n", strconv.Quote(spec), strconv.Quote("./"+stageTree+"/"+strings.TrimPrefix(spec, "./")))
	}
	start.WriteString("};\nconst compiledWorkers = {\n")
	for _, spec := range lists.workers {
		fmt.Fprintf(&start, "  %s: %s,\n", strconv.Quote(spec), strconv.Quote("./"+stageTree+"/"+strings.TrimPrefix(spec, "./")))
	}
	start.WriteString("};\nconst compiledSources = {\n")
	if dynamic {
		// Only a host that loads extensions from source reads the tree's own sources (jiti's cache identity hashes jiti-loader.mjs).
		for _, spec := range lists.sources {
			data, err := os.ReadFile(filepath.Join(s.dir, stageTree, filepath.FromSlash(strings.TrimPrefix(spec, "./"))))
			if err != nil {
				return err
			}
			fmt.Fprintf(&start, "  %s: %s,\n", strconv.Quote(spec), strconv.Quote(string(data)))
		}
	}
	start.WriteString("};\n")
	if dynamic {
		shims, err := s.virtualModuleFiles()
		if err != nil {
			return err
		}
		start.WriteString("const compiledShims = {\n")
		for _, spec := range shims {
			fmt.Fprintf(&start, "  %s: () => require(%s),\n", strconv.Quote(spec), strconv.Quote("./"+stageTree+"/"+strings.TrimPrefix(spec, "./")))
		}
		start.WriteString("};\n" + dynamicHooks)
	}
	start.WriteString(startHooks)
	if err := os.WriteFile(filepath.Join(s.dir, "aot-start.mjs"), []byte(start.String()), 0o644); err != nil {
		return err
	}

	var factories strings.Builder
	factories.WriteString("// Generated by the PiG aot runtime Piglet. Do not edit.\n")
	for i, member := range members {
		fmt.Fprintf(&factories, "import * as m%d from %s;\n", i, strconv.Quote(member.Entry))
	}
	factories.WriteString(factoryHelpers)
	factories.WriteString("globalThis[Symbol.for(\"pig.compiledFactories\")] = new Map([\n")
	for i, member := range members {
		fmt.Fprintf(&factories, "  [%s, factoryOf(m%d)],\n", strconv.Quote(member.Entry), i)
	}
	factories.WriteString("]);\n")
	if err := os.WriteFile(filepath.Join(s.dir, "aot-factories.mjs"), []byte(factories.String()), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, stageEntry), []byte(entryMain), 0o644)
}

// hostExecutable is what Pi's own modules read where they mean the executable they run from: it locates their package.json, themes, export template,
// photon wasm and native modules beside it. A compiled host names the Node on PATH as process.execPath for extensions, so the vendored Pi modules read the
// host's own path from the global aot-start.mjs sets.
const hostExecutable = "(globalThis[Symbol.for(\"pig.hostExecutable\")] ?? process.execPath)"

// patchHostExecutable makes the vendored Pi modules of the staged runtime tree read hostExecutable where they read process.execPath. It returns the
// number of files it changed.
func (s stage) patchHostExecutable() (int, error) {
	changed := 0
	root := filepath.Join(s.dir, stageTree, "shims", "pi-dist")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		if ext := filepath.Ext(path); ext != ".js" && ext != ".mjs" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		patched := strings.ReplaceAll(string(data), "process.execPath", hostExecutable)
		if patched == string(data) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		changed++
		return os.WriteFile(path, []byte(patched), info.Mode().Perm())
	})
	return changed, err
}

// assetCopies are the files Pi's own modules read beside the executable when they run from one (config.js getPackageDir and its callers), as the
// runtime tree holds them: source relative to the tree's shims, destination relative to the executable's directory.
var assetCopies = [][2]string{
	{"pi-dist/pi-coding-agent/package.json", "package.json"},
	{"pi-dist/pi-coding-agent/README.md", "README.md"},
	{"pi-dist/pi-coding-agent/CHANGELOG.md", "CHANGELOG.md"},
	{"pi-dist/pi-coding-agent/modes/interactive/theme", "theme"},
	{"pi-dist/pi-coding-agent/modes/interactive/assets", "assets"},
	{"pi-dist/pi-coding-agent/core/export-html", "export-html"},
	{"photon-node/photon_rs_bg.wasm", "photon_rs_bg.wasm"},
}

// layoutAssets copies the assets beside the compiled host in outDir. A source the tree lacks is skipped.
func (s stage) layoutAssets(outDir, platform string) error {
	shims := filepath.Join(s.dir, stageTree, "shims")
	copies := append([][2]string(nil), assetCopies...)
	goos, _, _ := strings.Cut(platform, "-")
	if goos == "android" {
		goos = "linux"
	}
	copies = append(copies, [2]string{"pi-dist/pi-tui/native/" + goos + "/prebuilds/" + platform, "native/" + goos + "/prebuilds/" + platform})
	for _, c := range copies {
		src, dst := filepath.Join(shims, filepath.FromSlash(c[0])), filepath.Join(outDir, filepath.FromSlash(c[1]))
		info, err := os.Stat(src)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := copyFile(src, dst); err != nil {
				return err
			}
			continue
		}
		err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel, _ := filepath.Rel(src, path)
			target := filepath.Join(dst, rel)
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			if !d.Type().IsRegular() {
				return nil
			}
			return copyFile(path, target)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// startNode makes process.execPath a Node. Extensions re-launch "process.execPath <script>" and run "process.execPath -e ..." expecting Node. The
// compiled host is neither, so it names the Node on PATH, as the Node runtime would, and refuses to start without one: PiG then runs the extension
// on the next runtime.
const startNode = `
import { execFileSync } from "node:child_process";
import { accessSync, constants, statSync } from "node:fs";
import { delimiter, join } from "node:path";
{
  let found;
  for (const dir of (process.env.PATH ?? "").split(delimiter)) {
    if (!dir) continue;
    const candidate = join(dir, process.platform === "win32" ? "node.exe" : "node");
    try {
      if (statSync(candidate).isFile()) { accessSync(candidate, constants.X_OK); found = candidate; break; }
    } catch {}
  }
  if (!found) {
    process.stderr.write("pig-aot-host: no node executable on PATH, so process.execPath cannot name a Node\n");
    process.exit(87);
  }
  globalThis[Symbol.for("pig.hostExecutable")] = process.execPath;
  // An extension that reads process.execPath sees what Node reports for itself: its resolved path, which a launcher on PATH (a shim, a symlink) may differ from.
  // Asking Node costs a process start, so it happens on the first read.
  let execPath;
  const nodeExecPath = () => (execPath ??= execFileSync(found, ["-p", "process.execPath"], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim());
  Object.defineProperty(process, "execPath", { get: nodeExecPath, set(value) { execPath = value; }, enumerable: true, configurable: true });
  Object.defineProperty(process.argv, 0, { get: nodeExecPath, set(value) { execPath = value; }, enumerable: true, configurable: true });
}
`

const startHooks = `
const notCompiled = (kind, specifier) => new Error("the compiled host does not include " + kind + " " + specifier + ": its extensions are compiled in");
globalThis[Symbol.for("pig.runtimeModules")] = {
  require(specifier) {
    const load = compiledRequires[specifier];
    if (!load) throw notCompiled("module", specifier);
    return load();
  },
  source(specifier) {
    if (!(specifier in compiledSources)) throw notCompiled("source", specifier);
    return compiledSources[specifier];
  },
  worker(specifier) {
    const worker = compiledWorkers[specifier];
    if (!worker) throw notCompiled("worker", specifier);
    return worker;
  },
};
`

// factoryHelpers turn a compiled module into the value jiti would return for an extension: its default export.
const factoryHelpers = `
function factoryOf(ns) {
  let value = ns.default !== undefined ? ns.default : ns;
  if (value && typeof value === "object" && value.__esModule && value.default !== undefined) value = value.default;
  return value;
}
`

const entryMain = `// Generated by the PiG aot runtime Piglet. Do not edit.
import "./aot-start.mjs";
import "./aot-factories.mjs";
const mode = process.argv[2];
process.argv.splice(2, 1);
if (mode === "cell" || mode === "cell.mjs") await import("./runtime/cell.mjs");
else if (mode === "cli" || mode === "cli.mjs") await import("./runtime/cli.mjs");
else {
  process.stderr.write("usage: <executable> cli|cell <args>\n");
  process.exit(2);
}
`

// jitiVirtualRequire is how the runtime tree's jiti loader reaches the module behind a virtual specifier such as @earendil-works/pi-tui: by the path of a
// file in the tree, which a bundled host does not have.
const jitiVirtualRequire = "return require(fileURLToPath(url));"

// dynamicHooks serves those modules to a dynamic host from the ones it bundled.
const dynamicHooks = `globalThis[Symbol.for("pig.shimRequire")] = (path) => {
  const spec = "./shims/" + path.split("/shims/").pop();
  const load = compiledShims[spec];
  if (!load) throw new Error("the compiled host does not include module " + spec);
  return load();
};
`

var virtualModuleFile = regexp.MustCompile(`new URL\("(\./shims/[^"]+)", import\.meta\.url\)`)

// virtualModuleFiles lists the files of the tree that loader.mjs maps virtual module specifiers to.
func (s stage) virtualModuleFiles() ([]string, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, stageTree, "loader.mjs"))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, m := range virtualModuleFile.FindAllStringSubmatch(string(data), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			files = append(files, m[1])
		}
	}
	if len(files) == 0 {
		return nil, errors.New("loader.mjs maps no virtual modules")
	}
	slices.Sort(files)
	return files, nil
}

// patchVirtualModules makes the staged jiti loader reach virtual modules through the dynamic host's hook.
func (s stage) patchVirtualModules() error {
	path := filepath.Join(s.dir, stageTree, "jiti-loader.mjs")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), jitiVirtualRequire) {
		return errors.New("jiti-loader.mjs does not load virtual modules the way the dynamic host expects")
	}
	patched := strings.Replace(string(data), jitiVirtualRequire, `return globalThis[Symbol.for("pig.shimRequire")](fileURLToPath(url));`, 1)
	// The staged file may be a hard link to the source tree's: unlink it so that the write cannot reach the source.
	if err := os.Remove(path); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(patched), 0o644)
}
