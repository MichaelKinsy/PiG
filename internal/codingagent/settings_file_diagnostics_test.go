package codingagent

import (
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi 0.87.1 settings-manager.ts loadFromStorage reads nothing from an empty file and otherwise calls
// JSON.parse(stripBom(content)); settings-diagnostics.ts reports `Invalid settings file <path>: <error.message>`, so a
// syntax error carries V8's JSON.parse message. The expected diagnostics come from Pi's own SettingsManager reading
// the same files.
func TestSettingsFileDiagnosticsMatchPi(t *testing.T) {
	contents := map[string]string{
		"valid":          `{"theme":"dark"}`,
		"unquoted-key":   `{compaction: {"keepRecentTokens": 1}}`,
		"trailing-comma": "{\n  \"theme\": \"dark\",\n}",
		"truncated":      `{"theme": "da`,
		"single-quotes":  `{'theme': 'dark'}`,
		"comment":        "{\n  // comment\n  \"theme\": \"dark\"\n}",
		"empty":          "",
		"whitespace":     "  \n",
		"bom-only":       "\ufeff",
		"bom-valid":      "\ufeff{\"theme\":\"dark\"}",
		"extra-token":    `{"theme":"dark"} x`,
		"bad-escape":     `{"theme": "\q"}`,
		"control-char":   "{\"theme\": \"da\trk\"}",
		"astral-comma":   "{\n \"\U0001F600\": 1,}",
		"crlf-comma":     "{\r\n  \"theme\": \"dark\",\r\n}",
		"null":           "null",
		"array":          `[{"theme":"dark"}]`,
		"string":         `"x y"`,
		"number":         "5",
		"big-number":     "1e21",
		"negative-zero":  "-0",
		"fraction":       "1.50",
		"true":           "true",
		"type-mismatch":  `{"theme": 5, "packages": "x", "compaction": {"keepRecentTokens": "1"}}`,
	}
	root := t.TempDir()
	dirs := map[string]string{}
	for name, content := range contents {
		agentDir := filepath.Join(root, name)
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		dirs[name] = agentDir
	}
	want := runPiJSONFileOracle(t, dirs, `const {SettingsManager} = await load('core/settings-manager');
const {collectSettingsDiagnostics} = await load('core/settings-diagnostics');
for (const [name, agentDir] of Object.entries(files)) {
  out[name] = collectSettingsDiagnostics(SettingsManager.create(agentDir + '-project', agentDir)).map(d => d.type + ': ' + d.message).join('\n');
}`)
	for name, agentDir := range dirs {
		t.Run(name, func(t *testing.T) {
			sm := NewSettingsManager(agentDir+"-project", agentDir)
			var got []string
			for _, diagnostic := range CollectSettingsDiagnostics(sm) {
				got = append(got, diagnostic.Type+": "+diagnostic.Message)
			}
			if joined := strings.Join(got, "\n"); joined != want[name] {
				t.Fatalf("diagnostics = %q, want %q", joined, want[name])
			}
		})
	}
}

var jsonFileSyntaxCases = map[string]string{
	"blank":         "",
	"whitespace":    "  \n",
	"unterminated":  "{\n  \"providers\": {\n",
	"invalid-token": `{ invalid json }`,
	"extra-token":   `{} x`,
	"bad-escape":    `{"a": "\q"}`,
}

// Pi 0.87.1 model-config.ts ModelConfig.load reports `Failed to parse models.json: <JSON.parse message>` for content
// that JSON.parse rejects after comments and a BOM are stripped. The expected errors come from Pi's ModelConfig.
func TestModelsJSONParseErrorsMatchPi(t *testing.T) {
	root := t.TempDir()
	paths := map[string]string{}
	for name, content := range jsonFileSyntaxCases {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		paths[name] = filepath.Join(dir, "models.json")
		if err := os.WriteFile(paths[name], []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := runPiJSONFileOracle(t, paths, `const {ModelConfig} = await load('core/model-config');
for (const [name, path] of Object.entries(files)) out[name] = (await ModelConfig.load(path)).getError() ?? '';`)
	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			if got := NewModelRegistryWithModelsPath(path).LoadError(); got != want[name] {
				t.Fatalf("LoadError() = %q, want %q", got, want[name])
			}
		})
	}
}

// Pi 0.87.1 trust-manager.ts readTrustFile throws `Failed to read trust store <path>: <JSON.parse message>`. The
// expected errors come from Pi's ProjectTrustStore.
func TestTrustStoreParseErrorsMatchPi(t *testing.T) {
	root := t.TempDir()
	dirs := map[string]string{}
	// Unlike models.json, the trust store strips no comments or trailing commas before JSON.parse.
	cases := maps.Clone(jsonFileSyntaxCases)
	cases["trailing-comma"] = "{\"a\": true,\n}"
	for name, content := range cases {
		dirs[name] = filepath.Join(root, name)
		if err := os.MkdirAll(dirs[name], 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dirs[name], "trust.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := runPiJSONFileOracle(t, dirs, `const {ProjectTrustStore} = await load('core/trust-manager');
for (const [name, dir] of Object.entries(files)) {
  try { new ProjectTrustStore(dir).getEntry(dir); out[name] = ''; } catch (error) { out[name] = error.message; }
}`)
	for name, dir := range dirs {
		t.Run(name, func(t *testing.T) {
			got := ""
			if _, err := NewProjectTrustStore(dir).GetEntry(dir); err != nil {
				got = err.Error()
			}
			if got != want[name] {
				t.Fatalf("GetEntry error = %q, want %q", got, want[name])
			}
		})
	}
}

// runPiJSONFileOracle runs body against Pi 0.87.1's pinned package with `files` bound to the given map and returns the
// strings it stores in `out`.
func runPiJSONFileOracle(t *testing.T, files map[string]string, body string) map[string]string {
	t.Helper()
	pkg, err := filepath.Abs("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	script := `import {pathToFileURL} from 'node:url';
import {join} from 'node:path';
const [pkg, files] = [process.argv[1], JSON.parse(process.argv[2])];
const load = name => import(pathToFileURL(join(pkg, 'dist', name + '.js')).href);
const out = {};
` + body + `
process.stdout.write(JSON.stringify(out));`
	output, err := exec.CommandContext(t.Context(), node, "--input-type=module", "-e", script, "--", pkg, string(input)).Output()
	if err != nil {
		var stderr []byte
		if exitErr := new(exec.ExitError); errors.As(err, &exitErr) {
			stderr = exitErr.Stderr
		}
		t.Fatalf("Pi oracle: %v\n%s", err, stderr)
	}
	var want map[string]string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	return want
}
