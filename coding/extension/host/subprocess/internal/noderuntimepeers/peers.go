// Package noderuntimepeers generates the host peer packages of the Node runtime archive: package.json and entry modules under runtime-node/harness/node_modules. The
// generator writes them into the archive and the archive test recomputes them, so both use this one table.
package noderuntimepeers

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess/internal/noderuntimepack"
)

// hostPeers are the packages Pi's own package.json lists as dependencies of @earendil-works/pi-coding-agent, with the subpaths an extension (or a helper process it starts) imports
// and the runtime module that serves each. The harness package (runtime-node/harness) is the package a Node extension's process.argv[1] belongs to, named like Pi's CLI package;
// code that locates the Pi host from it (nicobailon/pi-subagents' detached runner) resolves these peers from harness/node_modules, as it does next to Pi's own install. The packages are
// generated into the runtime archive from this table; their entries re-export the runtime modules extensions import in process, so both routes share one implementation.
var hostPeers = map[string]map[string]string{
	"@earendil-works/pi-agent-core": {".": "shims/pi-agent-core.mjs"},
	// Pi's pi-ai exports its dist subpaths with patterns ("./providers/*": "./dist/providers/*.js"): a "*" key expands to every module the runtime has under the target pattern.
	"@earendil-works/pi-ai": {
		".": "shims/pi-ai.mjs", "./compat": "shims/pi-ai.mjs", "./oauth": "shims/pi-ai-oauth.mjs",
		"./models": "shims/pi-ai-models.mjs", "./bedrock-provider": "shims/pi-dist/pi-ai/bedrock-provider.js", "./bun-oauth": "shims/pi-dist/pi-ai/bun-oauth.js",
		"./providers/*": "shims/pi-dist/pi-ai/providers/*.js", "./api/*": "shims/pi-dist/pi-ai/api/*.js", "./utils/*": "shims/pi-dist/pi-ai/utils/*.js",
	},
	"@earendil-works/pi-tui": {".": "shims/pi-tui.mjs"},
	"@earendil-works/pi-codemode": {
		".": "shims/pi-dist/pi-codemode/index.js", "./declarations": "shims/pi-dist/pi-codemode/declarations.js", "./source": "shims/pi-dist/pi-codemode/source.js",
		"./worker": "shims/pi-dist/pi-codemode/runtime/worker.js",
	},
	"@earendil-works/pi-mcp": {".": "shims/pi-dist/pi-mcp/index.js", "./oauth": "shims/pi-dist/pi-mcp/oauth/index.js"},
	"typebox":                {".": "shims/typebox.mjs", "./compile": "shims/typebox-compile.mjs", "./value": "shims/typebox-value.mjs"},
}

// exportsDefault matches an ES module that has a default export: `export default ...`, or `x as default` in an export list (esbuild's bundles).
var exportsDefault = regexp.MustCompile(`(?m)^\s*export\s+default\b|\bas\s+default\s*[,}\n]`)

// Targets returns every runtime module the host peers re-export, in sorted order; a pattern target keeps its "*". A runtime tree the generator accepts has each plain target, and
// the generator expands each pattern over the modules the tree has under it.
func Targets() []string {
	var targets []string
	for _, table := range hostPeers {
		for _, target := range table {
			targets = append(targets, target)
		}
	}
	sort.Strings(targets)
	return targets
}

// HostPeerFiles returns the generated package.json and entry modules of every host peer, under runtime-node/harness/node_modules. sources maps each runtime path (relative to
// runtime-node) to its bytes. An entry re-exports its module's named exports and, when the module has one, its default export, which `export *` does not carry.
func HostPeerFiles(version string, sources map[string][]byte) ([]noderuntimepack.File, error) {
	exists := func(runtimePath string) bool { _, ok := sources[runtimePath]; return ok }
	var files []noderuntimepack.File
	names := make([]string, 0, len(hostPeers))
	for name := range hostPeers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dir := "harness/node_modules/" + name
		up := strings.Repeat("../", 2+strings.Count(name, "/")+1)
		table := expandPatterns(hostPeers[name], sources)
		subpaths := make([]string, 0, len(table))
		for subpath := range table {
			subpaths = append(subpaths, subpath)
		}
		sort.Strings(subpaths)
		var exports strings.Builder
		for i, subpath := range subpaths {
			target := table[subpath]
			if !exists(target) {
				return nil, fmt.Errorf("host peer %s %s: %s is not in the runtime", name, subpath, target)
			}
			entry := fmt.Sprintf("entry-%d.mjs", i)               //perf:ok build-time peer codegen, runs once per runtime pack
			data := fmt.Sprintf("export * from %q;\n", up+target) //perf:ok build-time peer codegen, runs once per runtime pack
			if exportsDefault.Match(sources[target]) {
				data += fmt.Sprintf("export { default } from %q;\n", up+target) //perf:ok build-time peer codegen, runs once per runtime pack
			}
			files = append(files, noderuntimepack.File{Name: "runtime-node/" + path.Join(dir, entry), Data: []byte(data)})
			if i > 0 {
				exports.WriteString(", ") //perf:ok build-time peer codegen, runs once per runtime pack
			}
			fmt.Fprintf(&exports, "%q: %q", subpath, "./"+entry) //perf:ok build-time peer codegen, runs once per runtime pack
		}
		manifest := fmt.Sprintf("{\n  \"name\": %q,\n  \"version\": %q,\n  \"private\": true,\n  \"type\": \"module\",\n  \"exports\": {%s}\n}\n", name, version, exports.String()) //perf:ok build-time peer codegen, runs once per runtime pack
		files = append(files, noderuntimepack.File{Name: "runtime-node/" + path.Join(dir, "package.json"), Data: []byte(manifest)})
	}
	return files, nil
}

// expandPatterns replaces each "./dir/*" key by one key per module the runtime has under the matching target ("shims/.../dir/*.js"), as Node's own pattern exports match files.
func expandPatterns(table map[string]string, sources map[string][]byte) map[string]string {
	out := map[string]string{}
	for subpath, target := range table {
		before0, after0, ok := strings.Cut(subpath, "*")
		if !ok {
			out[subpath] = target
			continue
		}
		targetStar := strings.Index(target, "*")
		before, after := target[:targetStar], target[targetStar+1:]
		for file := range sources {
			if strings.HasPrefix(file, before) && strings.HasSuffix(file, after) && len(file) > len(before)+len(after) {
				match := file[len(before) : len(file)-len(after)]
				out[before0+match+after0] = file
			}
		}
	}
	return out
}
