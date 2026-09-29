package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// themePresenceProjectValues are project theme values that are not strings. Pi 0.87.1 settings-manager.ts:169-186 lets every project key whose value is not undefined replace the global value, and settings-manager.ts:782-786 reads only a string theme.
var themePresenceProjectValues = []string{`{"theme":null}`, `{"theme":5}`, `{"theme":false}`, `{"theme":["light"]}`, `{"theme":{"name":"light"}}`}

func writeThemeLayers(t *testing.T, global, project string) (cwd, agentDir string) {
	t.Helper()
	cwd, agentDir = t.TempDir(), t.TempDir()
	writeSettingsFixture(t, filepath.Join(agentDir, "settings.json"), global)
	if project != "" {
		writeSettingsFixture(t, filepath.Join(ProjectConfigDir(cwd), "settings.json"), project)
		writeSettingsFixture(t, filepath.Join(cwd, ".pi", "settings.json"), project)
	}
	return cwd, agentDir
}

func storedTheme(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return string(object["theme"])
}

// A project null or other non-string theme replaces the global fixed theme, so the merged setting is unset and automatic detection applies. A later global write does not bring the fixed theme back through the project layer.
func TestSettingsProjectNonStringThemeClearsGlobalTheme(t *testing.T) {
	for _, project := range themePresenceProjectValues {
		t.Run(project, func(t *testing.T) {
			cwd, agentDir := writeThemeLayers(t, `{"theme":"light"}`, project)
			manager := NewSettingsManager(cwd, agentDir)
			if got := manager.GetThemeSetting(); got != nil {
				t.Fatalf("theme setting = %q, want unset: the project value replaces the global theme", *got)
			}
			if got := manager.GetTheme(); got != "" {
				t.Fatalf("fixed theme = %q, want none", got)
			}
			assertThemeField(t, manager.GetGlobalSettings(), new("light"))
			if err := manager.SetTheme("dark"); err != nil {
				t.Fatal(err)
			}
			if got := manager.GetThemeSetting(); got != nil {
				t.Fatalf("theme setting after a global write = %q, want unset", *got)
			}
			if got := storedTheme(t, filepath.Join(agentDir, "settings.json")); got != `"dark"` {
				t.Fatalf("global theme = %s, want \"dark\"", got)
			}
			projectPath := filepath.Join(ProjectConfigDir(cwd), "settings.json")
			if data, err := os.ReadFile(projectPath); err != nil || string(data) != project {
				t.Fatalf("project settings = %s err=%v, want the original %s", data, err, project)
			}
		})
	}
}

// Real Pi 0.87.1 SettingsManager reads the same layers to the same theme setting as PiG.
func TestSettingsThemeLayerPresenceMatchesPi(t *testing.T) {
	projects := append([]string{"", `{}`, `{"theme":""}`, `{"theme":"dark"}`}, themePresenceProjectValues...)
	type result struct {
		Present bool   `json:"present"`
		Value   string `json:"value"`
	}
	var pi, got []result
	var args []string
	for _, project := range projects {
		cwd, agentDir := writeThemeLayers(t, `{"theme":"light"}`, project)
		args = append(args, agentDir, cwd)
		setting := NewSettingsManager(cwd, agentDir).GetThemeSetting()
		if setting == nil {
			got = append(got, result{})
		} else {
			got = append(got, result{Present: true, Value: *setting})
		}
	}
	cmd := piDirectoryNode(t, `
const dirs = process.argv.slice(2), results = [];
for (let i = 0; i < dirs.length; i += 2) {
  const value = SettingsManager.create(dirs[i + 1], dirs[i]).getThemeSetting();
  results.push({present: value !== undefined, value: value ?? ""});
}
console.log(JSON.stringify(results));
`, args...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi: %v\n%s", err, output)
	}
	if err := json.Unmarshal(output, &pi); err != nil || len(pi) != len(projects) {
		t.Fatalf("Pi: %s: %v", output, err)
	}
	for i, project := range projects {
		if got[i] != pi[i] {
			t.Errorf("project %s: PiG theme setting %+v, Pi %+v", project, got[i], pi[i])
		}
	}
	t.Logf("Pi theme settings over global light: %+v", pi)
}
