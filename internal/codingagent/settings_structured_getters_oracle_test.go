package codingagent

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// settings-manager.ts getBranchSummarySettings, getBranchSummarySkipPrompt, getRetryEnabled, getRetrySettings, getProviderRetrySettings,
// getCacheWarmingMode (global file only), getCollapseChangelog, getEnableInstallTelemetry, getEnableAnalytics, getShowCacheMissNotices,
// getQuietStartup, getDefaultProjectTrust (global file only), getDoubleEscapeAction, getTreeFilterMode, getOutputPad, getMermaidRenderingMode,
// getImageWidthCells, getShowTerminalProgress, getTuiMode, getFullscreenExitOutput, getFullscreenScrollbar, getFullscreenCopyOnSelect,
// getFullscreenWheelScrollLines, getEnabledModels, getShellCommandPrefix, getNpmCommand, getLastChangelogVersion and getTrackingId against pinned Pi, over layered
// global and project settings files with enum values valid and invalid, objects merged one level down and keys omitted. Not compared: getters that
// read the environment, wrong-typed values and fractional numbers (PiG's Settings is typed), empty strings (Go cannot tell "" from absent).
func TestSettingsManagerStructuredGettersMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261115))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	fields := []string{
		`"branchSummary":{"reserveTokens":100}`, `"branchSummary":{"skipPrompt":true}`, `"branchSummary":{"reserveTokens":0,"skipPrompt":false}`, `"branchSummary":{}`,
		`"retry":{"enabled":false}`, `"retry":{"maxRetries":7}`, `"retry":{"baseDelayMs":50,"maxAgentDelayMs":900}`, `"retry":{"maxRetries":0}`, `"retry":{"enabled":true,"baseDelayMs":0}`,
		`"retry":{"provider":{"timeoutMs":5000}}`, `"retry":{"provider":{"maxRetries":2,"maxRetryDelayMs":10}}`, `"retry":{"provider":{"maxRetries":0}}`, `"retry":{"provider":{}}`,
		`"cacheWarming":"off"`, `"cacheWarming":"idle"`, `"cacheWarming":"streaming"`, `"cacheWarming":"bogus"`,
		`"collapseChangelog":true`, `"collapseChangelog":false`, `"enableInstallTelemetry":false`, `"enableInstallTelemetry":true`, `"enableAnalytics":true`, `"enableAnalytics":false`,
		`"showCacheMissNotices":true`, `"showCacheMissNotices":false`,
		`"quietStartup":true`, `"quietStartup":false`, `"quietStartup":"header"`, `"quietStartup":"bogus"`,
		`"defaultProjectTrust":"always"`, `"defaultProjectTrust":"never"`, `"defaultProjectTrust":"ask"`, `"defaultProjectTrust":"maybe"`,
		`"doubleEscapeAction":"fork"`, `"doubleEscapeAction":"none"`, `"doubleEscapeAction":"tree"`,
		`"treeFilterMode":"no-tools"`, `"treeFilterMode":"user-only"`, `"treeFilterMode":"labeled-only"`, `"treeFilterMode":"all"`, `"treeFilterMode":"default"`, `"treeFilterMode":"bogus"`,
		`"outputPad":0`, `"outputPad":1`, `"outputPad":2`, `"outputPad":-1`,
		`"markdown":{"mermaid":"off"}`, `"markdown":{"mermaid":"final"}`, `"markdown":{"mermaid":"streaming"}`, `"markdown":{"mermaid":"bogus"}`,
		`"terminal":{"imageWidthCells":0}`, `"terminal":{"imageWidthCells":1}`, `"terminal":{"imageWidthCells":120}`, `"terminal":{"imageWidthCells":-4}`, `"terminal":{"showTerminalProgress":true}`, `"terminal":{"showTerminalProgress":false}`,
		`"tuiMode":"regular"`, `"tuiMode":"fullscreen"`, `"tuiMode":"bogus"`, `"fullscreenExitOutput":"resume-hint"`, `"fullscreenExitOutput":"transcript"`, `"fullscreenExitOutput":"bogus"`,
		`"fullscreenScrollbar":"always"`, `"fullscreenScrollbar":"hidden"`, `"fullscreenScrollbar":"auto"`, `"fullscreenScrollbar":"bogus"`,
		`"fullscreenCopyOnSelect":false`, `"fullscreenCopyOnSelect":true`,
		`"fullscreenWheelScrollLines":0`, `"fullscreenWheelScrollLines":3`, `"fullscreenWheelScrollLines":100`, `"fullscreenWheelScrollLines":1000`, `"fullscreenWheelScrollLines":-5`, `"fullscreenWheelScrollLines":"auto"`, `"fullscreenWheelScrollLines":"many"`,
		`"enabledModels":["a","b*"]`, `"enabledModels":[]`, `"shellCommandPrefix":"set -e"`, `"npmCommand":["npm","--x"]`, `"npmCommand":[]`, `"lastChangelogVersion":"1.2.3"`, `"trackingId":"t-1"`, `"trackingId":"t-2"`,
	}
	content := func() string {
		if rng.Intn(5) == 0 {
			return ""
		}
		var members []string
		for range rng.Intn(7) {
			members = append(members, pick(fields))
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	cases := make([]settingsGettersCase, 2000)
	for i := range cases {
		cases[i] = settingsGettersCase{content(), content()}
	}
	var want []map[string]any
	pioracle.Run(t, `
const { SettingsManager } = await load("pi-coding-agent/core/settings-manager.js");
const { CONFIG_DIR_NAME } = await load("pi-coding-agent/config.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const names = ["getBranchSummarySettings", "getBranchSummarySkipPrompt", "getRetryEnabled", "getRetrySettings", "getProviderRetrySettings", "getCacheWarmingMode", "getCollapseChangelog", "getEnableInstallTelemetry", "getEnableAnalytics", "getShowCacheMissNotices", "getQuietStartup", "getDefaultProjectTrust", "getDoubleEscapeAction", "getTreeFilterMode", "getOutputPad", "getMermaidRenderingMode", "getImageWidthCells", "getShowTerminalProgress", "getTuiMode", "getFullscreenExitOutput", "getFullscreenScrollbar", "getFullscreenCopyOnSelect", "getFullscreenWheelScrollLines", "getEnabledModels", "getShellCommandPrefix", "getNpmCommand", "getLastChangelogVersion", "getTrackingId"];
const out = [];
for (const c of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "settingss-"));
	const agentDir = path.join(root, "agent");
	const cwd = path.join(root, "proj");
	fs.mkdirSync(agentDir); fs.mkdirSync(path.join(cwd, CONFIG_DIR_NAME), { recursive: true });
	if (c.global !== "") fs.writeFileSync(path.join(agentDir, "settings.json"), c.global);
	if (c.project !== "") fs.writeFileSync(path.join(cwd, CONFIG_DIR_NAME, "settings.json"), c.project);
	const settings = SettingsManager.create(cwd, agentDir, { projectTrusted: true });
	const row = {};
	for (const name of names) { const value = settings[name](); row[name] = value === undefined ? null : JSON.parse(JSON.stringify(value)); }
	out.push(row);
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);`, cases, &want)

	failures := 0
	for i, c := range cases {
		root := t.TempDir()
		agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
		_ = os.MkdirAll(agentDir, 0o755)
		_ = os.MkdirAll(ProjectConfigDir(cwd), 0o755)
		if c.Global != "" {
			_ = os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(c.Global), 0o644)
		}
		if c.Project != "" {
			_ = os.WriteFile(filepath.Join(ProjectConfigDir(cwd), "settings.json"), []byte(c.Project), 0o644)
		}
		sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
		branch := sm.GetBranchSummarySettings()
		retry := sm.GetRetrySettings()
		provider := sm.GetProviderRetrySettings()
		optionalInt := func(p *int) any {
			if p == nil {
				return nil
			}
			return float64(*p)
		}
		quiet := any(sm.GetQuietStartup().String())
		switch sm.GetQuietStartup().String() {
		case "true":
			quiet = true
		case "false":
			quiet = false
		}
		wheel := sm.GetFullscreenWheelScrollLines()
		var wheelValue any = wheel.Lines
		if wheel.Auto {
			wheelValue = "auto"
		}
		absent := func(s string) any {
			if s == "" {
				return nil
			}
			return s
		}
		var enabled any
		if models := sm.GetEnabledModels(); models != nil {
			enabled = toAnySlice(models)
		}
		var npm any
		if command := sm.GetNpmCommand(); command != nil {
			npm = toAnySlice(command)
		}
		got := map[string]any{
			"getBranchSummarySettings":      map[string]any{"reserveTokens": float64(branch.ReserveTokens), "skipPrompt": branch.SkipPrompt},
			"getBranchSummarySkipPrompt":    sm.GetBranchSummarySkipPrompt(),
			"getRetryEnabled":               sm.GetRetryEnabled(),
			"getRetrySettings":              map[string]any{"enabled": retry.Enabled, "maxRetries": float64(retry.MaxRetries), "baseDelayMs": float64(retry.BaseDelayMs), "maxAgentDelayMs": float64(retry.MaxAgentDelayMs)},
			"getProviderRetrySettings":      providerRetryJSON(optionalInt(provider.TimeoutMs), optionalInt(provider.MaxRetries), float64(provider.MaxRetryDelayMs)),
			"getCacheWarmingMode":           string(sm.GetCacheWarmingMode()),
			"getCollapseChangelog":          sm.GetCollapseChangelog(),
			"getEnableInstallTelemetry":     sm.GetEnableInstallTelemetry(),
			"getEnableAnalytics":            sm.GetEnableAnalytics(),
			"getShowCacheMissNotices":       sm.GetShowCacheMissNotices(),
			"getQuietStartup":               quiet,
			"getDefaultProjectTrust":        string(sm.GetDefaultProjectTrust()),
			"getDoubleEscapeAction":         sm.GetDoubleEscapeAction(),
			"getTreeFilterMode":             sm.GetTreeFilterMode(),
			"getOutputPad":                  float64(sm.GetOutputPad()),
			"getMermaidRenderingMode":       string(sm.GetMermaidRenderingMode()),
			"getImageWidthCells":            float64(sm.GetImageWidthCells()),
			"getShowTerminalProgress":       sm.GetShowTerminalProgress(),
			"getTuiMode":                    string(sm.GetTuiMode()),
			"getFullscreenExitOutput":       string(sm.GetFullscreenExitOutput()),
			"getFullscreenScrollbar":        sm.GetFullscreenScrollbar(),
			"getFullscreenCopyOnSelect":     sm.GetFullscreenCopyOnSelect(),
			"getFullscreenWheelScrollLines": wheelValue,
			"getEnabledModels":              enabled,
			"getShellCommandPrefix":         absent(sm.GetShellCommandPrefix()),
			"getNpmCommand":                 npm,
			"getLastChangelogVersion":       absent(sm.GetLastChangelogVersion()),
			"getTrackingId":                 absent(sm.GetTrackingID()),
		}
		if !reflect.DeepEqual(got, want[i]) {
			failures++
			if failures <= 5 {
				var diffs []string
				for name, value := range got {
					if !reflect.DeepEqual(value, want[i][name]) {
						diffs = append(diffs, fmt.Sprintf("%s Go %v Pi %v", name, value, want[i][name]))
					}
				}
				data, _ := json.Marshal(c)
				t.Errorf("case %d %s: %s", i, data, strings.Join(diffs, "; "))
			}
		}
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}

func toAnySlice(items []string) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out
}

