package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type configContractProbe struct {
	configOracleProbe
	Theme     string `json:"theme"`
	TrueColor bool   `json:"trueColor"`
}

// newConfigContractSelector builds Pig's selector for a probe the way coding/cli newScopedConfigSelector builds `pig config`: the project list carries each
// item's inheritance from the global list (the same type and path means inherited, a user-scope item inherits by scope, a project-scope item the
// project list alone has inherits as enabled) and every project item starts with no override.
func newConfigContractSelector(probe configContractProbe) *ConfigSelectorComponent {
	global := configOracleItems(probe.Resolved["global"])
	projectItems := configOracleItems(probe.Resolved["project"])
	globalEnabled := map[string]bool{}
	for _, item := range global {
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
	rows := probe.Height
	if rows == 0 {
		rows = 24
	}
	selector := NewScopedConfigSelector(BuildResourceGroups(global), BuildResourceGroups(projectItems), rows, probe.WriteScope, probe.ProjectModeAvailable)
	selector.OnOverride = func(*ResourceItem, string) error { return nil }
	return selector
}

func runConfigContractOracle(t *testing.T, probes []configContractProbe) []configOracleResult {
	t.Helper()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	// --single-threaded: see TestConfigSelectorMatchesPi (nodejs/node#64274).
	cmd := exec.CommandContext(ctx, "node", "--single-threaded", "testdata/config_selector_contract.mjs", pigversion.UpstreamPackageVersion)
	cmd.WaitDelay = 10 * time.Second
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
	if len(expected) != len(probes) {
		t.Fatalf("oracle returned %d results for %d probes", len(expected), len(probes))
	}
	return expected
}

// compareConfigContractProbes runs probes through Pig's selector and fails on the first rows that differ from pinned Pi (Pi's .pi/ directory read as .pig/, D2).
func compareConfigContractProbes(t *testing.T, probes []configContractProbe) {
	t.Helper()
	expected := runConfigContractOracle(t, probes)
	previousCaps, previousKitty, previousBindings, previousTheme := GetCapabilities(), IsKittyProtocolActive(), GetKeybindings(), ActiveTheme()
	SetKittyProtocolActive(false)
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		SetKittyProtocolActive(previousKitty)
		SetKeybindings(previousBindings)
		storeActiveTheme(previousTheme)
	})
	failures := 0
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: probe.TrueColor})
		SetTheme(probe.Theme)
		userBindings := map[string][]KeyID{}
		for action, keys := range probe.Bindings {
			userBindings[action] = []KeyID{} // an action bound to no key stays unbound
			for _, key := range keys {
				userBindings[action] = append(userBindings[action], KeyID(key))
			}
		}
		restoreKeybindingsAfterTest(t)
		SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), userBindings))
		selector := newConfigContractSelector(probe)
		cancelled, exited := 0, 0
		selector.OnCancel = func() { cancelled++ }
		selector.OnExit = func() { exited++ }
		want := expected[i]
		got := configOracleResult{Steps: []configOracleStep{{}}, Frames: [][]string{slices.Clone(selector.Render(probe.Width))}}
		for k := range probe.Keys {
			if want.Crash != nil && k >= len(want.Steps)-1 {
				break
			}
			selector.HandleInput(probe.Keys[k])
			got.Steps = append(got.Steps, configOracleStep{Cancelled: cancelled, Exited: exited})
			got.Frames = append(got.Frames, slices.Clone(selector.Render(probe.Width)))
		}
		wantFrames := make([][]string, len(want.Frames))
		for k, f := range want.Frames {
			wantFrames[k] = make([]string, len(f))
			for l, line := range f {
				wantFrames[k][l] = strings.ReplaceAll(line, ".pi/", ".pig/")
				// The scope hint row names the settings file: Pi's `.pi/` is one cell shorter than Pig's `.pig/` (D2), so a width that truncates the
				// row cuts them at different characters. Below that width the row is compared for fitting the width, not for its characters.
				if l == configScopeHintRow && probe.Width < configScopeHintCells {
					wantFrames[k][l] = ""
					if k < len(got.Frames) && l < len(got.Frames[k]) {
						if widthx.VisibleWidth(got.Frames[k][l]) > probe.Width {
							t.Errorf("probe %d frame %d row %d is wider than %d: %q", i, k, l, probe.Width, got.Frames[k][l])
						}
						got.Frames[k][l] = ""
					}
				}
			}
		}
		if !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Frames, wantFrames) {
			if failures++; failures <= 3 {
				at := 0
				for at < len(got.Frames) && at < len(wantFrames) && reflect.DeepEqual(got.Frames[at], wantFrames[at]) {
					at++
				}
				t.Errorf("probe %d scope=%s avail=%v w=%d h=%d theme=%s trueColor=%v bindings=%v keys=%q\nsteps differ=%v; first frame difference at %d\nPig:\n%s\nPi:\n%s", i, probe.WriteScope, probe.ProjectModeAvailable, probe.Width, probe.Height, probe.Theme, probe.TrueColor, probe.Bindings, probe.Keys, !reflect.DeepEqual(got.Steps, want.Steps), at, joinFrame(got.Frames, at), joinFrame(wantFrames, at))
			}
		}
	}
	if failures > 3 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// configScopeHintCells is the width of the widest scope hint row (the project scope's), in Pig's wording.
