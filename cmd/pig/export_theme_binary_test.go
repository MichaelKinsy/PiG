package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi 0.99.2 main.ts:633 runs `--export` before initTheme (main.ts:898), so theme.ts getResolvedThemeColors loads the
// system theme (`currentThemeName ?? SYSTEM_THEME_NAME`, theme.ts:904) and export-html/index.ts:112-125 derives the page
// colors from its userMessageBg. The expected lines are what `pi --export` 0.99.2 writes. Neither the terminal
// color-mode environment nor the plain fixture changes them: the export reads Color values, not ANSI escapes.
func TestExportCLIUsesTheSystemThemeColors(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	for _, colorterm := range []string{"truecolor", "24bit", ""} {
		t.Run("COLORTERM="+colorterm, func(t *testing.T) {
			t.Setenv("COLORTERM", colorterm)
			dir := t.TempDir()
			session := filepath.Join(dir, "s.jsonl")
			header := `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp"}` + "\n"
			if err := os.WriteFile(session, []byte(header), 0o600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "out.html")
			_, stderr, code := runPigForModeTest(t, bin, nil, nil, "--export", session, out)
			if code != 0 {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
			html, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"--accent: #800080;", "--borderAccent: #800080;", "--border: #000080;", "--success: #008000;",
				"--error: #800000;", "--warning: #808000;", "--userMessageBg: #000000;",
				"--exportPageBg: rgb(0, 0, 0);", "--exportCardBg: rgb(0, 0, 0);", "--exportInfoBg: rgb(20, 15, 0);",
			} {
				if !strings.Contains(string(html), want) {
					t.Errorf("export is missing %q", want)
				}
			}
		})
	}
}

// Pi 0.99.2 RPC export_html (rpc-mode.ts:598-600) exports with the settings theme when getThemeByName loads it, else with the theme main.ts:898 initTheme selected (agent-session.ts:4212-4227, theme.ts:551-571, 631-640, 903-906). Both resolve a name to the system theme, a built-in theme, or `<name>.json` in the agent's themes directory by file name. The expected values are what `pi --mode rpc --session s.jsonl` 0.99.2 writes for the same settings and theme files.
func TestRPCExportUsesTheConfiguredTheme(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		name, settings, accent, pageBg string
	}{
		{"unset", `{}`, "#800080", "rgb(0, 0, 0)"},
		{"custom theme by file name", `{"theme":"file-name"}`, "#123456", "#21252c"},
		{"a JSON name does not select its file", `{"theme":"json-name"}`, "#800080", "rgb(0, 0, 0)"},
		{"a custom file does not replace a built-in theme", `{"theme":"dark"}`, "#a798d7", "#21252c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			writeCustomTheme(t, agentDir, "file-name", "json-name", "#123456")
			writeCustomTheme(t, agentDir, "dark", "dark", "#abcdef")
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(tc.settings), 0o600); err != nil {
				t.Fatal(err)
			}
			// Pi moves a .jsonl file in the agent directory's root into sessions/ at startup, so the session lives elsewhere.
			// Pi main.ts:694-704 exits 1 when the stored session cwd does not exist, and `/tmp` does not exist on Windows, so the session records its own directory.
			sessionDir := t.TempDir()
			session := filepath.Join(sessionDir, "s.jsonl")
			sessionCwd, err := json.Marshal(sessionDir)
			if err != nil {
				t.Fatal(err)
			}
			lines := `{"type":"session","version":3,"id":"s1","timestamp":"2026-01-01T00:00:00.000Z","cwd":` + string(sessionCwd) + `}` + "\n" +
				`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1}}` + "\n"
			if err := os.WriteFile(session, []byte(lines), 0o600); err != nil {
				t.Fatal(err)
			}
			commands := filepath.Join(agentDir, "commands.jsonl")
			if err := os.WriteFile(commands, []byte(`{"id":"1","type":"export_html","outputPath":"out.html"}`+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			stdin, err := os.Open(commands)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stdin.Close() })
			stdout, stderr, code := runPigForModeTestIn(t, bin, agentDir, stdin, nil, "--mode", "rpc", "--offline", "--session", session)
			if code != 0 || !strings.Contains(stdout, `"success":true`) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			html, err := os.ReadFile(filepath.Join(agentDir, "work", "out.html"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"--accent: " + tc.accent + ";", "--exportPageBg: " + tc.pageBg + ";"} {
				if !strings.Contains(string(html), want) {
					t.Errorf("export is missing %q", want)
				}
			}
		})
	}
}
