package coding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type discoverEntry struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Target  string `json:"target,omitempty"`
}

type discoverProbe struct {
	Build      []discoverEntry `json:"build"`
	CWD        string          `json:"cwd"`
	AgentDir   string          `json:"agentDir"`
	Configured []string        `json:"configured"`
}

type discoverOut struct {
	Loaded []string `json:"loaded"`
	Failed []string `json:"failed"`
}

const discoverFactory = "export default function () {}\n"

func discoverProbes() []discoverProbe {
	rng := rand.New(rand.NewPCG(11, 77))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	// No name carries a no-break space: Pi loads every path through resolvePath(normalizeUnicodeSpaces), so a discovered file whose own name has one cannot be loaded and fails; the configured-path spellings below cover the normalization Pi does intend.
	names := []string{"a", "b", "ext one", "日本", "z", "m.v2"}
	var probes []discoverProbe
	for range 300 {
		probe := discoverProbe{Build: []discoverEntry{}, CWD: "{S}/proj", AgentDir: "{S}/agent", Configured: []string{}}
		used := map[string]bool{}
		addDir := func(dir string, depth int) {
			for range rng.IntN(5) {
				name := pick(names)
				if used[dir+"/"+name] {
					continue
				}
				used[dir+"/"+name] = true
				base := dir + "/" + name
				switch rng.IntN(9) {
				case 0, 1:
					probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: base + pick([]string{".ts", ".js"}), Content: discoverFactory})
				case 2:
					probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: base + pick([]string{".txt", ".json", ".mjs", ".d.ts.map"}), Content: "x"})
				case 3:
					probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: base + "/index." + pick([]string{"ts", "js"}), Content: discoverFactory})
					if rng.IntN(3) == 0 {
						probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: base + "/index.js", Content: discoverFactory})
					}
				case 4:
					entries := []string{"main.ts", "lib/x.js", "../outside.ts", "missing.ts", "./sub/../main.ts"}
					rng.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
					entries = entries[:rng.IntN(len(entries)+1)]
					manifest, _ := json.Marshal(map[string]any{"pi": map[string]any{"extensions": entries}})
					probe.Build = append(probe.Build,
						discoverEntry{Kind: "file", Path: base + "/package.json", Content: string(manifest)},
						discoverEntry{Kind: "file", Path: base + "/main.ts", Content: discoverFactory},
						discoverEntry{Kind: "file", Path: base + "/lib/x.js", Content: discoverFactory},
						discoverEntry{Kind: "file", Path: dir + "/outside.ts", Content: discoverFactory})
					if rng.IntN(2) == 0 {
						probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: base + "/index.ts", Content: discoverFactory})
					}
				case 5:
					probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: base + "/package.json", Content: pick([]string{"{", `{"pi":{"extensions":"main.ts"}}`, `{"pi":{"extensions":[]}}`, `{"pi":null}`, "\ufeff" + `{"pi":{"extensions":["main.ts"]}}`, `{"name":"x"}`})}, discoverEntry{Kind: "file", Path: base + "/main.ts", Content: discoverFactory})
					if rng.IntN(2) == 0 {
						probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: base + "/index.js", Content: discoverFactory})
					}
				case 6:
					probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: "{S}/real/" + name + ".ts", Content: discoverFactory},
						discoverEntry{Kind: "symlink", Path: base + ".ts", Target: "{S}/real/" + name + ".ts"})
				case 7:
					probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: "{S}/realdir/" + name + "/index.ts", Content: discoverFactory},
						discoverEntry{Kind: "symlink", Path: base, Target: "{S}/realdir/" + name})
				default:
					probe.Build = append(probe.Build, discoverEntry{Kind: "symlink", Path: base + ".ts", Target: "{S}/nowhere/" + name + ".ts"})
				}
			}
		}
		addDir("{S}/proj/{CFG}/extensions", 0)
		addDir("{S}/agent/extensions", 0)
		addDir("{S}/proj/shared", 0)
		for range rng.IntN(5) {
			switch rng.IntN(9) {
			case 0:
				probe.Configured = append(probe.Configured, "{S}/proj/shared")
			case 1:
				probe.Configured = append(probe.Configured, "shared")
			case 2:
				probe.Configured = append(probe.Configured, "{S}/proj/{CFG}/extensions", "{S}/agent/extensions")
			case 3:
				probe.Configured = append(probe.Configured, "{S}/proj/direct.ts")
				probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: "{S}/proj/direct.ts", Content: discoverFactory})
			case 4:
				probe.Configured = append(probe.Configured, "direct.ts", "./direct.ts", "../proj/direct.ts")
				probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: "{S}/proj/direct.ts", Content: discoverFactory})
			case 5:
				probe.Configured = append(probe.Configured, "{S}/proj/missing.ts", "gone.js")
			case 6:
				probe.Configured = append(probe.Configured, "file://{S}/proj/direct.ts", "~/home-ext.ts")
				probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: "{S}/proj/direct.ts", Content: discoverFactory}, discoverEntry{Kind: "file", Path: "{S}/home/home-ext.ts", Content: discoverFactory})
			case 7:
				probe.Configured = append(probe.Configured, "{S}/proj/nbsp\u00a0file.ts", "{S}/proj/nbsp file.ts")
				probe.Build = append(probe.Build, discoverEntry{Kind: "file", Path: "{S}/proj/nbsp file.ts", Content: discoverFactory})
			default:
				probe.Configured = append(probe.Configured, "{S}/proj/shared/", "{S}//proj//shared")
			}
		}
		probes = append(probes, probe)
	}
	return probes
}