const configScopeHintCells = 70

// configScopeHintRow is the scope hint's row in a frame: the spacer, the border, a spacer, the title row, then the hint.
const configScopeHintRow = 4

func configContractResolved() map[string]map[string]configOracleResolved {
	user := func(path string, enabled bool) configOracleResource {
		return configOracleResource1(path, enabled, "local", "user", "top-level", "")
	}
	userPkg := func(path string, enabled bool) configOracleResource {
		return configOracleResource1(path, enabled, "npm:@acme/pack", "user", "package", "/work/pack")
	}
	projectPkg := func(path string, enabled bool) configOracleResource {
		return configOracleResource1(path, enabled, "npm:@acme/pack", "project", "package", "/work/pack")
	}
	auto := func(path string, enabled bool, scope string) configOracleResource {
		return configOracleResource1(path, enabled, "auto", scope, "top-level", "/work/auto")
	}
	builtin := configOracleResource1("builtin:cursor", true, "builtin", "user", "top-level", "")
	projectOnly := configOracleResource1("/work/project/.pi/extensions/local.ts", true, "local", "project", "top-level", "")
	temporary := configOracleResource1("/tmp/one-off.ts", true, "cli", "temporary", "top-level", "")
	globalList := configOracleResolved{
		Extensions: []configOracleResource{user("/agent/extensions/zeta.ts", true), user("/agent/extensions/alpha/index.ts", false), userPkg("/work/pack/extensions/tool.ts", true), builtin},
		Skills:     []configOracleResource{user("/agent/skills/review/SKILL.md", true), auto("/work/auto/skills/lint.md", false, "user"), userPkg("/work/pack/skills/deep/SKILL.md", false)},
		Prompts:    []configOracleResource{user("/agent/prompts/fix.md", true)},
		Themes:     []configOracleResource{user("/agent/themes/solar.json", false)},
	}
	// The project list holds the global items (one disabled by the project) and the project's own.
	projectList := configOracleResolved{
		Extensions: []configOracleResource{user("/agent/extensions/zeta.ts", false), user("/agent/extensions/alpha/index.ts", false), userPkg("/work/pack/extensions/tool.ts", true), builtin, projectOnly, projectPkg("/work/pack/extensions/other.ts", false)},
		Skills:     []configOracleResource{user("/agent/skills/review/SKILL.md", true), auto("/work/auto/skills/lint.md", false, "user"), userPkg("/work/pack/skills/deep/SKILL.md", false), auto("/work/proj/skills/deep/SKILL.md", true, "project")},
		Prompts:    []configOracleResource{user("/agent/prompts/fix.md", true), auto("/work/proj/prompts/p.md", false, "project")},
		Themes:     []configOracleResource{user("/agent/themes/solar.json", false)},
	}
	// Global-scope list holding items of other scopes: only user-scope items toggle there (config-selector.ts:494).
	otherScopes := configOracleResolved{
		Extensions: []configOracleResource{user("/agent/extensions/a.ts", true), projectOnly, temporary, projectPkg("/work/pack/extensions/other.ts", true), userPkg("/work/pack/extensions/tool.ts", false)},
		Skills:     []configOracleResource{auto("/work/proj/skills/deep/SKILL.md", false, "project")},
	}
	// One path listed twice (a top-level file and a package member): ResourceList.updateItem sets the first match as well as the toggled item.
	dupes := configOracleResolved{
		Extensions: []configOracleResource{userPkg("/agent/extensions/dup.ts", false), user("/agent/extensions/dup.ts", true), user("/agent/extensions/other.ts", true)},
		Skills:     []configOracleResource{user("/agent/extensions/dup.ts", true)}, // the same path under another resource type is another item
	}
	var many configOracleResolved
	for i := range 12 {
		many.Extensions = append(many.Extensions, user(fmt.Sprintf("/agent/extensions/ext%02d/index.ts", i), i%3 != 0))
	}
	for i := range 6 {
		many.Skills = append(many.Skills, userPkg(fmt.Sprintf("/work/pack/skills/skill%d/SKILL.md", i), i%2 == 0))
	}
	single := configOracleResolved{Extensions: []configOracleResource{user("/agent/extensions/only.ts", true)}}
	return map[string]map[string]configOracleResolved{
		"empty":       {"global": {}, "project": {}},
		"mixed":       {"global": globalList, "project": projectList},
		"otherScopes": {"global": otherScopes, "project": otherScopes},
		"many":        {"global": many, "project": many},
		"single":      {"global": single, "project": single},
		"dupes":       {"global": dupes, "project": dupes},
	}
}

