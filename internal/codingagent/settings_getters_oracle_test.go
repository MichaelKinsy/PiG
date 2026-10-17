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

type settingsGettersCase struct {
	Global  string `json:"global"`
	Project string `json:"project"`
}

// settings-manager.ts getTransport, getSteeringMode, getFollowUpMode, getDefaultThinkingLevel, getTheme, getDefaultProvider, getDefaultModel,
// getHideThinkingBlock, getCompactionEnabled, getImageAutoResize, getBlockImages, getShowImages, getEditorPaddingX, getAutocompleteMaxVisible,
// getCodeBlockIndent and getEnableSkillCommands against pinned Pi: over global and project settings files that set, omit or conflict on each key
// (project over global, nested terminal/images/compaction/markdown/skills objects merged one level down), every getter returns what Pi's returns, defaults
// included. Not compared: getters that read the environment (hardware cursor, clear on shrink), wrong-typed values and fractional numbers (PiG's Settings is typed:
// integers and strings).
func TestSettingsManagerGettersMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(20261114))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	fields := []string{
		`"transport":"sse"`, `"transport":"websocket"`, `"transport":"auto"`, `"transport":"bogus"`,
		`"steeringMode":"all"`, `"steeringMode":"one-at-a-time"`, `"steeringMode":"x"`, `"followUpMode":"all"`, `"followUpMode":"one-at-a-time"`, `"followUpMode":"y"`,
		`"defaultThinkingLevel":"high"`, `"defaultThinkingLevel":"off"`, `"defaultThinkingLevel":"bogus"`, `"defaultThinkingLevel":"xhigh"`,
		`"theme":"dark"`, `"theme":"light"`, `"theme":"my custom"`, `"defaultProvider":"anthropic"`, `"defaultProvider":"界"`, `"defaultModel":"m1"`, `"defaultModel":"a/b:c"`,
		`"hideThinkingBlock":true`, `"hideThinkingBlock":false`,
		`"compaction":{"enabled":false}`, `"compaction":{"enabled":true,"reserveTokens":10}`, `"compaction":{"reserveTokens":5}`,
		`"images":{"autoResize":false}`, `"images":{"blockImages":true}`, `"images":{"autoResize":true,"blockImages":false}`,
		`"terminal":{"showImages":false}`, `"terminal":{"showImages":true,"imageWidthCells":30}`, `"terminal":{"imageWidthCells":10}`,
		`"editorPaddingX":2`, `"editorPaddingX":0`, `"editorPaddingX":7`, `"editorPaddingX":-1`,
		`"autocompleteMaxVisible":3`, `"autocompleteMaxVisible":20`, `"autocompleteMaxVisible":0`, `"autocompleteMaxVisible":1`,
		`"markdown":{"codeBlockIndent":"    "}`, `"markdown":{"codeBlockIndent":"\t"}`, `"markdown":{"codeBlockIndent":""}`, `"markdown":{"codeBlockIndent":null}`, `"markdown":{}`,
		`"skills":{"enableSkillCommands":false}`, `"skills":{"enableSkillCommands":true}`, `"skills":{"customDirectories":["/a"]}`,
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
	cases := make([]settingsGettersCase, 1500)
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
const names = ["getTransport", "getSteeringMode", "getFollowUpMode", "getDefaultThinkingLevel", "getTheme", "getDefaultProvider", "getDefaultModel", "getHideThinkingBlock", "getCompactionEnabled", "getImageAutoResize", "getBlockImages", "getShowImages", "getEditorPaddingX", "getAutocompleteMaxVisible", "getCodeBlockIndent", "getEnableSkillCommands"];
const out = [];
for (const c of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "settingsg-"));
	const agentDir = path.join(root, "agent");
	const cwd = path.join(root, "proj");
	fs.mkdirSync(agentDir); fs.mkdirSync(path.join(cwd, CONFIG_DIR_NAME), { recursive: true });
	if (c.global !== "") fs.writeFileSync(path.join(agentDir, "settings.json"), c.global);
	if (c.project !== "") fs.writeFileSync(path.join(cwd, CONFIG_DIR_NAME, "settings.json"), c.project);
	const settings = SettingsManager.create(cwd, agentDir, { projectTrusted: true });
	const row = {};
	for (const name of names) { const value = settings[name](); row[name] = value === undefined ? null : value; }
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
		absent := func(s string) any {
			if s == "" {
				return nil
			}
			return s
		}
		got := map[string]any{
			"getTransport": sm.GetTransport(), "getSteeringMode": sm.GetSteeringMode(), "getFollowUpMode": sm.GetFollowUpMode(),
			"getDefaultThinkingLevel": absent(string(sm.GetDefaultThinkingLevel())), "getTheme": absent(sm.GetTheme()),
			"getDefaultProvider": absent(sm.GetDefaultProvider()), "getDefaultModel": absent(sm.GetDefaultModel()),
			"getHideThinkingBlock": sm.GetHideThinkingBlock(), "getCompactionEnabled": sm.GetCompactionEnabled(), "getImageAutoResize": sm.GetImageAutoResize(),
			"getBlockImages": sm.GetBlockImages(), "getShowImages": sm.GetShowImages(), "getEditorPaddingX": float64(sm.GetEditorPaddingX()),
			"getAutocompleteMaxVisible": float64(sm.GetAutocompleteMaxVisible()), "getCodeBlockIndent": sm.GetCodeBlockIndent(), "getEnableSkillCommands": sm.GetEnableSkillCommands(),
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
