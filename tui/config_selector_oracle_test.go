package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type configOracleResource struct {
	Path     string               `json:"path"`
	Enabled  bool                 `json:"enabled"`
	Metadata configOracleMetadata `json:"metadata"`
}

type configOracleMetadata struct {
	Source  string `json:"source"`
	Scope   string `json:"scope"`
	Origin  string `json:"origin"`
	BaseDir string `json:"baseDir,omitempty"`
}

type configOracleResolved struct {
	Extensions []configOracleResource `json:"extensions"`
	Skills     []configOracleResource `json:"skills"`
	Prompts    []configOracleResource `json:"prompts"`
	Themes     []configOracleResource `json:"themes"`
}

type configOracleProbe struct {
	Resolved             map[string]configOracleResolved `json:"resolved"`
	AgentDir             string                          `json:"agentDir"`
	Width                int                             `json:"width"`
	Height               int                             `json:"height,omitempty"`
	WriteScope           string                          `json:"writeScope"`
	ProjectModeAvailable bool                            `json:"projectModeAvailable"`
	Bindings             map[string][]string             `json:"bindings,omitempty"`
	Keys                 []string                        `json:"keys"`
}

type configOracleStep struct {
	Cancelled int `json:"cancelled"`
	Exited    int `json:"exited"`
}

type configOracleResult struct {
	Steps  []configOracleStep `json:"steps"`
	Frames [][]string         `json:"frames"`
	Crash  *string            `json:"crash"`
}

func configOracleItems(resolved configOracleResolved) []ResourceItem {
	var items []ResourceItem
	add := func(kind ResourceType, resources []configOracleResource) {
		for _, r := range resources {
			items = append(items, ResourceItem{Path: r.Path, Enabled: r.Enabled, ResourceType: kind, Scope: r.Metadata.Scope, Origin: r.Metadata.Origin, Source: r.Metadata.Source, BaseDir: r.Metadata.BaseDir})
		}
	}
	add(ResourceExtensions, resolved.Extensions)
	add(ResourceSkills, resolved.Skills)
	add(ResourcePrompts, resolved.Prompts)
	add(ResourceThemes, resolved.Themes)
	return items
}

func configOracleResource1(path string, enabled bool, source, scope, origin, baseDir string) configOracleResource {
	return configOracleResource{Path: path, Enabled: enabled, Metadata: configOracleMetadata{Source: source, Scope: scope, Origin: origin, BaseDir: baseDir}}
}

