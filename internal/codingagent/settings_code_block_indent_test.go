package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settings-manager.ts getCodeBlockIndent is `markdown?.codeBlockIndent ?? "  "`: an authored "" is a defined value (no indent) that survives load and
// write, and an authored null in the project layer overrides the global value and reads the default.
func TestSettingsKeepAnAuthoredEmptyOrNullCodeBlockIndent(t *testing.T) {
	for _, tc := range []struct {
		name, global, project, want, keeps, merged string
	}{
		{"empty", `{"markdown":{"codeBlockIndent":""}}`, "", "", `"codeBlockIndent":""`, `"codeBlockIndent":""`},
		{"project empty over global", `{"markdown":{"codeBlockIndent":"\t"}}`, `{"markdown":{"codeBlockIndent":""}}`, "", "", `"codeBlockIndent":""`},
		{"project null over global", `{"markdown":{"codeBlockIndent":"\t"}}`, `{"markdown":{"codeBlockIndent":null}}`, "  ", "", `"codeBlockIndent":null`},
		{"global null", `{"markdown":{"codeBlockIndent":null}}`, "", "  ", `"codeBlockIndent":null`, `"codeBlockIndent":null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
			_ = os.MkdirAll(agentDir, 0o755)
			_ = os.MkdirAll(ProjectConfigDir(cwd), 0o755)
			_ = os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(tc.global), 0o644)
			if tc.project != "" {
				_ = os.WriteFile(filepath.Join(ProjectConfigDir(cwd), "settings.json"), []byte(tc.project), 0o644)
			}
			sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
			if got := sm.GetCodeBlockIndent(); got != tc.want {
				t.Fatalf("GetCodeBlockIndent = %q, want %q", got, tc.want)
			}
			if encoded, err := json.Marshal(sm.Get()); err != nil || !strings.Contains(string(encoded), tc.merged) {
				t.Errorf("merged settings %s (%v) lost %s", encoded, err, tc.merged)
			}
			if tc.keeps == "" {
				return
			}
			if encoded, err := json.Marshal(sm.GetGlobalSettings()); err != nil || !strings.Contains(string(encoded), tc.keeps) {
				t.Errorf("global settings %s (%v) lost %s", encoded, err, tc.keeps)
			}
			if err := sm.SetTheme("dark"); err != nil {
				t.Fatal(err)
			}
			written, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
			if err != nil {
				t.Fatal(err)
			}
			if compact := strings.NewReplacer(" ", "", "\n", "").Replace(string(written)); !strings.Contains(compact, tc.keeps) {
				t.Errorf("a settings write lost %s: %s", tc.keeps, written)
			}
		})
	}
}