// config-selector.ts ResourceList.handleInput and render, ConfigSelectorHeader.render and ResourceList.renderCheckbox against the pinned Pi, in every
// state the earlier oracle probe leaves out: dark and light themes (and the faint-dim system theme in the directed toggle probes) at 24-bit and 256-color depth, widths from 7 to 100, a rebound tui.input.tab (the
// header names the key, and the key switches the scope), toggles in the project scope (inherit, then unload or load, back to inherit, the checkbox glyph
// and color and the "project load" / "project unload" / "inherited global" suffix of each state) and in global scope over items of other scopes, which Pi
// does not toggle. The callbacks and every frame must agree byte for byte.
func TestConfigSelectorContractMatchesPi(t *testing.T) {
	resolveds := configContractResolved()
	base := []string{"\x1b[A", "\x1b[B", "\x1b[B", "\x1b[5~", "\x1b[6~", "\x1b", "\x03", "\t", "\t", " ", "\r", " ", "\r", "\x7f", "e", "x", "p", "a", "/", "\x15", "z", "\x1b[200~ex \x1b[201~"}
	var probes []configContractProbe
	rng := rand.New(rand.NewPCG(20261102, 1))
	bindingSets := []map[string][]string{
		nil,
		{"tui.input.tab": {"ctrl+t"}},
		{"tui.input.tab": {"ctrl+t", "alt+s"}},
		{"tui.input.tab": {}},
		{"tui.select.cancel": {"escape"}},
		{"tui.select.confirm": {"ctrl+y"}, "tui.select.up": {"ctrl+p"}, "tui.select.down": {"ctrl+n"}, "tui.select.pageUp": {"ctrl+b"}, "tui.select.pageDown": {"ctrl+f"}},
	}
	rebound := []string{"\x14", "\x19", "\x10", "\x0e", "\x02", "\x06", "\x1bs"}
	for _, name := range []string{"empty", "mixed", "otherScopes", "many", "single", "dupes"} {
		for _, theme := range []string{"dark", "light"} {
			for _, trueColor := range []bool{true, false} {
				for _, width := range []int{100, 40, 24, 12, 7} {
					for _, height := range []int{0, 12} {
						for bi, bindings := range bindingSets {
							for _, mode := range []struct {
								scope string
								avail bool
							}{{"global", true}, {"project", true}, {"global", false}, {"project", false}} {
								if rng.IntN(3) != 0 {
									continue
								}
								keys := make([]string, 6+rng.IntN(30))
								for i := range keys {
									keys[i] = base[rng.IntN(len(base))]
									if bi > 0 && rng.IntN(4) == 0 {
										keys[i] = rebound[rng.IntN(len(rebound))]
									}
								}
								probes = append(probes, configContractProbe{
									Resolved: resolveds[name], AgentDir: "/agent", Width: width, Height: height, WriteScope: mode.scope, ProjectModeAvailable: mode.avail, Bindings: bindings, Keys: keys,
									Theme: theme, TrueColor: trueColor,
								})
							}
						}
					}
				}
			}
		}
	}
	// Directed probes: each toggle state of a project item in turn, the header after a rebound-tab switch, and a toggle in the global scope over other scopes.
	space, enter, down, tab := " ", "\r", "\x1b[B", "\t"
	directed := [][]string{
		{space}, {space, space}, {space, space, space}, {space, space, space, space}, {enter, enter, enter}, {down, space, space, space}, {down, down, space, down, space, space},
		{tab, space, space, space}, {tab, down, space, tab, space}, {down, down, down, down, down, space, space, space}, {"\x1b[6~", space, space}, {"\x1b[5~", space},
	}
	directed = append(directed,
		[]string{"\x1b[200~  \x1b[201~", down, space, space},
		[]string{"e", "\x1b[200~  \x1b[201~", "\x7f", space},
		[]string{"\x1b[200~ \x1b[201~", space, "\x1b[200~\u00a0\x1b[201~", space},
		[]string{down, space, down, space, space, down, space},
		// every row in turn, so each item of each scope's list meets the toggle
		[]string{space, down, space, down, space, down, space, down, space, down, space, down, space, down, space, down, space},
		[]string{tab, space, down, space, down, space, down, space, down, space, down, space, down, space, down, space},
	)
	// The system theme (Pi's default) renders `dim` as a faint token (SGR 2 on top of the color), which every dim span of the list must close: a space
	// after a dim checkbox, or the name of a project-owned item after one, must not stay faint. The dark and light themes' dim is a plain color.
	for _, theme := range []struct {
		name      string
		trueColor []bool
	}{{"dark", []bool{true}}, {SystemThemeName, []bool{true, false}}} {
		for _, name := range []string{"mixed", "otherScopes", "many", "single", "dupes"} {
			for _, scope := range []string{"project", "global"} {
				for _, keys := range directed {
					for _, width := range []int{100, 30} {
						for _, trueColor := range theme.trueColor {
							probes = append(probes, configContractProbe{
								Resolved: resolveds[name], AgentDir: "/agent", Width: width, WriteScope: scope, ProjectModeAvailable: true, Keys: keys,
								Theme: theme.name, TrueColor: trueColor,
							})
						}
					}
				}
			}
		}
	}
	compareConfigContractProbes(t, probes)
	t.Logf("%d probes", len(probes))
}