// discoverAndLoadExtensions (core/extensions/loader.ts:828-875, discoverExtensionsInDir, resolveExtensionEntries) against pinned Pi over 300 seeded trees: the project, agent and configured locations in order, direct files, one-level subdirectories with index.ts/index.js or a package.json `pi.extensions` manifest (missing and unreadable manifests, entries outside the directory), symbolic links, configured files, directories, `~`, file:// URLs, relative and no-break-space spellings, and deduplication by absolute path. Pi's loaded and failed extension paths equal Pig's discovered paths split by whether the path is a regular file.
func TestDiscoverExtensionPathsMatchPi(t *testing.T) {
	probes := discoverProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	piSandbox, pigSandbox := t.TempDir(), t.TempDir()
	cmd := exec.CommandContext(t.Context(), "node", "testdata/discover_extensions.mjs", pigversion.UpstreamPackageVersion, piSandbox)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, stderr.String())
	}
	var want []discoverOut
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(probes) {
		t.Fatalf("Pi returned %d results for %d probes", len(want), len(probes))
	}
	for i, probe := range probes {
		base := filepath.Join(pigSandbox, fmt.Sprintf("p%d", i))
		fill := func(s string) string {
			return strings.ReplaceAll(strings.ReplaceAll(s, "{S}", base), "{CFG}", icodingagent.ConfigDirName())
		}
		if err := os.MkdirAll(filepath.Join(base, "home"), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, entry := range probe.Build {
			path := fill(entry.Path)
			dir := filepath.Dir(path)
			if entry.Kind == "dir" {
				dir = path
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			switch entry.Kind {
			case "file":
				err = os.WriteFile(path, []byte(entry.Content), 0o644)
			case "symlink":
				testenv.Symlink(t, fill(entry.Target), path)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("HOME", filepath.Join(base, "home"))
		t.Setenv("USERPROFILE", filepath.Join(base, "home")) // the home directory is USERPROFILE on Windows
		t.Chdir(base)
		configured := make([]string, len(probe.Configured))
		for j, p := range probe.Configured {
			configured[j] = fill(p)
		}
		paths, _, err := discoverExtensionPaths(configured, fill(probe.CWD), fill(probe.AgentDir))
		if err != nil {
			t.Fatal(err)
		}
		got := discoverOut{Loaded: []string{}, Failed: []string{}}
		for _, p := range paths {
			sep := string(filepath.Separator)
			shown := strings.ReplaceAll(strings.ReplaceAll(p, base, "{S}"), sep+icodingagent.ConfigDirName()+sep, sep+"{CFG}"+sep)
			if info, statErr := os.Stat(p); statErr == nil && info.Mode().IsRegular() {
				got.Loaded = append(got.Loaded, shown)
			} else {
				got.Failed = append(got.Failed, shown)
			}
		}
		if !reflect.DeepEqual(got, want[i]) {
			in, _ := json.Marshal(probe)
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(want[i])
			t.Fatalf("probe %d %s\nPig: %s\nPi:  %s", i, in, gotJSON, wantJSON)
		}
	}
}
