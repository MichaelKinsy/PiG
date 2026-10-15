package codingagent

// pi: packages/coding-agent/src/core/settings-manager.ts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
	"github.com/MichaelKinsy/PiG/tui"
)

type tuiModeProbe struct {
	Global  string `json:"global"`
	Project string `json:"project"`
	Set     string `json:"set"`
}

type tuiModeResult struct {
	Mode   string `json:"mode"`
	Exit   string `json:"exit"`
	Global string `json:"global"`
}

// settings-manager.ts getTuiMode and getFullscreenExitOutput: "regular" (or "resume-hint") only when the merged setting
// holds exactly that string, any other value (wrong case, empty, null, a number, boolean, array or object) gives the
// default; a project string overrides the global value even when it is not a valid choice; setTuiMode writes the global
// file. The project file is trusted. A global value of the wrong type or empty reads as the default in both. A project
// value that is empty or of the wrong type overrides the global one in Pi but is dropped by Pig: D158 for the wrong
// type, and an empty project string is an open gap shared by every enum string setting (settings.go mergeSettings
// tests each against "" and only theme, defaultProvider, defaultModel, shellPath and shellCommandPrefix keep a present
// empty string).
func TestSettingsTuiModeAndFullscreenExitOutputMatchPi(t *testing.T) {
	values := []string{"", `"regular"`, `"fullscreen"`, `"Regular"`, `""`, `" regular"`, `null`, `5`, `true`, `[]`, `["regular"]`, `{}`, `"resume-hint"`, `"transcript"`, `"Resume-Hint"`, `"resume-hint "`}
	wrap := func(tuiValue, exitValue string) string {
		members := ""
		if tuiValue != "" {
			members += `"tuiMode":` + tuiValue
		}
		if exitValue != "" {
			if members != "" {
				members += ","
			}
			members += `"fullscreenExitOutput":` + exitValue
		}
		return "{" + members + "}"
	}
	var projectValues []string
	for _, value := range values {
		if value != "" && value != `""` && value != "null" && value != "5" && value != "true" && value != "[]" && value != `["regular"]` && value != "{}" {
			projectValues = append(projectValues, value)
		}
	}
	projectValues = append(projectValues, "")
	var probes []tuiModeProbe
	for _, g := range values {
		for _, p := range projectValues {
			probes = append(probes, tuiModeProbe{Global: wrap(g, g), Project: wrap(p, p)}, tuiModeProbe{Global: wrap(g, p), Project: wrap(p, p)})
		}
	}
	probes = append(probes, tuiModeProbe{Global: "{}", Project: "{}", Set: "regular"}, tuiModeProbe{Global: `{"tuiMode":"regular"}`, Project: "{}", Set: "fullscreen"}, tuiModeProbe{Global: `{"tuiMode":5,"x":1}`, Project: `{"tuiMode":"regular"}`, Set: "fullscreen"})
	var want []tuiModeResult
	pioracle.Run(t, `
const { SettingsManager } = await load("pi-coding-agent/core/settings-manager.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const p of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "tuimode-"));
	const agentDir = path.join(root, "agent");
	const cwd = path.join(root, "proj");
	fs.mkdirSync(agentDir); fs.mkdirSync(path.join(cwd, ".pig"), { recursive: true });
	fs.writeFileSync(path.join(agentDir, "settings.json"), p.global);
	fs.writeFileSync(path.join(cwd, ".pig", "settings.json"), p.project);
	const settings = SettingsManager.create(cwd, agentDir);
	const result = { mode: settings.getTuiMode(), exit: settings.getFullscreenExitOutput(), global: "" };
	if (p.set !== "") { settings.setTuiMode(p.set); await settings.flush(); result.mode = settings.getTuiMode(); }
	result.global = fs.readFileSync(path.join(agentDir, "settings.json"), "utf8");
	out.push(result);
}
emit(out);`, probes, &want)
	bad := 0
	for i, p := range probes {
		root := t.TempDir()
		agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
		if err := os.Mkdir(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(agentDir, "settings.json")
		if err := os.WriteFile(file, []byte(p.Global), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cwd, ".pig", "settings.json"), []byte(p.Project), 0o600); err != nil {
			t.Fatal(err)
		}
		sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
		got := tuiModeResult{Mode: string(sm.GetTuiMode()), Exit: string(sm.GetFullscreenExitOutput())}
		if p.Set != "" {
			if err := sm.SetTuiMode(tui.TuiMode(p.Set)); err != nil {
				t.Fatal(err)
			}
			got.Mode = string(sm.GetTuiMode())
		}
		written, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		got.Global = string(written)
		if got != want[i] {
			if bad++; bad <= 30 {
				t.Errorf("global %s project %s set %q:\n  Pig %+v\n  Pi  %+v", p.Global, p.Project, p.Set, got, want[i])
			}
		}
	}
	if bad > 30 {
		t.Errorf("%d of %d probes differ from Pi", bad, len(probes))
	}
}
