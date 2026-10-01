package codingagent

import (
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Regression tests for the settings that upstream 0.99.1 adds beyond its ported cases
// (.upstream/v0.99.1/packages/coding-agent/src/core/settings-manager.ts).

// mergeDefaultTools (settings-manager.ts:222-228): a list of only +name/-name entries appends to the inherited list,
// any other list replaces it, and an empty list counts as all modifiers (Array.prototype.every on []).
func TestMergeDefaultToolsMatchesUpstream(t *testing.T) {
	for _, tc := range []struct {
		name            string
		base, overrides []string
		want            []string
	}{
		{"no override keeps the base", []string{"read"}, nil, []string{"read"}},
		{"no base takes the override", nil, []string{"+a"}, []string{"+a"}},
		{"plain names replace", []string{"read", "+a"}, []string{"grep"}, []string{"grep"}},
		{"modifiers append", []string{"read"}, []string{"+a", "-read"}, []string{"read", "+a", "-read"}},
		{"mixed list replaces", []string{"read"}, []string{"+a", "grep"}, []string{"+a", "grep"}},
		{"empty override keeps an inherited list", []string{"read"}, []string{}, []string{"read"}},
		{"empty override without a base stays empty", nil, []string{}, []string{}},
		{"empty override on an empty base stays empty", []string{}, []string{}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeSettings(Settings{DefaultTools: tc.base}, Settings{DefaultTools: tc.overrides}).DefaultTools
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("merged = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// Global and project `defaultTools: []` merge to [] (`[...[], ...[]]`, settings-manager.ts:227), which getDefaultTools
// resolves to no tools; it must not collapse to unset and fall back to the built-in defaults (settings-manager.ts:1430-1434).
func TestEmptyDefaultToolsInBothLayersSelectNoTools(t *testing.T) {
	f := newUpstreamSettingsFixture(t)
	f.write(f.global(), map[string]any{"defaultTools": []string{}})
	f.write(f.project(), map[string]any{"defaultTools": []string{}})
	manager := f.create()
	if got := manager.GetDefaultTools(); got == nil || len(got) != 0 {
		t.Fatalf("layered empty lists = %#v, want []", got)
	}
	manager.ApplyOverrides(Settings{DefaultTools: []string{}})
	if got := manager.GetDefaultTools(); got == nil || len(got) != 0 {
		t.Fatalf("empty override = %#v, want []", got)
	}
}

// resolveDefaultTools (settings-manager.ts:233-245).
func TestResolveDefaultToolsMatchesUpstream(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    []string
	}{
		{"empty list stays empty", []string{}, []string{}},
		{"plain names replace the defaults", []string{"grep", "find"}, []string{"grep", "find"}},
		{"modifiers start from the defaults", []string{"+x"}, []string{"read", "bash", "edit", "write", "x"}},
		{"a bare plus adds nothing", []string{"+"}, []string{"read", "bash", "edit", "write"}},
		{"removing an absent tool is a no-op", []string{"-nope"}, []string{"read", "bash", "edit", "write"}},
		{"order matters", []string{"+x", "-x", "+x"}, []string{"read", "bash", "edit", "write", "x"}},
		{"a duplicate add is ignored", []string{"read", "+read", "+read"}, []string{"read"}},
		{"plain names stay in list order", []string{"b", "a", "-b", "+b"}, []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := NewInMemorySettingsManager(Settings{DefaultTools: tc.entries}).GetDefaultTools()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("resolved = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// getSettings returns a copy: changing it must not change the manager (structuredClone, settings-manager.ts:564-566).
func TestGetSettingsReturnsAnIndependentCopy(t *testing.T) {
	f := newUpstreamSettingsFixture(t)
	f.write(f.global(), map[string]any{"theme": "dark", "extensions": []string{"a.ts"}})
	f.write(f.project(), map[string]any{"defaultModel": "m"})
	manager := f.create()
	manager.ApplyOverrides(Settings{DefaultProvider: "override"})

	got := manager.GetSettings()
	if got.Theme != "dark" || got.DefaultModel != "m" || got.DefaultProvider != "override" {
		t.Fatalf("effective settings = %#v", got)
	}
	got.Extensions[0] = "changed.ts"
	if again := manager.GetSettings(); again.Extensions[0] != "a.ts" {
		t.Fatalf("mutating the copy changed the manager: %v", again.Extensions)
	}
}

// deviceId is a global setting: a project file must not give every clone the same ID (settings-manager.ts:1167-1169),
// and an in-memory manager keeps the ID it created.
func TestGetOrCreateDeviceIDIgnoresProjectAndKeepsMemoryID(t *testing.T) {
	f := newUpstreamSettingsFixture(t)
	f.write(f.project(), map[string]any{"deviceId": "project-device"})
	if got := f.create().GetOrCreateDeviceID(); got == "" || got == "project-device" {
		t.Fatalf("device ID = %q", got)
	}
	memory := NewInMemorySettingsManager(Settings{})
	first := memory.GetOrCreateDeviceID()
	if first == "" || memory.GetOrCreateDeviceID() != first {
		t.Fatalf("in-memory device ID is not stable: %q", first)
	}
	preset := NewInMemorySettingsManager(Settings{DeviceID: "fixed"})
	if got := preset.GetOrCreateDeviceID(); got != "fixed" {
		t.Fatalf("stored device ID = %q", got)
	}
}

// A global file that does not parse keeps the ID in memory and does not overwrite the file (save() returns early, settings-manager.ts:737).
func TestGetOrCreateDeviceIDWithUnparsableGlobalFile(t *testing.T) {
	f := newUpstreamSettingsFixture(t)
	const broken = "{not json"
	f.write(f.global(), broken)
	manager := f.create()
	id := manager.GetOrCreateDeviceID()
	if id == "" || manager.GetOrCreateDeviceID() != id {
		t.Fatalf("device ID is not stable in memory: %q", id)
	}
	if data := readFileString(t, f.global()); data != broken {
		t.Fatalf("global settings file was rewritten: %q", data)
	}
}

// The wheel setter clamps and floors, and stores auto as the string (settings-manager.ts:1394-1400).
func TestSetFullscreenWheelScrollLinesClampsAndPersists(t *testing.T) {
	for _, tc := range []struct {
		in   WheelScrollLines
		want any
	}{
		{WheelScrollLines{Auto: true}, "auto"},
		{WheelScrollLines{Lines: 2.9}, float64(2)},
		{WheelScrollLines{Lines: 0}, float64(1)},
		{WheelScrollLines{Lines: -5}, float64(1)},
		{WheelScrollLines{Lines: 101}, float64(100)},
	} {
		f := newUpstreamSettingsFixture(t)
		manager := f.create()
		mustNoError(t, manager.SetFullscreenWheelScrollLines(tc.in))
		if got := f.readJSON(f.global())["fullscreenWheelScrollLines"]; got != tc.want {
			t.Fatalf("set %#v: saved %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// A project value overrides the global one, and a JSON null overrides it too and reads as auto (deepMergeObjects skips only undefined).
func TestFullscreenWheelScrollLinesLayers(t *testing.T) {
	f := newUpstreamSettingsFixture(t)
	f.write(f.global(), map[string]any{"fullscreenWheelScrollLines": 4})
	if got := f.create().GetFullscreenWheelScrollLines(); got != (WheelScrollLines{Lines: 4}) {
		t.Fatalf("global = %#v", got)
	}
	f.write(f.project(), map[string]any{"fullscreenWheelScrollLines": 9})
	if got := f.create().GetFullscreenWheelScrollLines(); got != (WheelScrollLines{Lines: 9}) {
		t.Fatalf("project = %#v", got)
	}
	f.write(f.project(), `{"fullscreenWheelScrollLines": null}`)
	if got := f.create().GetFullscreenWheelScrollLines(); got != (WheelScrollLines{Auto: true}) {
		t.Fatalf("project null = %#v", got)
	}
}

// codemode nests: project fields merge over global fields one by one (deepMergeObjects).
func TestCodemodeSettingsRoundTripAndMerge(t *testing.T) {
	f := newUpstreamSettingsFixture(t)
	f.write(f.global(), map[string]any{"codemode": map[string]any{"mode": "only", "inlineBudget": 500}})
	f.write(f.project(), map[string]any{"codemode": map[string]any{"inlineBudget": 900}})
	got := f.create().GetSettings().Codemode
	if got == nil || got.Mode != CodemodeModeOnly || got.InlineBudget == nil || *got.InlineBudget != 900 {
		t.Fatalf("merged codemode = %#v", got)
	}
	f.write(f.project(), map[string]any{"codemode": map[string]any{"mode": "on"}})
	got = f.create().GetSettings().Codemode
	if got == nil || got.Mode != CodemodeModeOn || got.InlineBudget == nil || *got.InlineBudget != 500 {
		t.Fatalf("project mode over global budget = %#v", got)
	}
	mustNoError(t, f.create().UpdateGlobal(func(s *Settings) { s.Theme = "light" }))
	saved, _ := f.readJSON(f.global())["codemode"].(map[string]any)
	if saved["mode"] != "only" || saved["inlineBudget"] != float64(500) {
		t.Fatalf("an unrelated save changed codemode: %#v", saved)
	}
}

// Upstream bug-report.ts:62 drops both trackingId and deviceId from the reported settings.
func TestBugReportOmitsDeviceID(t *testing.T) {
	metadata, err := CollectBugReportMetadata(BugReportInputs{GlobalSettings: Settings{DeviceID: "device-secret", TrackingID: "tracking-secret"}}, BugReportOptions{}, false, fixedBugReportTime())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bugReportJSON(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "device-secret") || strings.Contains(string(encoded), "deviceId") {
		t.Fatalf("bug report carries the device ID:\n%s", encoded)
	}
}

// Concurrent first callers agree on one ID and one write (the settings lock makes creation atomic).
func TestGetOrCreateDeviceIDIsAtomic(t *testing.T) {
	f := newUpstreamSettingsFixture(t)
	manager := f.create()
	const callers = 16
	ids := make([]string, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Go(func() {
			<-start
			ids[i] = manager.GetOrCreateDeviceID()
		})
	}
	close(start)
	wg.Wait()
	for _, id := range ids {
		if id == "" || id != ids[0] {
			t.Fatalf("callers disagree: %v", ids)
		}
	}
	if saved := f.readJSON(f.global())["deviceId"]; saved != ids[0] {
		t.Fatalf("saved %v, callers got %s", saved, ids[0])
	}
}
