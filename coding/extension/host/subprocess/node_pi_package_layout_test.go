//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// An extension that locates a Pi package on disk resolves it (import.meta.resolve, or a resolve a helper
// process makes) and derives the other files from the answer: pi-open-agents 0.1.27 goes two directories up
// from the pi-coding-agent entry and appends dist/cli.js, and pi-subagents walks up to the nearest package.json
// named for the package. Those derivations are only valid when the file a specifier resolves to has Pi's layout:
// it sits under a directory whose package.json names the package, at the path Pi's own package.json gives its
// entry ("main" or the "exports" target), and the package root carries the entry points Pi's build writes next
// to it (a "bin" target's unbundled twin under dist/). The runtime serves several Pi packages from shim
// modules; this test checks every @earendil-works specifier loader.mjs serves against the pinned Pi packages,
// so a Pi package that is served from the wrong place fails here before an extension finds out.
//
// A served package that does not conform fails: there is no list of known gaps. The compatibility corpus
// (pi-open-agents 0.1.27, @henryqw/pi-subagent 28.0.1, pi-subagents 0.84.0) derives paths from the pi-coding-agent root.
// upstream: packages/*/package.json main, exports and bin; packages/coding-agent/package.json scripts.build:unbundled.
// piLayoutAliases lists the specifiers Pi's extension loader serves as one module with another
// (core/extensions/virtual-modules.ts:28-29 maps the pi-ai root and pi-ai/compat to the same extension view). They
// must resolve to the same file as the specifier they alias, because the namespace of a module is its identity.
var piLayoutAliases = map[string]string{
	"@earendil-works/pi-ai/compat": "@earendil-works/pi-ai",
}

