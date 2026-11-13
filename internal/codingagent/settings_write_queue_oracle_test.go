package codingagent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// settingsQueueOp is one step of a write-queue scenario. A setter is followed by flush() in Pi, so every step after it finds the setter's write performed, as it does in PiG, where the setter returns after it.
type settingsQueueOp struct {
	// Op is set (the global setter Name with Text), setProject (the project setter Name with Items), lock or unlock (the lock directory of the file of scope Name), external (another process writes Text to the file of scope Name), trust (Value), reload, drain (the recorded errors) or snapshot (both files).
	Op    string   `json:"op"`
	Name  string   `json:"name,omitempty"`
	Text  string   `json:"text,omitempty"`
	Items []string `json:"items,omitempty"`
	Value bool     `json:"value,omitempty"`
}

// The error path of the settings write queue against Pi (settings-manager.js enqueueWrite, save, reload, setProjectTrusted, drainErrors): a write that fails on a held lock is recorded once and not thrown, the writes after it run, the write after a failed one saves what the failed one did not, reload and a trust change forget what was not saved, and a project setter of an untrusted project throws. The recorded errors, the thrown errors and the bytes of both files are compared.
func TestSettingsWriteQueueErrorPathMatchesPi(t *testing.T) {
	set := func(name, text string) settingsQueueOp { return settingsQueueOp{Op: "set", Name: name, Text: text} }
	project := func(items ...string) settingsQueueOp {
		return settingsQueueOp{Op: "setProject", Name: "setProjectExtensionPaths", Items: items}
	}
	op := func(name string) settingsQueueOp { return settingsQueueOp{Op: name, Name: "global"} }
	projectOp := func(name string) settingsQueueOp { return settingsQueueOp{Op: name, Name: "project"} }
	external := func(scope, text string) settingsQueueOp {
		return settingsQueueOp{Op: "external", Name: scope, Text: text}
	}
	trust := func(value bool) settingsQueueOp { return settingsQueueOp{Op: "trust", Value: value} }
	scenarios := [][]settingsQueueOp{
		// A failed write is recorded once; the next write of the scope saves its change too.
		{op("lock"), set("setTheme", "dark"), op("drain"), op("drain"), op("snapshot"), op("unlock"), set("setDefaultModel", "m"), op("drain"), op("snapshot")},
		// Two failed writes, then one that saves all three changes; a later write saves only its own.
		{op("lock"), set("setTheme", "dark"), set("setShellPath", "/bin/zsh"), op("unlock"), set("setDefaultProvider", "p"), op("snapshot"), op("drain"), set("setDefaultModel", "m"), op("snapshot")},
		// reload forgets the change a failed write did not save, in memory and for the next write.
		{set("setTheme", "a"), op("lock"), set("setTheme", "b"), op("unlock"), op("reload"), op("snapshot"), set("setDefaultModel", "m"), op("snapshot"), op("drain")},
		// After a reload the next write saves only its own change: what another process wrote to a field of the failed write stays.
		{set("setTheme", "a"), op("lock"), set("setTheme", "b"), op("unlock"), external("global", `{"theme":"x"}`), op("reload"), external("global", `{"theme":"y"}`), set("setDefaultModel", "m"), op("snapshot"), op("drain")},
		// The same for a project write that failed, after the trust was withdrawn and given back.
		{project("a"), projectOp("lock"), project("b"), projectOp("unlock"), trust(false), external("project", `{"extensions":["z"]}`), trust(true), external("project", `{"extensions":["y"]}`), {Op: "setProject", Name: "setProjectSkillPaths", Items: []string{"s"}}, op("snapshot"), op("drain")},
		// A failed project write is carried by the next write of the project scope, and that write only: once saved, a change is not written again over what another process wrote.
		{project("a"), projectOp("lock"), project("b"), projectOp("unlock"), {Op: "setProject", Name: "setProjectSkillPaths", Items: []string{"s"}}, op("snapshot"), external("project", `{"skills":["s"]}`), {Op: "setProject", Name: "setProjectThemePaths", Items: []string{"t"}}, op("snapshot"), op("drain")},
		// The same for the global scope.
		{op("lock"), set("setTheme", "dark"), op("unlock"), set("setShellPath", "/bin/zsh"), op("snapshot"), external("global", `{"shellPath":"/bin/zsh"}`), set("setDefaultModel", "m"), op("snapshot"), op("drain")},
		// The failed write's change is saved even when the next setter of the scope changes the same field.
		{op("lock"), set("setTheme", "dark"), op("unlock"), set("setTheme", "light"), op("snapshot"), op("drain")},
		// Project writes; a setter of an untrusted project throws and writes nothing; trusting it again reads the file.
		{project("a"), op("snapshot"), trust(false), project("b"), op("snapshot"), set("setTheme", "dark"), trust(true), project("c"), op("snapshot"), op("drain")},
	}
	var want struct {
		Scenarios [][]string `json:"scenarios"`
		// SameTickRevoke is what Pi records when the trust is withdrawn in the synchronous block of a project setter, before its queued write runs.
		SameTickRevoke []string `json:"sameTickRevoke"`
	}
	pioracle.Run(t, `
const { SettingsManager } = await load("pi-coding-agent/core/settings-manager.js");
const { CONFIG_DIR_NAME } = await load("pi-coding-agent/config.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const world = () => {
	const root = fs.mkdtempSync(path.join(os.tmpdir(), "settingsq-"));
	const agentDir = path.join(root, "agent");
	const cwd = path.join(root, "proj");
	fs.mkdirSync(agentDir); fs.mkdirSync(cwd);
	const files = { global: path.join(agentDir, "settings.json"), project: path.join(cwd, CONFIG_DIR_NAME, "settings.json") };
	fs.writeFileSync(files.global, "{}");
	return { root, files, settings: SettingsManager.create(cwd, agentDir) };
};
const read = (file) => fs.existsSync(file) ? fs.readFileSync(file, "utf8") : "<absent>";
const errors = (settings) => "errors: " + settings.drainErrors().map((e) => e.scope + ": " + e.error.message).join(" | ");
const scenarios = [];
for (const ops of input) {
	const { root, files, settings } = world();
	const events = [];
	for (const op of ops) {
		try {
			switch (op.op) {
			case "set": settings[op.name](op.text); await settings.flush(); break;
			case "setProject": settings[op.name](op.items); await settings.flush(); break;
			case "lock": fs.mkdirSync(files[op.name] + ".lock"); break;
			case "unlock": fs.rmdirSync(files[op.name] + ".lock"); break;
			case "external": fs.writeFileSync(files[op.name], op.text); break;
			case "trust": settings.setProjectTrusted(op.value === true); break;
			case "reload": await settings.reload(); break;
			case "drain": events.push(errors(settings)); break;
			case "snapshot": events.push("global=" + read(files.global) + " project=" + read(files.project)); break;
			}
		} catch (e) { events.push(op.op + " throws: " + e.message); }
	}
	scenarios.push(events);
	fs.rmSync(root, { recursive: true, force: true });
}
const { root, files, settings } = world();
settings.setProjectExtensionPaths(["a"]);
settings.setProjectTrusted(false);
await settings.flush();
const sameTickRevoke = [errors(settings), "project=" + read(files.project)];
fs.rmSync(root, { recursive: true, force: true });
emit({ scenarios, sameTickRevoke });
`, scenarios, &want)
	if len(want.Scenarios) != len(scenarios) {
		t.Fatalf("Pi answered %d scenarios, want %d", len(want.Scenarios), len(scenarios))
	}
	read := func(file string) string {
		data, err := os.ReadFile(file)
		if err != nil {
			return "<absent>"
		}
		return string(data)
	}
	drain := func(sm *SettingsManager) string {
		var parts []string
		for _, e := range sm.DrainErrors() {
			parts = append(parts, e.Scope+": "+e.Error.Error())
		}
		return "errors: " + strings.Join(parts, " | ")
	}
	for i, ops := range scenarios {
		root := t.TempDir()
		agentDir, cwd := filepath.Join(root, "agent"), filepath.Join(root, "proj")
		for _, dir := range []string{agentDir, cwd} {
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		global, projectFile := filepath.Join(agentDir, "settings.json"), filepath.Join(ProjectConfigDir(cwd), "settings.json")
		files := map[string]string{"global": global, "project": projectFile}
		if err := os.WriteFile(global, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		sm := NewSettingsManager(cwd, agentDir)
		events := []string{}
		for _, op := range ops {
			var err error
			switch op.Op {
			case "set":
				switch op.Name {
				case "setTheme":
					err = sm.SetTheme(op.Text)
				case "setDefaultModel":
					err = sm.SetDefaultModel(op.Text)
				case "setDefaultProvider":
					err = sm.SetDefaultProvider(op.Text)
				case "setShellPath":
					err = sm.SetShellPath(op.Text)
				default:
					t.Fatalf("unknown setter %s", op.Name)
				}
			case "setProject":
				switch op.Name {
				case "setProjectExtensionPaths":
					err = sm.SetProjectExtensionPaths(op.Items)
				case "setProjectSkillPaths":
					err = sm.SetProjectSkillPaths(op.Items)
				case "setProjectThemePaths":
					err = sm.SetProjectThemePaths(op.Items)
				default:
					t.Fatalf("unknown setter %s", op.Name)
				}
			case "lock":
				if err := os.Mkdir(files[op.Name]+".lock", 0o755); err != nil {
					t.Fatal(err)
				}
			case "unlock":
				if err := os.Remove(files[op.Name] + ".lock"); err != nil {
					t.Fatal(err)
				}
			case "external":
				if err := os.WriteFile(files[op.Name], []byte(op.Text), 0o600); err != nil {
					t.Fatal(err)
				}
			case "trust":
				sm.SetProjectTrusted(op.Value)
			case "reload":
				sm.Reload()
			case "drain":
				events = append(events, drain(sm))
			case "snapshot":
				events = append(events, "global="+read(global)+" project="+read(projectFile))
			}
			// Pi's setter throws only the refusal of an untrusted project. PiG's also returns the failure of its write, which is recorded as Pi's is and compared by the drain steps.
			if err != nil && op.Op == "setProject" && !sm.IsProjectTrusted() {
				events = append(events, op.Op+" throws: "+err.Error())
			}
		}
		if !reflect.DeepEqual(events, want.Scenarios[i]) {
			t.Errorf("scenario %d:\n PiG %q\n  Pi %q", i, events, want.Scenarios[i])
		}
	}
	// PiG performs a setter's write before the setter returns, so a trust withdrawn after it finds the file written. The refusal Pi records in that block is the one PiG's queue records for a project write that runs after the trust was withdrawn (TestQueuedProjectWriteIsRefusedWhenTheTrustWasWithdrawnBeforeItRuns).
	if wantRevoke := []string{"errors: project: Project is not trusted; refusing to write project settings", "project=<absent>"}; !reflect.DeepEqual(want.SameTickRevoke, wantRevoke) {
		t.Errorf("Pi's same-block trust withdrawal = %q, want %q", want.SameTickRevoke, wantRevoke)
	}
}
