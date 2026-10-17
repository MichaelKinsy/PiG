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

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// settings-manager.ts getCompactionSettings (with and without a model), getCompactionReserveTokens, getCompactionKeepRecentTokens, getShellPath,
// getSessionDir, getThemeSetting, getTheme and getModelThinkingLevel against pinned Pi, over layered global and project files: a model override beats
// the ordinary token setting beats the built-in default, an invalid authored token value throws (compared by message), a path setting goes through
// normalizePath (home expansion, file URL), and a theme with "/" is a path, not a name. Not compared: wrong-typed values and fractional numbers other
// than in the compaction token settings (PiG keeps those as JavaScript numbers), and empty strings (PiG's string cannot tell "" from absent).
func TestSettingsManagerResolvedGettersMatchPi(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	rng := rand.New(rand.NewSource(20261117))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	fields := []string{
		`"compaction":{"enabled":false}`, `"compaction":{"reserveTokens":100}`, `"compaction":{"keepRecentTokens":7}`, `"compaction":{"reserveTokens":0,"keepRecentTokens":0}`,
		`"compaction":{"reserveTokens":-1}`, `"compaction":{"keepRecentTokens":2.5}`, `"compaction":{"reserveTokens":9007199254740993}`, `"compaction":{"reserveTokens":9007199254740991}`,
		`"compaction":{"reserveTokens":1e3}`, `"compaction":{"keepRecentTokens":-0}`,
		`"compaction":{"modelOverrides":{"p/m":{"reserveTokens":11}}}`, `"compaction":{"modelOverrides":{"p/m":{"keepRecentTokens":22,"reserveTokens":0}}}`,
		`"compaction":{"reserveTokens":5,"modelOverrides":{"p/m":{"keepRecentTokens":3},"q/n":{"reserveTokens":-4}}}`, `"compaction":{"modelOverrides":{"q/n":{"reserveTokens":-4}}}`,
		`"compaction":{"modelOverrides":{"p/m":{"reserveTokens":1.5}}}`, `"compaction":{"modelOverrides":{"p/m":{}}}`, `"compaction":{"modelOverrides":{"p/m":{"reserveTokens":9007199254740993}}}`,
		`"compaction":{"modelOverrides":{"p/m":7}}`, `"compaction":{"modelOverrides":{"p/m":null}}`, `"compaction":{"modelOverrides":{"p/m":[1]}}`,
		`"shellPath":"/bin/sh"`, `"shellPath":"~/sh"`, `"shellPath":"~"`, `"shellPath":"file:///opt/sh"`, `"shellPath":"file:///a%20b/sh"`, `"shellPath":"./rel/sh"`, `"shellPath":"sh"`,
		`"shellPath":"file://host/x"`, `"shellPath":"file:///a%zz"`, `"shellPath":"~other/x"`,
		`"sessionDir":"/s"`, `"sessionDir":"~/sessions"`, `"sessionDir":"file:///tmp/s%20x"`, `"sessionDir":"rel/dir"`, `"sessionDir":"~"`, `"sessionDir":"file://remote/s"`,
		`"theme":"dark"`, `"theme":"light"`, `"theme":"./themes/x.json"`, `"theme":"/abs/theme.json"`, `"theme":"my theme"`, `"theme":null`, `"theme":5`,
		`"modelThinkingLevels":{"p/m":"high"}`, `"modelThinkingLevels":{"p/m":"low","a/b":"off"}`, `"modelThinkingLevels":{"a/b":"minimal"}`,
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
	cases := make([]settingsGettersCase, 3000)
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
const model = { provider: "p", id: "m" };
const other = { provider: "q", id: "n" };
const out = [];
for (const c of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "settingsr-"));
	const agentDir = path.join(root, "agent");
	const cwd = path.join(root, "proj");
	fs.mkdirSync(agentDir); fs.mkdirSync(path.join(cwd, CONFIG_DIR_NAME), { recursive: true });
	if (c.global !== "") fs.writeFileSync(path.join(agentDir, "settings.json"), c.global);
	if (c.project !== "") fs.writeFileSync(path.join(cwd, CONFIG_DIR_NAME, "settings.json"), c.project);
	const settings = SettingsManager.create(cwd, agentDir, { projectTrusted: true });
	const row = {};
	const probe = (name, fn) => {
		try { const value = fn(); row[name] = value === undefined ? null : JSON.parse(JSON.stringify(value)); }
		catch (e) { row[name] = { error: e.message }; }
	};
	probe("compaction", () => settings.getCompactionSettings());
	probe("compactionP", () => settings.getCompactionSettings(model));
	probe("compactionQ", () => settings.getCompactionSettings(other));
	probe("reserveP", () => settings.getCompactionReserveTokens(model));
	probe("keepP", () => settings.getCompactionKeepRecentTokens(model));
	probe("shellPath", () => settings.getShellPath());
	probe("sessionDir", () => settings.getSessionDir());
	probe("themeSetting", () => settings.getThemeSetting());
	probe("theme", () => settings.getTheme());
	probe("levelP", () => settings.getModelThinkingLevel("p", "m"));
	probe("levelAB", () => settings.getModelThinkingLevel("a", "b"));
	out.push(row);
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);`, cases, &want)

	modelP := &ai.Model{ID: "m", ProviderMeta: ai.ProviderMetadata{ProviderID: "p"}}
	modelQ := &ai.Model{ID: "n", ProviderMeta: ai.ProviderMetadata{ProviderID: "q"}}
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
		compaction := func(m ...*ai.Model) any {
			config, err := sm.GetCompactionSettings(m...)
			if err != nil {
				return map[string]any{"error": err.Error()}
			}
			return map[string]any{"enabled": config.Enabled, "reserveTokens": float64(config.ReserveTokens), "keepRecentTokens": float64(config.KeepRecentTokens)}
		}
		count := func(n int, err error) any {
			if err != nil {
				return map[string]any{"error": err.Error()}
			}
			return float64(n)
		}
		path := func(p string, err error) any {
			if err != nil {
				return map[string]any{"error": err.Error()}
			}
			if p == "" {
				return nil
			}
			return p
		}
		absent := func(s string) any {
			if s == "" {
				return nil
			}
			return s
		}
		var themeSetting any
		if theme := sm.GetThemeSetting(); theme != nil {
			themeSetting = *theme
		}
		reserve, reserveErr := sm.GetCompactionReserveTokens(modelP)
		keep, keepErr := sm.GetCompactionKeepRecentTokens(modelP)
		shellPath, shellErr := sm.GetShellPath()
		sessionDir, sessionErr := sm.GetSessionDir()
		got := map[string]any{
			"compaction": compaction(), "compactionP": compaction(modelP), "compactionQ": compaction(modelQ),
			"reserveP": count(reserve, reserveErr), "keepP": count(keep, keepErr),
			"shellPath": path(shellPath, shellErr), "sessionDir": path(sessionDir, sessionErr),
			"themeSetting": themeSetting, "theme": absent(sm.GetTheme()),
			"levelP": absent(string(sm.GetModelThinkingLevel("p", "m"))), "levelAB": absent(string(sm.GetModelThinkingLevel("a", "b"))),
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
