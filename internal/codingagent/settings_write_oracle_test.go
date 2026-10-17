package codingagent

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type settingsWriteProbe struct {
	Seed string                `json:"seed"`
	Ops  []settingsWriteSetter `json:"ops"`
}

type settingsWriteSetter struct {
	Name  string   `json:"name"`
	Text  string   `json:"text"`
	Items []string `json:"items"`
}

// A global settings write (settings-manager.js) against Pi: the file is rewritten with JSON.stringify(settings, null, 2) after each setter, so a seed file's number spellings (1.0, 1e21, 5e-7, -0), key order, unicode escapes, nesting and indentation, and setter strings with <, >, &, U+2028/9, astral characters,
// quotes, backslashes and control characters must come out as Pi writes them. The final bytes of settings.json are compared.
// Left out of the corpus: nothing known; the seeds include empty strings for the four string settings and roots that are not objects (an array, null, a number, a string, a boolean).
func TestSettingsWriteMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(88))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	seeds := []string{
		"", "{}", `{"theme":"dark"}`, "[]", "null", "5", `"s"`, `[1,2]`, `"é😀 x"`, "true", `[{"a":1},null,"x"]`, `{"defaultProvider":"","defaultModel":"","shellPath":"","shellCommandPrefix":""}`, `{"defaultProvider":"x","shellCommandPrefix":"y"}`, "{\n    \"theme\": \"dark\",\n    \"extra\": 1\n}\n", "{\n\t\"b\": 1,\n\t\"a\": 2\n}",
		`{"httpIdleTimeoutMs":1.0,"x":1E5,"y":100000000000000000000000,"z":-0,"w":0.1e1,"v":5e-7,"u":1e21,"t":123456789012345680000,"s":0.000001,"r":1e-7}`,
		`{"theme":"\u00e9\u2028<>&","k":"\ud83d\ude00","nested":{"a":[1,2,{"b":null}],"c":{}},"empty":[],"e2":{}}`,
		`{"10":1,"2":2,"a":3,"1":4,"-1":5}`,
		`{"__proto__":{"x":1},"constructor":2}`,
		`{"enabledModels":["a","b"],"packages":["npm:x",{"source":"npm:y","extensions":["z"]}]}`,
		`{"theme":"a","theme":"b"}`,
		"\ufeff{}", `{`, `{"a":1,}`, "{}\n\n", "  {  }  ", `{"a":"\/"}`, `{"a":1.5E+3,"b":-1.0E-3}`,
	}
	text := func() string {
		return pick("dark", "a<b>&c", "x\u2028y\u2029z", "日本語", "😀", `q"uote`, `back\slash`, "tab\there", "nl\nx", "\u0001\u001f\u007f", "é", "a/b", "</script>", "\u00a0", "\ufeff", strings.Repeat("long", 50))
	}
	items := func() []string {
		out := []string{}
		for range rng.Intn(4) {
			out = append(out, text())
		}
		return out
	}
	var probes []settingsWriteProbe
	for range 800 {
		p := settingsWriteProbe{Seed: pick(seeds...)}
		for range 1 + rng.Intn(4) {
			switch rng.Intn(8) {
			case 0:
				p.Ops = append(p.Ops, settingsWriteSetter{Name: "setTheme", Text: pick("", text())})
			case 1:
				p.Ops = append(p.Ops, settingsWriteSetter{Name: "setDefaultProvider", Text: pick("", text())})
			case 2:
				p.Ops = append(p.Ops, settingsWriteSetter{Name: "setDefaultModel", Text: pick("", text())})
			case 3:
				p.Ops = append(p.Ops, settingsWriteSetter{Name: "setShellPath", Text: pick("", text())})
			case 4:
				p.Ops = append(p.Ops, settingsWriteSetter{Name: "setShellCommandPrefix", Text: pick("", text())})
			case 5:
				p.Ops = append(p.Ops, settingsWriteSetter{Name: "setNpmCommand", Items: items()})
			case 6:
				p.Ops = append(p.Ops, settingsWriteSetter{Name: "setEnabledModels", Items: items()})
			default:
				p.Ops = append(p.Ops, settingsWriteSetter{Name: "setExtensionPaths", Items: items()})
			}
		}
		probes = append(probes, p)
	}
	var want []string
	pioracle.Run(t, `
const { SettingsManager } = await load("pi-coding-agent/core/settings-manager.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
for (const p of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "settingsw-"));
	const agentDir = path.join(root, "agent");
	const cwd = path.join(root, "proj");
	fs.mkdirSync(agentDir); fs.mkdirSync(cwd);
	const file = path.join(agentDir, "settings.json");
	if (p.seed !== "") fs.writeFileSync(file, p.seed);
	try {
		const settings = SettingsManager.create(cwd, agentDir);
		for (const op of p.ops) {
			const arg = op.name === "setNpmCommand" || op.name === "setEnabledModels" || op.name === "setExtensionPaths" ? op.items : op.text;
			settings[op.name](arg);
			await settings.flush();
		}
	} catch (e) { /* the file stays as written */ }
	out.push(fs.existsSync(file) ? fs.readFileSync(file, "utf8") : "<absent>");
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);
`, probes, &want)
	bad := 0
	for i, p := range probes {
		root := t.TempDir()
		agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
		_ = os.Mkdir(agentDir, 0o755)
		_ = os.Mkdir(cwd, 0o755)
		file := filepath.Join(agentDir, "settings.json")
		if p.Seed != "" {
			_ = os.WriteFile(file, []byte(p.Seed), 0o600)
		}
		sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
		for _, op := range p.Ops {
			var err error
			switch op.Name {
			case "setTheme":
				err = sm.SetTheme(op.Text)
			case "setDefaultProvider":
				err = sm.SetDefaultProvider(op.Text)
			case "setDefaultModel":
				err = sm.SetDefaultModel(op.Text)
			case "setShellPath":
				err = sm.SetShellPath(op.Text)
			case "setShellCommandPrefix":
				err = sm.SetShellCommandPrefix(op.Text)
			case "setNpmCommand":
				err = sm.SetNpmCommand(op.Items)
			case "setEnabledModels":
				err = sm.SetEnabledModels(op.Items)
			case "setExtensionPaths":
				err = sm.SetExtensionPaths(op.Items)
			}
			_ = err
		}
		got := "<absent>"
		if data, err := os.ReadFile(file); err == nil {
			got = string(data)
		}
		if got != want[i] {
			if bad++; bad <= 5 {
				t.Errorf("probe %d seed=%q ops=%+v:\n got %q\nwant %q", i, p.Seed, p.Ops, got, want[i])
			}
		}
	}
	if bad > 5 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}