// config-selector.ts ConfigSelectorComponent/ResourceList against pinned Pi: group/subgroup layout and labels, item display names,
// up/down/page navigation over item rows only, search filtering, space/enter toggling, Escape/Ctrl+C callbacks and Tab switching
// of the write scope; the callbacks after each key and every frame's rows must agree byte for byte (colours included; only Pi's .pi/ directory is read as .pig/).
func TestConfigSelectorMatchesPi(t *testing.T) {
	user := func(path string, enabled bool) configOracleResource {
		return configOracleResource1(path, enabled, "local", "user", "top-level", "")
	}
	pkg := func(path string, enabled bool, scope string) configOracleResource {
		return configOracleResource1(path, enabled, "npm:@acme/pack", scope, "package", "/work/pack")
	}
	auto := func(path string, enabled bool, scope string) configOracleResource {
		return configOracleResource1(path, enabled, "auto", scope, "top-level", "/work/auto")
	}
	small := configOracleResolved{
		Extensions: []configOracleResource{user("/agent/extensions/zeta.ts", true), user("/agent/extensions/alpha/index.ts", false), pkg("/work/pack/extensions/tool.ts", true, "user"), configOracleResource1("builtin:cursor", true, "builtin", "user", "top-level", "")},
		Skills:     []configOracleResource{user("/agent/skills/review/SKILL.md", true), auto("/work/auto/skills/lint.md", false, "user")},
		Prompts:    []configOracleResource{user("/agent/prompts/fix.md", true)},
		Themes:     []configOracleResource{user("/agent/themes/solar.json", false)},
	}
	projectSmall := configOracleResolved{
		Extensions: []configOracleResource{configOracleResource1("/proj/.pig/extensions/zeta.ts", true, "local", "project", "top-level", ""), pkg("/work/pack/extensions/tool.ts", false, "project")},
		Skills:     []configOracleResource{auto("/work/proj/skills/deep/SKILL.md", true, "project")},
	}
	var large configOracleResolved
	for i := range 14 {
		large.Extensions = append(large.Extensions, user(fmt.Sprintf("/agent/extensions/ext%02d/index.ts", i), i%3 != 0))
	}
	for i := range 9 {
		large.Skills = append(large.Skills, pkg(fmt.Sprintf("/work/pack/skills/skill%d/SKILL.md", i), i%2 == 0, "user"))
	}
	for i := range 6 {
		large.Prompts = append(large.Prompts, auto(fmt.Sprintf("/work/auto/prompts/p%d.md", i), true, "user"))
	}
	resolveds := []map[string]configOracleResolved{
		{"global": {}, "project": {}},
		{"global": small, "project": projectSmall},
		{"global": large, "project": small},
	}
	base := []string{"\x1b[A", "\x1b[A", "\x1b[B", "\x1b[B", "\x1b[B", "\x1b[5~", "\x1b[6~", "\x1b", "\x03", "\x7f", "e", "x", "s", "k", "p", "t", "a", "/", ".", "\x15", "\x17", "z", "l"}
	// Pi derives a project override from the SettingsManager's project settings and Pig carries it on the item (OnOverride persists it),
	// so project-scope toggles are not comparable here: toggle keys only run in global scope, Tab only where no toggle can follow in project scope.
	toggling := append(append([]string{}, base...), " ", " ", "\r")
	switching := append(append([]string{}, base...), "\t", "\t")
	rng := rand.New(rand.NewSource(20261010))
	var probes []configOracleProbe
	for _, resolved := range resolveds {
		for _, height := range []int{0, 12, 40} {
			for _, bindings := range []map[string][]string{nil, {"tui.select.cancel": {"escape"}}, {"tui.select.cancel": {"ctrl+g"}, "tui.select.confirm": {"ctrl+y"}, "tui.select.up": {"ctrl+p"}, "tui.select.down": {"ctrl+n"}, "tui.select.pageUp": {"ctrl+b"}, "tui.select.pageDown": {"ctrl+f"}}} {
				for _, mode := range []struct {
					scope    string
					avail    bool
					alphabet []string
				}{{"global", true, toggling}, {"global", false, append(append([]string{}, toggling...), "\t", "\t")}, {"global", true, switching}, {"project", true, switching}} {
					for range 6 {
						keys := make([]string, 4+rng.Intn(36))
						for i := range keys {
							keys[i] = mode.alphabet[rng.Intn(len(mode.alphabet))]
							if bindings != nil && rng.Intn(5) == 0 {
								rebound := []string{"\x07", "\x19", "\x10", "\x0e", "\x02", "\x06"}
								if mode.scope == "project" || len(mode.alphabet) == len(switching) {
									rebound = append(rebound[:1], rebound[2:]...) // no rebound confirm key where a project toggle could follow
								}
								keys[i] = rebound[rng.Intn(len(rebound))]
							}
						}
						probes = append(probes, configOracleProbe{Resolved: resolved, AgentDir: "/agent", Width: 100, Height: height, WriteScope: mode.scope, ProjectModeAvailable: mode.avail, Bindings: bindings, Keys: keys})
					}
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/config_selector.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []configOracleResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousKitty, previousBindings, previousTheme := GetCapabilities(), IsKittyProtocolActive(), GetKeybindings(), ActiveTheme()
	SetCapabilities(TerminalCapabilities{TrueColor: true})
	SetTheme("dark") // the Pi fixture renders with the dark theme
	SetKittyProtocolActive(false)
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		SetKittyProtocolActive(previousKitty)
		SetKeybindings(previousBindings)
		storeActiveTheme(previousTheme)
	})
	failures, crashes := 0, 0
	reached := map[string]int{}
	for i, probe := range probes {
		userBindings := map[string][]KeyID{}
		for action, keys := range probe.Bindings {
			for _, key := range keys {
				userBindings[action] = append(userBindings[action], KeyID(key))
			}
		}
		restoreKeybindingsAfterTest(t)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), userBindings))
		global := BuildResourceGroups(configOracleItems(probe.Resolved["global"]))
		projectItems := configOracleItems(probe.Resolved["project"])
		globalEnabled := map[string]bool{}
		for _, item := range configOracleItems(probe.Resolved["global"]) {
			globalEnabled[string(item.ResourceType)+":"+item.Path] = item.Enabled
		}
		for j := range projectItems {
			inherited, found := globalEnabled[string(projectItems[j].ResourceType)+":"+projectItems[j].Path]
			projectItems[j].Inherited = found || projectItems[j].Scope == "user"
			if !found && projectItems[j].Scope == "project" {
				inherited = true
			}
			projectItems[j].InheritedEnabled = inherited
			projectItems[j].Override = "inherit"
		}
		selector := NewScopedConfigSelector(global, BuildResourceGroups(projectItems), probe.Height, probe.WriteScope, probe.ProjectModeAvailable)
		if probe.Height == 0 {
			selector = NewScopedConfigSelector(global, BuildResourceGroups(projectItems), 24, probe.WriteScope, probe.ProjectModeAvailable)
		}
		cancelled, exited := 0, 0
		selector.OnCancel = func() { cancelled++ }
		selector.OnExit = func() { exited++ }
		frame := func() []string {
			lines := selector.Render(probe.Width)
			return slices.Clone(lines)
		}
		want := expected[i]
		got := configOracleResult{Steps: []configOracleStep{{}}, Frames: [][]string{frame()}}
		for k := range probe.Keys {
			if want.Crash != nil && k >= len(want.Steps)-1 {
				break
			}
			selector.HandleInput(probe.Keys[k])
			got.Steps = append(got.Steps, configOracleStep{Cancelled: cancelled, Exited: exited})
			got.Frames = append(got.Frames, frame())
		}
		if want.Crash != nil {
			crashes++
		}
		if cancelled > 0 {
			reached["cancel"]++
		}
		if exited > 0 {
			reached["exit"]++
		}
		wantFrames := make([][]string, len(want.Frames))
		for k, f := range want.Frames {
			wantFrames[k] = make([]string, len(f))
			for l, line := range f {
				// pig divergence (D80-family brand): Pig's config directory is .pig, Pi's is .pi (D1 brand rename).
				wantFrames[k][l] = strings.ReplaceAll(line, ".pi/", ".pig/")
			}
		}
		if !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Frames, wantFrames) {
			failures++
			if failures <= 3 {
				at := 0
				for at < len(got.Frames) && at < len(wantFrames) && reflect.DeepEqual(got.Frames[at], wantFrames[at]) {
					at++
				}
				t.Errorf("probe %d scope=%s avail=%v h=%d keys=%q\nsteps differ=%v; first frame difference at %d\nPig:\n%s\nPi:\n%s", i, probe.WriteScope, probe.ProjectModeAvailable, probe.Height, probe.Keys, !reflect.DeepEqual(got.Steps, want.Steps), at, joinFrame(got.Frames, at), joinFrame(wantFrames, at))
			}
		}
	}
	if reached["cancel"] == 0 || reached["exit"] == 0 {
		t.Errorf("probes never reached cancel/exit: %v", reached)
	}
	t.Logf("%d probes, %d failures, %d Pi crashes, reached %v", len(probes), failures, crashes, reached)
}

func joinFrame(frames [][]string, at int) string {
	if at >= len(frames) {
		return "(none)"
	}
	return strings.Join(frames[at], "\n")
}
