package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
)

type loadedResourcesProbe struct {
	Kind        string                           `json:"kind"`
	Infos       map[string]*extension.SourceInfo `json:"infos,omitempty"`
	Diagnostics []extension.ResourceDiagnostic   `json:"diagnostics,omitempty"`
	Path        string                           `json:"path,omitempty"`
	Info        *extension.SourceInfo            `json:"info,omitempty"`
	Items       []loadedResourceItem             `json:"items,omitempty"`
}

type loadedResourceItem struct {
	Path       string                `json:"path"`
	SourceInfo *extension.SourceInfo `json:"sourceInfo,omitempty"`
}

// interactive-mode.ts getShortPath, buildScopeGroups, formatScopeGroups, findSourceInfoForPath, formatPathWithSource and
// formatDiagnostics against pinned Pi, called through InteractiveMode's prototype with a fixed home directory.
func TestLoadedResourcesFormattingMatchesPi(t *testing.T) {
	const home = "/home/tester"
	// A SourceInfo always carries a source and a scope upstream; Go has no undefined string, so an empty one is not probed.
	sources := []string{"local", "cli", "npm:foo", "npm:@scope/pkg", "git:github.com/user/repo", "auto", "npm:@a/b@1.2.3"}
	scopes := []string{"user", "project", "temporary", "other"}
	bases := []string{"", home + "/.pi/agent/npm/node_modules/foo", "/opt/node_modules/@scope/pkg", home + "/.pi/agent/git/github.com/user/repo", "/srv/base", `C:\\work\\base`}
	paths := []string{
		home + "/.pi/agent/npm/node_modules/foo/extensions/index.ts", "/opt/node_modules/@scope/pkg/skills/a/SKILL.md", "/opt/node_modules/@scope/pkg",
		home + "/.pi/agent/git/github.com/user/repo/prompts/p.md", home + "/.pi/agent/skills/x/SKILL.md", home + "/project/.pi/extensions/e.ts",
		"/srv/base/lib/a.js", "/srv/base", "/srv/other/a.js", "/srv/base/../elsewhere/z", `C:\\work\\base\\sub\\f.ts`, "relative/path/a.ts", "/",
		home, home + "x/not-home", "/node_modules/pkg/inner/file", "/x/git/a/b/c/d.md", "/srv/base/", "/opt/node_modules-extra/@scope/pkg/a.md", "/srv/basement/x",
	}
	rng := rand.New(rand.NewSource(11))
	pick := func(list []string) string { return list[rng.Intn(len(list))] }
	info := func() *extension.SourceInfo {
		return &extension.SourceInfo{Path: pick(paths), Source: pick(sources), Scope: pick(scopes), Origin: "top-level", BaseDir: pick(bases)}
	}
	var probes []loadedResourcesProbe
	for range 400 {
		probes = append(probes, loadedResourcesProbe{Kind: "shortPath", Path: pick(paths), Info: info()})
	}
	probes = append(probes, loadedResourcesProbe{Kind: "shortPath", Path: pick(paths)})
	for range 150 {
		infos := map[string]*extension.SourceInfo{}
		for range 1 + rng.Intn(4) {
			infos[pick(paths)] = info()
		}
		var diagnostics []extension.ResourceDiagnostic
		for range 1 + rng.Intn(5) {
			switch rng.Intn(3) {
			case 0:
				diagnostics = append(diagnostics, extension.ResourceDiagnostic{Type: "collision", Message: "collision", Collision: &extension.ResourceCollision{ResourceType: "skill", Name: pick([]string{"alpha", "beta"}), WinnerPath: pick(paths), LoserPath: pick(paths)}})
			case 1:
				diagnostics = append(diagnostics, extension.ResourceDiagnostic{Type: pick([]string{"warning", "error"}), Message: "message " + fmt.Sprint(rng.Intn(9)), Path: pick(append(paths, ""))})
			default:
				diagnostics = append(diagnostics, extension.ResourceDiagnostic{Type: "collision", Message: "no collision object"})
			}
		}
		probes = append(probes, loadedResourcesProbe{Kind: "diagnostics", Infos: infos, Diagnostics: diagnostics})
	}
	for range 150 {
		var items []loadedResourceItem
		for range 1 + rng.Intn(8) {
			item := loadedResourceItem{Path: pick(paths)}
			if rng.Intn(5) > 0 {
				item.SourceInfo = info()
			}
			items = append(items, item)
		}
		probes = append(probes, loadedResourcesProbe{Kind: "scopeGroups", Items: items})
	}

	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/loaded_resources.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	cmd.Env = append(os.Environ(), "HOME="+home)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []string
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	tui.SetTheme("dark")
	t.Setenv("HOME", home)
	collator := collate.New(language.Und)
	failures := 0
	for i, probe := range probes {
		var got string
		switch probe.Kind {
		case "shortPath":
			got = getShortPath(probe.Path, probe.Info)
		case "diagnostics":
			got = formatResourceDiagnostics(probe.Diagnostics, probe.Infos)
		case "scopeGroups":
			items := make([]loadedResource, len(probe.Items))
			for j, item := range probe.Items {
				items[j] = loadedResource{path: item.Path, info: item.SourceInfo}
			}
			got = formatScopeGroups(tui.ActiveTheme(), collator, buildScopeGroups(items), formatDisplayPathItem, getShortPathItem)
		}
		if got != expected[i] {
			if failures++; failures <= 6 {
				probeJSON, _ := json.Marshal(probe)
				t.Errorf("probe %d %s\n  %s\n  Pig %q\n  Pi  %q", i, probe.Kind, strings.TrimSpace(string(probeJSON)), got, expected[i])
			}
		}
	}
	if failures > 6 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
