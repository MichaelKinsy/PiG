package codingagent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// pi: packages/coding-agent/src/core/settings-manager.ts

// The bytes of settings.json after setter calls (persistScopedSettings: the file's own member order kept, a modified field overwritten in place or
// appended, `JSON.stringify(merged, null, 2)`). The pinned SettingsManager and PiG's write the same scripted setters over the same starting file.
func TestSettingsFileBytesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(47))
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	chunks := []string{"a", "dark", "é", "😀", "\u2028", "\u2029", "<&>", "\\", "\"", "\n", "\u007f", "\u0001", "x y", "/"}
	str := func() string {
		var b strings.Builder
		for range 1 + rng.Intn(4) {
			b.WriteString(chunks[rng.Intn(len(chunks))])
		}
		return b.String()
	}
	strs := func() []string {
		out := []string{}
		for range rng.Intn(4) {
			out = append(out, str())
		}
		return out
	}
	initials := []func() string{
		func() string { return "" },
		func() string { return "{}" },
		func() string {
			return fmt.Sprintf(`{"$schema":"https://raw.githubusercontent.com/earendil-works/pi/main/packages/coding-agent/schemas/settings.schema.json","zzz":{"b":1,"a":[1,2,{"k":%s}]},"theme":%s,"10":true,"2":"x","aaa":null}`, quote(str()), quote(str()))
		},
		func() string {
			return fmt.Sprintf("\ufeff{\n  \"defaultProvider\": %s,\n  \"compaction\": {\"enabled\": false, \"keepRecentTokens\": 5, \"extra\": 1},\n  \"unknown\": 1.50\n}", quote(str()))
		},
		func() string {
			return `{"enabledModels":["a","b"],"extensions":["/x"],"lastChangelogVersion":"1.0.0","editorPaddingX":2}`
		},
		// Legacy keys that Pi's migrateSettings rewrites when the file is saved (persistScopedSettings migrates the current file first).
		func() string {
			return `{"queueMode":"all","websockets":true,"skills":{"enableSkillCommands":false,"customDirectories":["/a","/b"]},"retry":{"enabled":true,"maxDelayMs":9000}}`
		},
		func() string {
			return `{"retry":{"maxDelayMs":5,"provider":{"maxRetryDelayMs":7,"timeoutMs":9}},"skills":{"customDirectories":[]},"websockets":false,"steeringMode":"one-at-a-time","queueMode":"all"}`
		},
		func() string { return `{"retry":{"provider":null,"maxDelayMs":3},"transport":"sse","websockets":true}` },
		func() string {
			return `{"retry":{"maxDelayMs":"x","provider":{"maxRetryDelayMs":null,"a":1}},"skills":{"enableSkillCommands":true},"enableSkillCommands":false,"websockets":"yes"}`
		},
	}
	type script struct {
		Initial *string          `json:"initial"`
		Ops     []map[string]any `json:"ops"`
	}
	var scripts []script
	for range 400 {
		s := script{}
		if i := rng.Intn(len(initials)); i > 0 {
			text := initials[i]()
			s.Initial = &text
		}
		for range 1 + rng.Intn(6) {
			switch rng.Intn(12) {
			case 0:
				s.Ops = append(s.Ops, map[string]any{"op": "theme", "value": str()})
			case 1:
				s.Ops = append(s.Ops, map[string]any{"op": "model", "provider": str(), "model": str()})
			case 2:
				s.Ops = append(s.Ops, map[string]any{"op": "prefix", "value": str()})
			case 3:
				s.Ops = append(s.Ops, map[string]any{"op": "npm", "value": strs()})
			case 4:
				s.Ops = append(s.Ops, map[string]any{"op": "enabledModels", "value": strs()})
			case 5:
				s.Ops = append(s.Ops, map[string]any{"op": "extensions", "value": strs()})
			case 6:
				s.Ops = append(s.Ops, map[string]any{"op": "changelog", "value": str()})
			case 7:
				s.Ops = append(s.Ops, map[string]any{"op": "compaction", "value": rng.Intn(2) == 0})
			case 8:
				s.Ops = append(s.Ops, map[string]any{"op": "imageWidth", "value": 1 + rng.Intn(80)})
			case 9:
				s.Ops = append(s.Ops, map[string]any{"op": "padding", "value": rng.Intn(5)})
			case 10:
				s.Ops = append(s.Ops, map[string]any{"op": "shellPath", "value": str()})
			default:
				s.Ops = append(s.Ops, map[string]any{"op": "transport", "value": []string{"sse", "websocket", "auto"}[rng.Intn(3)]})
			}
		}
		scripts = append(scripts, s)
	}
	input, err := json.Marshal(scripts)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/settings_file_bytes.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []*string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, s := range scripts {
		agentDir, cwd := t.TempDir(), t.TempDir()
		if s.Initial != nil {
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(*s.Initial), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		manager := NewSettingsManager(cwd, agentDir)
		for _, op := range s.Ops {
			var err error
			switch op["op"] {
			case "theme":
				err = manager.SetTheme(op["value"].(string))
			case "model":
				err = manager.SetDefaultModelAndProvider(op["provider"].(string), op["model"].(string))
			case "prefix":
				err = manager.SetShellCommandPrefix(op["value"].(string))
			case "npm":
				err = manager.SetNpmCommand(op["value"].([]string))
			case "enabledModels":
				err = manager.SetEnabledModels(op["value"].([]string))
			case "extensions":
				err = manager.SetExtensionPaths(op["value"].([]string))
			case "changelog":
				err = manager.SetLastChangelogVersion(op["value"].(string))
			case "compaction":
				err = manager.SetCompactionEnabled(op["value"].(bool))
			case "imageWidth":
				err = manager.SetImageWidthCells(op["value"].(int))
			case "padding":
				err = manager.SetEditorPaddingX(op["value"].(int))
			case "shellPath":
				err = manager.SetShellPath(op["value"].(string))
			case "transport":
				err = manager.SetTransport(op["value"].(string))
			}
			if err != nil {
				t.Fatalf("script %d %v: %v", i, op["op"], err)
			}
		}
		if err := manager.Flush(); err != nil {
			t.Fatal(err)
		}
		raw, readErr := os.ReadFile(filepath.Join(agentDir, "settings.json"))
		got := string(raw)
		if readErr != nil {
			got = "<none>"
		}
		expected := "<none>"
		if want[i] != nil {
			expected = *want[i]
		}
		if got != expected {
			if failures++; failures <= 4 {
				t.Errorf("script %d: initial %v ops %v\n PiG %q\n Pi  %q", i, s.Initial, s.Ops, got, expected)
			}
		}
	}
	if failures > 4 {
		t.Errorf("%d of %d settings files differ from Pi", failures, len(scripts))
	}
}