func TestNodeServesEachPiPackageWithPisLayout(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	modRoot := findModuleRoot(t)
	runtimeRoot := filepath.Join(modRoot, "coding", "extension", "host", "subprocess", "runtime-node")
	oracleRoot := filepath.Join(modRoot, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	oracleDirs := func(pkg string) string {
		if pkg == "@earendil-works/pi-coding-agent" {
			return oracleRoot
		}
		return filepath.Join(oracleRoot, "node_modules", filepath.FromSlash(pkg))
	}

	loader, err := os.ReadFile(filepath.Join(runtimeRoot, "loader.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	var specs []string
	for _, m := range regexp.MustCompile(`(?m)^\s*\["(@earendil-works/[^"]+)", new URL\(`).FindAllStringSubmatch(string(loader), -1) {
		specs = append(specs, m[1])
	}
	if len(specs) < 5 {
		t.Fatalf("parsed %d @earendil-works specifiers from loader.mjs: %v", len(specs), specs)
	}
	encoded, _ := json.Marshal(specs)
	out, err := exec.CommandContext(t.Context(), node,
		"--import", registerLoaderURL(t, runtimeRoot), "--input-type=module",
		"-e", `const out = {}; for (const s of JSON.parse(process.argv[1])) { try { out[s] = import.meta.resolve(s); } catch (e) { out[s] = "THROWS " + (e.code ?? e.message); } } console.log(JSON.stringify(out));`,
		string(encoded)).Output()
	if err != nil {
		t.Fatalf("resolving the served specifiers: %v", err)
	}
	var resolved map[string]string
	if err := json.Unmarshal(out, &resolved); err != nil {
		t.Fatalf("resolve output %q: %v", out, err)
	}

	type manifest struct {
		Name    string          `json:"name"`
		Main    string          `json:"main"`
		Exports json.RawMessage `json:"exports"`
		Bin     json.RawMessage `json:"bin"`
	}
	readManifest := func(dir string) (manifest, bool) {
		var m manifest
		data, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err != nil || json.Unmarshal(data, &m) != nil {
			return m, false
		}
		return m, true
	}
	// entryTarget is the file Pi's package.json gives for a subpath: the "import" (else "default") condition of its exports entry, or "main" for the root of a package with no exports.
	entryTarget := func(m manifest, subpath string) string {
		var asMap map[string]json.RawMessage
		if len(m.Exports) > 0 && json.Unmarshal(m.Exports, &asMap) == nil {
			value, ok := asMap[subpath]
			star := ""
			if !ok {
				// A pattern export ("./providers/*": "./dist/providers/*.js") serves every subpath its prefix and suffix match.
				for key, v := range asMap {
					before, after, found := strings.Cut(key, "*")
					if found && strings.HasPrefix(subpath, before) && strings.HasSuffix(subpath, after) && len(subpath) > len(before)+len(after) {
						value, ok, star = v, true, subpath[len(before):len(subpath)-len(after)]
					}
				}
			}
			if !ok {
				return ""
			}
			var s string
			if json.Unmarshal(value, &s) == nil {
				return path.Clean(strings.ReplaceAll(s, "*", star))
			}
			var conditions map[string]string
			if json.Unmarshal(value, &conditions) == nil {
				for _, condition := range []string{"import", "default"} {
					if conditions[condition] != "" {
						return path.Clean(strings.ReplaceAll(conditions[condition], "*", star))
					}
				}
			}
			return ""
		}
		if subpath == "." && m.Main != "" {
			return path.Clean(m.Main)
		}
		return ""
	}
	binTargets := func(m manifest) []string {
		var one string
		if json.Unmarshal(m.Bin, &one) == nil && one != "" {
			return []string{one}
		}
		var many map[string]string
		_ = json.Unmarshal(m.Bin, &many)
		var targets []string
		for _, target := range many {
			targets = append(targets, target)
		}
		sort.Strings(targets)
		return targets
	}

	// problems maps each served package to what is wrong with its layout; a package with none conforms.
	problems := map[string][]string{}
	served := map[string]bool{}
	for _, spec := range specs {
		pkg := strings.Join(strings.SplitN(spec, "/", 3)[:2], "/")
		subpath := "." + strings.TrimPrefix(spec, pkg)
		served[pkg] = true
		url := resolved[spec]
		// import.meta.resolve answers a file URL, which names a Windows file as file:///D:/dir/file.js.
		file, err := nodeurl.FileURLToPath(url, runtime.GOOS == "windows")
		if err != nil {
			problems[pkg] = append(problems[pkg], spec+" resolves to "+url+", not a file")
			continue
		}
		if target, aliased := piLayoutAliases[spec]; aliased {
			if url != resolved[target] {
				problems[pkg] = append(problems[pkg], spec+" resolves to "+url+", Pi's loader serves it as the same module as "+target+" ("+resolved[target]+")")
			}
			continue
		}
		pi, ok := readManifest(oracleDirs(pkg))
		if !ok {
			t.Fatalf("the pinned %s has no readable package.json (run make parity-deps)", pkg)
		}
		want := entryTarget(pi, subpath)
		if want == "" {
			continue // Pi's package.json gives this subpath no entry of its own: the specifier has no layout to compare.
		}
		root := filepath.Dir(file)
		var own manifest
		for {
			if m, found := readManifest(root); found {
				own = m
				break
			}
			if root == runtimeRoot || root == filepath.Dir(root) {
				root = ""
				break
			}
			root = filepath.Dir(root)
		}
		if root == "" || own.Name != pkg {
			problems[pkg] = append(problems[pkg], spec+" resolves to "+file+", which is not under a package.json named "+pkg)
			continue
		}
		if rel, _ := filepath.Rel(root, file); filepath.ToSlash(rel) != want {
			problems[pkg] = append(problems[pkg], spec+" resolves to "+filepath.ToSlash(rel)+" of its package root, Pi's package.json names "+want)
		}
		// The build writes each bundled bin's unbundled twin next to the package entry (dist/cli.js beside dist/bundle/cli.js); an extension computes it from the root.
		if subpath == "." {
			for _, target := range binTargets(pi) {
				twin := path.Join("dist", path.Base(target))
				if _, err := os.Stat(filepath.Join(oracleDirs(pkg), filepath.FromSlash(twin))); err != nil {
					continue
				}
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(twin))); err != nil {
					problems[pkg] = append(problems[pkg], "the package root lacks "+twin+", which Pi's build writes for the bin "+target)
				}
			}
		}
	}

	for pkg := range served {
		if len(problems[pkg]) > 0 {
			t.Errorf("%s is served without Pi's package layout:\n  %s", pkg, strings.Join(problems[pkg], "\n  "))
		}
	}
}
