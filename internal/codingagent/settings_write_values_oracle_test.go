package codingagent

import (
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type settingsValueProbe struct {
	Seed        string                `json:"seed"`
	ProjectSeed string                `json:"projectSeed"`
	Ops         []settingsValueSetter `json:"ops"`
}

type settingsValueSetter struct {
	Name     string                 `json:"name"`
	Number   float64                `json:"number"`
	Flag     bool                   `json:"flag"`
	Provider string                 `json:"provider"`
	Model    string                 `json:"model"`
	Level    string                 `json:"level"`
	Packages []settingsValuePackage `json:"packages"`
	Paths    []string               `json:"paths"`
}

type settingsValuePackage struct {
	Source     string   `json:"source"`
	Object     bool     `json:"object"`
	Autoload   *bool    `json:"autoload"`
	Extensions []string `json:"extensions"`
	Skills     []string `json:"skills"`
}

type settingsValueResult struct {
	Global  string `json:"global"`
	Project string `json:"project"`
	Threw   []bool `json:"threw"`
}

// Value-typed global and project settings writes against Pi's SettingsManager (settings-manager.js): clamped and floored numbers, booleans written into a nested object that the seed file may already hold in an odd shape, per-model thinking levels, warnings, package sources (string or object form) and project-scope lists. The bytes of both settings files and which setters threw are compared.
func TestSettingsValueWritesMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(89))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	seeds := []string{
		"", "{}", `{"compaction":{"enabled":false,"reserveTokens":1,"x":[1,2]},"terminal":{"showImages":true,"imageWidthCells":9}}`,
		`{"compaction":null}`, `{"terminal":{}}`, `{"modelThinkingLevels":{"a/b":"low"},"warnings":{"anthropicExtraUsage":true}}`, `{"warnings":{"anthropicExtraUsage":true,"other":1,"nested":{"a":2}}}`, `{"warnings":{"unknown":false}}`,
		`{"packages":["npm:a",{"source":"npm:b","extensions":["x"]}]}`,
		`{"packages":[{"extensions":["x"],"source":"npm:b","autoload":false},{"skills":[],"themes":["t"],"prompts":[],"source":"git:y"},"npm:c"]}`, `{"httpIdleTimeoutMs":5,"editorPaddingX":2}`,
		"{\n\t\"retry\": {\"enabled\": true, \"maxRetries\": 3},\n\t\"compaction\": {\"enabled\": true}\n}\n",
		// Members PackageSource has no field for, array-index keys and a duplicate key: Pi's `{ ...existing, source }` keeps them.
		`{"packages":[{"source":"npm:b","note":"keep <&>","extensions":["x"]},{"2":1,"source":"git:z","1":{"a":[1.50,"\u00e9"]}},{"skills":[],"source":"npm:a","source":"npm:d"},"npm:s"]}`,
	}
	projectSeeds := []string{"", "{}", `{"packages":[{"themes":["t"],"source":"npm:q"}]}`, `{"packages":["npm:p"],"extensions":["e1"]}`, `{"theme":"x","skills":[]}`}
	numbers := []float64{0, 1, 1.5, 2.9, 3, 7, 20, 21, 100, 123456.789, 2e9, -1, -0.5, -7, 0.4}
	flag := func() bool { return rng.Intn(2) == 0 }
	strs := func() []string {
		out := []string{}
		for range rng.Intn(3) {
			out = append(out, pick("a", "b/c", "<x>&", "日本", "./rel", "npm:z@1"))
		}
		return out
	}
	maybeStrs := func() []string {
		if rng.Intn(3) == 0 {
			return nil
		}
		return strs()
	}
	var probes []settingsValueProbe
	for range 900 {
		p := settingsValueProbe{Seed: pick(seeds...), ProjectSeed: pick(projectSeeds...)}
		for range 1 + rng.Intn(4) {
			n := numbers[rng.Intn(len(numbers))]
			switch rng.Intn(13) {
			case 0:
				p.Ops = append(p.Ops, settingsValueSetter{Name: "setHttpIdleTimeoutMs", Number: n})
			case 1:
				p.Ops = append(p.Ops, settingsValueSetter{Name: "setImageWidthCells", Number: float64(int(n))})
			case 2:
				p.Ops = append(p.Ops, settingsValueSetter{Name: "setEditorPaddingX", Number: float64(int(n))})
			case 3:
				p.Ops = append(p.Ops, settingsValueSetter{Name: "setAutocompleteMaxVisible", Number: float64(int(n))})
			case 4:
				p.Ops = append(p.Ops, settingsValueSetter{Name: "setCompactionEnabled", Flag: flag()})
			case 5:
				p.Ops = append(p.Ops, settingsValueSetter{Name: "setHideThinkingBlock", Flag: flag()})
			case 6:
				p.Ops = append(p.Ops, settingsValueSetter{Name: "setModelThinkingLevel", Provider: pick("anthropic", "a", "x<y"), Model: pick("b", "claude/3", "m&n"), Level: pick("low", "high", "off", "xhigh")})
			case 7:
				p.Ops = append(p.Ops, settingsValueSetter{Name: "setWarnings", Flag: flag()})
			case 8, 9:
				op := settingsValueSetter{Name: pick("setPackages", "setProjectPackages")}
				for range rng.Intn(4) {
					pk := settingsValuePackage{Source: pick("npm:a", "git:github.com/x/y", "./local", "npm:<z>&")}
					if rng.Intn(2) == 0 {
						pk.Object = true
						pk.Extensions, pk.Skills = maybeStrs(), maybeStrs()
						if rng.Intn(3) == 0 {
							pk.Autoload = new(flag())
						}
					}
					op.Packages = append(op.Packages, pk)
				}
				p.Ops = append(p.Ops, op)
			case 10:
				p.Ops = append(p.Ops, settingsValueSetter{Name: pick("rewritePackages", "repinPackages")})
			default:
				p.Ops = append(p.Ops, settingsValueSetter{Name: pick("setProjectExtensionPaths", "setProjectSkillPaths", "setProjectThemePaths"), Paths: strs()})
			}
		}
		probes = append(probes, p)
	}
	var want []settingsValueResult
	pioracle.Run(t, `
const { SettingsManager } = await load("pi-coding-agent/core/settings-manager.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const out = [];
const sources = (list) => (list ?? []).map((pk) => {
	if (!pk.object) return pk.source;
	const o = { source: pk.source };
	if (pk.autoload !== null) o.autoload = pk.autoload;
	if (pk.extensions !== null) o.extensions = pk.extensions;
	if (pk.skills !== null) o.skills = pk.skills;
	return o;
});
for (const p of input) {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "settingsv-"));
	const agentDir = path.join(root, "agent");
	const cwd = path.join(root, "proj");
	fs.mkdirSync(agentDir); fs.mkdirSync(path.join(cwd, ".pig"), { recursive: true });
	const file = path.join(agentDir, "settings.json");
	const pfile = path.join(cwd, ".pig", "settings.json");
	if (p.seed !== "") fs.writeFileSync(file, p.seed);
	if (p.projectSeed !== "") fs.writeFileSync(pfile, p.projectSeed);
	const threw = [];
	try {
		const settings = SettingsManager.create(cwd, agentDir);
		for (const op of p.ops) {
			try {
				switch (op.name) {
					case "setModelThinkingLevel": settings.setModelThinkingLevel(op.provider, op.model, op.level); break;
					case "setWarnings": settings.setWarnings({ anthropicExtraUsage: op.flag }); break;
					case "setPackages": case "setProjectPackages": settings[op.name](sources(op.packages)); break;
					case "rewritePackages": settings.setPackages(settings.getPackages()); break;
					case "repinPackages": settings.setPackages(settings.getPackages().map((pk) => typeof pk === "string" ? pk + "@2" : { ...pk, source: pk.source + "@2" })); break;
					case "setProjectExtensionPaths": case "setProjectSkillPaths": case "setProjectThemePaths": settings[op.name](op.paths ?? []); break;
					case "setCompactionEnabled": case "setHideThinkingBlock": settings[op.name](op.flag); break;
					default: settings[op.name](op.number);
				}
				threw.push(false);
			} catch (e) { threw.push(true); }
			await settings.flush();
		}
	} catch (e) { /* files stay as written */ }
	out.push({ global: fs.existsSync(file) ? fs.readFileSync(file, "utf8") : "<absent>", project: fs.existsSync(pfile) ? fs.readFileSync(pfile, "utf8") : "<absent>", threw });
	fs.rmSync(root, { recursive: true, force: true });
}
emit(out);
`, probes, &want)
	bad := 0
	for i, p := range probes {
		root := t.TempDir()
		agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
		_ = os.Mkdir(agentDir, 0o755)
		_ = os.MkdirAll(filepath.Join(cwd, ".pig"), 0o755)
		file, pfile := filepath.Join(agentDir, "settings.json"), filepath.Join(cwd, ".pig", "settings.json")
		if p.Seed != "" {
			_ = os.WriteFile(file, []byte(p.Seed), 0o600)
		}
		if p.ProjectSeed != "" {
			_ = os.WriteFile(pfile, []byte(p.ProjectSeed), 0o600)
		}
		sm := NewSettingsManagerWithProjectTrust(cwd, agentDir, true)
		var threw []bool
		for _, op := range p.Ops {
			var err error
			sources := func() []PackageSource {
				out := []PackageSource{}
				for _, pk := range op.Packages {
					out = append(out, PackageSource{Source: pk.Source, WasObject: pk.Object, Autoload: pk.Autoload, Extensions: pk.Extensions, Skills: pk.Skills})
				}
				return out
			}
			switch op.Name {
			case "setHttpIdleTimeoutMs":
				err = sm.SetHttpIdleTimeoutMs(op.Number)
			case "setImageWidthCells":
				err = sm.SetImageWidthCells(int(op.Number))
			case "setEditorPaddingX":
				err = sm.SetEditorPaddingX(int(op.Number))
			case "setAutocompleteMaxVisible":
				err = sm.SetAutocompleteMaxVisible(int(op.Number))
			case "setCompactionEnabled":
				err = sm.SetCompactionEnabled(op.Flag)
			case "setHideThinkingBlock":
				err = sm.SetHideThinkingBlock(op.Flag)
			case "setModelThinkingLevel":
				err = sm.SetModelThinkingLevel(op.Provider, op.Model, ai.ThinkingLevel(op.Level))
			case "setWarnings":
				err = sm.SetWarnings(WarningSettings{AnthropicExtraUsage: op.Flag})
			case "setPackages":
				err = sm.SetPackages(sources())
			case "setProjectPackages":
				err = sm.SetProjectPackages(sources())
			case "rewritePackages":
				err = sm.SetPackages(sm.GetPackages())
			case "repinPackages":
				packages := sm.GetPackages()
				for i := range packages {
					packages[i].Source += "@2"
				}
				err = sm.SetPackages(packages)
			case "setProjectExtensionPaths":
				err = sm.SetProjectExtensionPaths(op.Paths)
			case "setProjectSkillPaths":
				err = sm.SetProjectSkillPaths(op.Paths)
			case "setProjectThemePaths":
				err = sm.SetProjectThemePaths(op.Paths)
			}
			threw = append(threw, err != nil)
		}
		got := settingsValueResult{Global: "<absent>", Project: "<absent>", Threw: threw}
		if data, err := os.ReadFile(file); err == nil {
			got.Global = string(data)
		}
		if data, err := os.ReadFile(pfile); err == nil {
			got.Project = string(data)
		}
		w := want[i]
		if got.Global != w.Global || got.Project != w.Project || !slices.Equal(got.Threw, w.Threw) {
			if bad++; bad <= 6 {
				t.Errorf("probe %d seed=%q project=%q ops=%v:\n got %#q\nwant %#q", i, p.Seed, p.ProjectSeed, p.Ops, []any{got.Global, got.Project, got.Threw}, []any{w.Global, w.Project, w.Threw})
			}
		}
	}
	if bad > 6 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}
