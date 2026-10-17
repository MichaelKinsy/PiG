package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Wrong-typed values and keys outside PiG's typed Settings are not compared: Pi keeps any JSON value in memory, PiG's Settings struct keeps its typed fields.
// pi: packages/coding-agent/src/core/settings-manager.ts create / tryLoadFromStorage / migrateSettings / deepMergeSettings. A settings.json with legacy keys, wrong-typed values, a BOM, invalid JSON or a non-object value loads the same layers, merged view and errors.
func TestSettingsLoadMatchesPiOnCorruptedFiles(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required: ", err)
	}
	rng := rand.New(rand.NewSource(20261010))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	fields := []string{
		`"theme":"dark"`, `"defaultProvider":"anthropic"`, `"defaultModel":"m"`, `"defaultThinkingLevel":"high"`, `"defaultThinkingLevel":"bogus"`,
		`"steeringMode":"all"`, `"queueMode":"one-at-a-time"`, `"followUpMode":"one-at-a-time"`, `"websockets":true`, `"websockets":false`, `"transport":"sse"`,
		`"skills":{"enableSkillCommands":false,"customDirectories":["/a","/b"]}`, `"skills":["/x"]`,
		`"retry":{"enabled":true,"maxRetries":5,"maxDelayMs":9000}`, `"retry":{"maxDelayMs":5,"provider":{"maxRetryDelayMs":7}}`, `"retry":{"provider":null,"maxDelayMs":3}`,
		`"compaction":{"enabled":false,"reserveTokens":100}`,
		`"enabledModels":["a","b"]`, `"extensions":["/e"]`, `"packages":["npm:x",{"source":"git:y","skills":["s"]}]`, `"editorPaddingX":2`,
		`"hideThinkingBlock":true`, `"images":{"autoResize":false,"blockImages":true}`, `"terminal":{"showImages":false,"imageWidthCells":40}`, `"markdown":{"codeBlockIndent":"    "}`,
		`"lastChangelogVersion":"1.0.0"`, `"shellPath":"/bin/sh"`, `"thinkingBudgets":{"low":10}`, `"sessionDir":"/s"`,
	}
	content := func() *string {
		switch rng.Intn(14) {
		case 0:
			return nil
		case 1:
			s := pick([]string{"", "{", "}", "null", "[]", "1", `"s"`, "true", "[1,2]", "nope", "{}", "{ }\n", `{"a":}`, "\ufeff", "\ufeff{}"})
			return &s
		}
		var members []string
		for range 1 + rng.Intn(6) {
			members = append(members, pick(fields))
		}
		text := "{" + strings.Join(members, ",") + "}"
		if rng.Intn(10) == 0 {
			text = "\ufeff" + text
		}
		return &text
	}
	type scopes struct {
		Global  *string `json:"global"`
		Project *string `json:"project"`
	}
	var cases []scopes
	for range 1500 {
		cases = append(cases, scopes{content(), content()})
	}
	in, _ := json.Marshal(cases)
	cmd := exec.CommandContext(t.Context(), "node", "testdata/settings_load.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	type piErr struct{ Scope, Message, Path string }
	var want []struct {
		Global, Project, Merged string
		Errors                  []piErr
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	parse := func(s string) any {
		var v any
		_ = json.Unmarshal([]byte(s), &v)
		return v
	}
	var differences []string
	for i, c := range cases {
		agentDir, cwd := filepath.Join(base, fmt.Sprint(i), "agent"), filepath.Join(base, fmt.Sprint(i), "work")
		_ = os.MkdirAll(agentDir, 0o755)
		_ = os.MkdirAll(ProjectConfigDir(cwd), 0o755)
		if c.Global != nil {
			_ = os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(*c.Global), 0o644)
		}
		if c.Project != nil {
			_ = os.WriteFile(filepath.Join(ProjectConfigDir(cwd), "settings.json"), []byte(*c.Project), 0o644)
		}
		sm := NewSettingsManager(cwd, agentDir)
		encode := func(s Settings) any { b, _ := json.Marshal(s); return parse(string(b)) }
		var gotErrors []piErr
		for _, e := range sm.DrainErrors() {
			gotErrors = append(gotErrors, piErr{e.Scope, e.Error.Error(), strings.Replace(strings.Replace(e.Path, base, "$BASE", 1), string(filepath.Separator)+".pig"+string(filepath.Separator), string(filepath.Separator)+".pi"+string(filepath.Separator), 1)})
		}
		desc := func(name string, got, w any) {
			// A key Pi holds that Go's typed Settings does not model (a leftover legacy key, null) is not compared.
			if object, ok := w.(map[string]any); ok {
				projected := map[string]any{}
				for key, value := range object {
					if _, modeled := got.(map[string]any)[key]; modeled || settingsLoadComparedKeys[key] {
						projected[key] = value
					}
				}
				w = projected
			} else if _, ok := w.([]any); ok {
				w = map[string]any{}
			}
			if !reflect.DeepEqual(got, w) {
				differences = append(differences, fmt.Sprintf("%s: global %q project %q\n  go: %v\n  pi: %v", name, settingsLoadOracleText(c.Global), settingsLoadOracleText(c.Project), got, w))
			}
		}
		desc("global", encode(sm.GetGlobalSettings()), parse(want[i].Global))
		desc("project", encode(sm.GetProjectSettings()), parse(want[i].Project))
		desc("merged", encode(sm.GetSettings()), parse(want[i].Merged))
		if len(gotErrors) != len(want[i].Errors) || len(gotErrors) > 0 && !reflect.DeepEqual(gotErrors, want[i].Errors) {
			differences = append(differences, fmt.Sprintf("errors: global %q project %q\n  go: %v\n  pi: %v", settingsLoadOracleText(c.Global), settingsLoadOracleText(c.Project), gotErrors, want[i].Errors))
		}
	}
	if len(differences) > 0 {
		shown := differences
		if len(shown) > 12 {
			shown = shown[:12]
		}
		t.Errorf("%d differences from Pi in %d cases:\n%s", len(differences), len(cases), strings.Join(shown, "\n"))
	}
}

// settingsLoadComparedKeys are keys PiG models: if Pi holds one, PiG must hold it too. The legacy queueMode and websockets keys are not listed: Pi keeps one only
// when the key it migrates to is already set, and no Pi reader uses the leftover.
var settingsLoadComparedKeys = map[string]bool{
	"theme": true, "defaultProvider": true, "defaultModel": true, "defaultThinkingLevel": true, "steeringMode": true, "followUpMode": true, "transport": true, "retry": true,
	"enabledModels": true, "extensions": true, "packages": true, "hideThinkingBlock": true, "skills": true, "enableSkillCommands": true, "images": true, "terminal": true,
	"markdown": true, "thinkingBudgets": true, "shellPath": true, "editorPaddingX": true, "compaction": true, "sessionDir": true, "lastChangelogVersion": true,
}

func settingsLoadOracleText(s *string) string {
	if s == nil {
		return "<absent>"
	}
	return *s
}