// providerRetryJSON mirrors what JSON.stringify keeps of Pi's { timeoutMs?, maxRetries?, maxRetryDelayMs }: an undefined member is dropped.
func providerRetryJSON(timeoutMs, maxRetries any, maxRetryDelayMs float64) map[string]any {
	out := map[string]any{"maxRetryDelayMs": maxRetryDelayMs}
	if timeoutMs != nil {
		out["timeoutMs"] = timeoutMs
	}
	if maxRetries != nil {
		out["maxRetries"] = maxRetries
	}
	return out
}

// settings-manager.ts keeps terminal.imageWidthCells as authored when another setting is saved: a 0 or negative number is a value
// (getImageWidthCells reads it as 1), so neither the next write of the global file nor the encoded settings may drop it.
func TestSettingsWriteKeepsAnAuthoredNonPositiveImageWidthCells(t *testing.T) {
	for _, width := range []string{"0", "-4"} {
		root := t.TempDir()
		agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
		_ = os.MkdirAll(agentDir, 0o755)
		file := filepath.Join(agentDir, "settings.json")
		if err := os.WriteFile(file, []byte(`{"terminal":{"imageWidthCells":`+width+`}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
		if err := sm.SetTheme("dark"); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `"imageWidthCells": `+width) {
			t.Errorf("width %s: saved file %s lost terminal.imageWidthCells", width, data)
		}
		encoded, err := json.Marshal(NewSettingsManagerWithProjectTrust(cwd, agentDir, true).GetGlobalSettings())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"imageWidthCells":`+width) {
			t.Errorf("width %s: encoded settings %s lost terminal.imageWidthCells", width, encoded)
		}
		if got := NewSettingsManagerWithProjectTrust(cwd, agentDir, true).GetImageWidthCells(); got != 1 {
			t.Errorf("width %s: GetImageWidthCells after reload = %d, want 1", width, got)
		}
	}
}
