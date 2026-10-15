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

// settings-manager.ts getPackages, getExtensionPaths, getSkillPaths, getPromptTemplatePaths, getThemePaths, getAllModelThinkingLevels, getThinkingBudgets,
// getTerminalCapabilityOverrides, getWarnings (compared as the consumer reads it: anthropicExtraUsage !== false), getHttpIdleTimeoutMs,
// getWebSocketConnectTimeoutMs (a throw is compared by message) and getExternalEditorCommand (VISUAL and EDITOR cleared) against pinned Pi, over layered
// global and project files: project arrays replace global ones, nested objects merge a key at a time. Not compared: wrong-typed array members, fractional
// numbers (PiG's Settings is typed) and a thrown value that is not a number or string.
func TestSettingsManagerCollectionGettersMatchPi(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	rng := rand.New(rand.NewSource(20261116))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	fields := []string{
		`"packages":["npm:a","git:b"]`, `"packages":[]`, `"packages":[{"source":"npm:x","extensions":["e"]}]`, `"packages":[{"source":"git:y"},"npm:z"]`,
		`"packages":[{"source":"p","autoload":false,"skills":["s"],"prompts":[],"themes":["t"]}]`,
		`"extensions":["/a","./b"]`, `"extensions":[]`, `"skills":["/s1"]`, `"skills":[]`, `"prompts":["/p"]`, `"prompts":["a","b"]`, `"themes":["/t"]`, `"themes":[]`,
		`"modelThinkingLevels":{"a/b":"high"}`, `"modelThinkingLevels":{"a/b":"low","c/d":"off"}`, `"modelThinkingLevels":{}`,
		`"thinkingBudgets":{"minimal":100}`, `"thinkingBudgets":{"low":5,"high":9000}`, `"thinkingBudgets":{"medium":0}`, `"thinkingBudgets":{}`,
		`"terminal":{"images":"kitty"}`, `"terminal":{"images":"iterm2"}`, `"terminal":{"images":false}`, `"terminal":{"images":true}`, `"terminal":{"images":"bogus"}`,
		`"terminal":{"trueColor":true}`, `"terminal":{"trueColor":false}`, `"terminal":{"hyperlinks":true}`, `"terminal":{"hyperlinks":false}`, `"terminal":{"showImages":false}`,
		`"warnings":{"anthropicExtraUsage":false}`, `"warnings":{"anthropicExtraUsage":true}`, `"warnings":{}`,
		`"httpIdleTimeoutMs":0`, `"httpIdleTimeoutMs":5000`, `"httpIdleTimeoutMs":-1`, `"httpIdleTimeoutMs":"disabled"`, `"httpIdleTimeoutMs":" DISABLED "`, `"httpIdleTimeoutMs":"7000"`,
		`"httpIdleTimeoutMs":"abc"`, `"httpIdleTimeoutMs":""`, `"httpIdleTimeoutMs":"  "`, `"httpIdleTimeoutMs":"-3"`, `"httpIdleTimeoutMs":"1e3"`, `"httpIdleTimeoutMs":true`, `"httpIdleTimeoutMs":null`,
		`"websocketConnectTimeoutMs":0`, `"websocketConnectTimeoutMs":1500`, `"websocketConnectTimeoutMs":-1`, `"websocketConnectTimeoutMs":"disabled"`, `"websocketConnectTimeoutMs":"2500"`,
		`"websocketConnectTimeoutMs":"abc"`, `"websocketConnectTimeoutMs":null`, `"websocketConnectTimeoutMs":true`,
		`"externalEditor":"vim"`, `"externalEditor":"code -w"`, `"externalEditor":"  "`, `"externalEditor":""`,
	}
	content := func() string {
		if rng.Intn(5) == 0 {
			return ""
		}
		var members []string
		for range rng.Intn(6) {
			members = append(members, pick(fields))
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	cases := make([]settingsGettersCase, 2500)
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
const names = ["getPackages", "getExtensionPaths", "getSkillPaths", "getPromptTemplatePaths", "getThemePaths", "getAllModelThinkingLevels", "getThinkingBudgets", "getTerminalCapabilityOverrides", "getWarnings", "getHttpIdleTimeoutMs", "getWebSocketConnectTimeoutMs", "getExternalEditorCommand"];
const out = [];
for (const c of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "settingsc-"));
	const agentDir = path.join(root, "agent");
	const cwd = path.join(root, "proj");
	fs.mkdirSync(agentDir); fs.mkdirSync(path.join(cwd, CONFIG_DIR_NAME), { recursive: true });
	if (c.global !== "") fs.writeFileSync(path.join(agentDir, "settings.json"), c.global);
	if (c.project !== "") fs.writeFileSync(path.join(cwd, CONFIG_DIR_NAME, "settings.json"), c.project);
	const settings = SettingsManager.create(cwd, agentDir, { projectTrusted: true });
	const row = {};
	for (const name of names) {
		try {
			let value = settings[name]();
			if (name === "getWarnings") value = value.anthropicExtraUsage !== false;
			row[name] = value === undefined ? null : JSON.parse(JSON.stringify(value));
		} catch (e) { row[name] = { error: e.message }; }
	}
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
		list := func(items []string) any { return toAnySlice(append([]string{}, items...)) }
		packages := []any{}
		for _, p := range sm.GetPackages() {
			if !p.WasObject {
				packages = append(packages, p.Source)
				continue
			}
			object := map[string]any{"source": p.Source}
			if p.Autoload != nil {
				object["autoload"] = *p.Autoload
			}
			for key, items := range map[string][]string{"extensions": p.Extensions, "skills": p.Skills, "prompts": p.Prompts, "themes": p.Themes} {
				if items != nil {
					object[key] = list(items)
				}
			}
			packages = append(packages, object)
		}
		levels := map[string]any{}
		for key, level := range sm.GetAllModelThinkingLevels() {
			levels[key] = string(level)
		}
		var budgets any
		if b := sm.GetThinkingBudgets(); b != nil {
			object := map[string]any{}
			for key, value := range map[string]*int{"minimal": b.Minimal, "low": b.Low, "medium": b.Medium, "high": b.High} {
				if value != nil {
					object[key] = float64(*value)
				}
			}
			budgets = object
		}
		capabilities := map[string]any{}
		overrides := sm.GetTerminalCapabilityOverrides()
		if overrides.Images != nil {
			capabilities["images"] = nil
			if *overrides.Images != "" {
				capabilities["images"] = string(*overrides.Images)
			}
		}
		if overrides.TrueColor != nil {
			capabilities["trueColor"] = *overrides.TrueColor
		}
		if overrides.Hyperlinks != nil {
			capabilities["hyperlinks"] = *overrides.Hyperlinks
		}
		failure := func(err error) any { return map[string]any{"error": err.Error()} }
		var httpIdle, websocket any
		if ms, err := sm.GetHttpIdleTimeoutMs(); err != nil {
			httpIdle = failure(err)
		} else {
			httpIdle = float64(ms)
		}
		if ms, err := sm.GetWebSocketConnectTimeoutMs(); err != nil {
			websocket = failure(err)
		} else if ms != nil {
			websocket = float64(*ms)
		}
		got := map[string]any{
			"getPackages": packages, "getExtensionPaths": list(sm.GetExtensionPaths()), "getSkillPaths": list(sm.GetSkillPaths()),
			"getPromptTemplatePaths": list(sm.GetPromptTemplatePaths()), "getThemePaths": list(sm.GetThemePaths()),
			"getAllModelThinkingLevels": levels, "getThinkingBudgets": budgets, "getTerminalCapabilityOverrides": capabilities,
			"getWarnings": sm.GetWarnings().AnthropicExtraUsage, "getHttpIdleTimeoutMs": httpIdle, "getWebSocketConnectTimeoutMs": websocket,
			"getExternalEditorCommand": sm.GetExternalEditorCommand(),
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

// settings-manager.ts keeps an authored `"httpIdleTimeoutMs": null` and `"externalEditor": ""` as defined values (the getters read them: the first
// throws, the second falls back to VISUAL, EDITOR or the platform editor), so the encoded settings must keep both keys.
func TestSettingsEncodeKeepsAnAuthoredNullHTTPIdleTimeoutAndEmptyExternalEditor(t *testing.T) {
	root := t.TempDir()
	agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
	_ = os.MkdirAll(agentDir, 0o755)
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"httpIdleTimeoutMs":null,"externalEditor":""}`), 0o644); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(NewSettingsManagerWithProjectTrust(cwd, agentDir, true).GetGlobalSettings())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"httpIdleTimeoutMs":null`, `"externalEditor":""`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("encoded settings %s lost %s", encoded, want)
		}
	}
}
