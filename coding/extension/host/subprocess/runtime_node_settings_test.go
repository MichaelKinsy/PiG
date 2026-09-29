package subprocess_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Extensions read and write settings through Pi's SettingsManager
// (pi-vim reads its piVim settings with SettingsManager.create(cwd) and
// getGlobalSettings/getProjectSettings). The runtime's SettingsManager is
// Pi's own over PiG's configuration tree, so against the same files it
// answers every getter, migrates, merges, persists setters and reports lock
// and parse errors exactly as the pinned package does over Pi's tree.
// Pi config.ts and SettingsManager select the same project tree. The independent SDK bundle must preserve the host's explicit D2 directory choice, not its pre-bundle hard-coded default.
func TestNodeIndependentSDKHonorsPiDirectoryOptIn(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	for name, theme := range map[string]string{".pi": "light", ".pig": "dark"} {
		dir := filepath.Join(cwd, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"theme":"`+theme+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIG_USE_PI_DIRS", "1")
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PIG_TEST_SHARED_CWD", cwd)
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
assert.equal(pig.CONFIG_DIR_NAME, pi.CONFIG_DIR_NAME);
assert.equal(pig.ENV_AGENT_DIR, pi.ENV_AGENT_DIR);
assert.equal(pig.getAgentDir(), pi.getAgentDir());
const cwd = process.env.PIG_TEST_SHARED_CWD;
assert.equal(pi.SettingsManager.create(cwd).getTheme(), "light");
assert.equal(pig.SettingsManager.create(cwd).getTheme(), "light");
`)
}

func TestPiSettingsManagerMatchesThePinnedPackage(t *testing.T) {
	root := t.TempDir()
	global := "\ufeff" + `{
  "theme": "light",
  "queueMode": "all",
  "websockets": true,
  "skills": { "enableSkillCommands": false, "customDirectories": ["~/skills"] },
  "retry": { "maxDelayMs": 5000, "enabled": false },
  "compaction": { "reserveTokens": 1000, "keepRecentTokens": 2000 },
  "piVim": { "modeColors": { "insert": "red" }, "exCommand": { "piDispatch": false } },
  "packages": ["npm:pi-vim"],
  "sessionDir": "~/sessions",
  "defaultThinkingLevel": "high",
  "modelThinkingLevels": { "openai/gpt": "low" },
  "unknownKey": [1, 2, 3]
}`
	project := `{
  "compaction": { "keepRecentTokens": 5 },
  "piVim": { "modeColors": { "normal": "blue" } },
  "theme": "dark",
  "editorPaddingX": 2
}`
	trees := map[string]string{}
	for _, side := range []string{"pi", "pig"} {
		configDir := map[string]string{"pi": ".pi", "pig": ".pig"}[side]
		agent := filepath.Join(root, side, "agent")
		cwd := filepath.Join(root, side, "project")
		for dir, content := range map[string]string{agent: global, filepath.Join(cwd, configDir): project} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		broken := filepath.Join(root, side, "broken")
		if err := os.MkdirAll(filepath.Join(broken, configDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(broken, configDir, "settings.json"), []byte("{ not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		trees[side] = filepath.Join(root, side)
	}
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(trees["pi"], "agent"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(trees["pig"], "agent"))
	t.Setenv("PIG_TEST_SETTINGS_TREES", trees["pi"]+string(os.PathListSeparator)+trees["pig"])
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
import { existsSync, mkdirSync, readFileSync, rmdirSync } from "node:fs";
import { delimiter, join } from "node:path";
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const [piTree, pigTree] = process.env.PIG_TEST_SETTINGS_TREES.split(delimiter);
const agentDirs = { pi: join(piTree, "agent"), pig: join(pigTree, "agent") };
assert.equal(pig.getAgentDir(), agentDirs.pig);
assert.equal(pig.CONFIG_DIR_NAME, ".pig");

function getters(manager) {
  const out = {};
  for (const name of Object.getOwnPropertyNames(Object.getPrototypeOf(manager)).sort()) {
    if (!name.startsWith("get") || name === "getGlobalSettings" || name === "getProjectSettings") continue;
    const fn = manager[name];
    if (typeof fn !== "function") continue;
    try {
      out[name] = fn.length === 0 ? fn.call(manager) : name === "getModelThinkingLevel" ? fn.call(manager, "openai", "gpt") : undefined;
    } catch (error) {
      out[name] = "throws " + error.message;
    }
  }
  // getTrackingId creates a random id; only its presence is comparable.
  out.getTrackingId = typeof out.getTrackingId;
  return out;
}

function errors(manager) {
  return manager.drainErrors().map((e) => ({ scope: e.scope, message: e.error.message.replace(/ at line.*$/, ""), code: e.error.code }));
}

function read(path) {
  return existsSync(path) ? JSON.parse(readFileSync(path, "utf8").replace(/^\ufeff/, "")) : undefined;
}

async function scene(m, tree, configDir) {
  const out = {};
  const cwd = join(tree, "project");
  const manager = m.SettingsManager.create(cwd);
  out.global = manager.getGlobalSettings();
  out.project = manager.getProjectSettings();
  out.getters = getters(manager);
  out.loadErrors = errors(manager);

  manager.setTheme("solarized");
  manager.setDefaultModelAndProvider("anthropic", "claude");
  manager.setCompactionEnabled(false);
  manager.setModelThinkingLevel("openai", "gpt", "medium");
  manager.setProjectPackages(["npm:pi-vim", "npm:other"]);
  await manager.flush();
  out.writeErrors = errors(manager);
  out.globalFile = read(join(tree, "agent", "settings.json"));
  out.projectFile = read(join(cwd, configDir, "settings.json"));
  out.afterWrite = getters(manager);
  out.lockLeft = existsSync(join(tree, "agent", "settings.json.lock"));

  // A live lock (a fresh proper-lockfile directory) holds off the write.
  const lock = join(tree, "agent", "settings.json.lock");
  mkdirSync(lock);
  manager.setTheme("held");
  await manager.flush();
  out.heldErrors = errors(manager);
  rmdirSync(lock);
  out.heldFile = read(join(tree, "agent", "settings.json")).theme;

  await manager.reload();
  out.reloaded = manager.getGlobalSettings();

  const broken = m.SettingsManager.create(join(tree, "broken"));
  out.brokenProject = broken.getProjectSettings();
  out.brokenErrors = errors(broken);
  const untrusted = m.SettingsManager.create(cwd, undefined, { projectTrusted: false });
  out.untrustedProject = untrusted.getProjectSettings();
  const memory = m.SettingsManager.inMemory({ theme: "x", queueMode: "one-at-a-time" });
  out.memory = [memory.getGlobalSettings(), memory.getTheme(), memory.getSteeringMode()];
  return out;
}

const want = await scene(pi, piTree, ".pi");
const got = await scene(pig, pigTree, ".pig");
assert.equal(got.global.theme, "light");
assert.deepEqual(got.project.piVim, { modeColors: { normal: "blue" } });
assert.equal(got.heldErrors[0]?.code, "ELOCKED");
assert.deepEqual(got, want);
`)
}

// PiG's host and an extension's SettingsManager share settings files: the
// runtime resolves PiG's agent directory from the environment exactly as the
// host does, a setting PiG writes is what the extension reads, and a setting
// the extension writes is what PiG reads back.
func TestNodeSettingsManagerSharesPigSettings(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	shim, err := filepath.Abs(filepath.Join("runtime-node", "shims", "pi-coding-agent.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	runNode := func(script string, env ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "--eval",
			`const pig = await import(process.argv[1]);`+script, "file://"+filepath.ToSlash(shim))
		cmd.Env = append(os.Environ(), env...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v\n%s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	for _, env := range [][2]string{
		{"PIG_HOME", filepath.Join(home, "pighome")},
		{"XDG_CONFIG_HOME", filepath.Join(home, "xdg")},
		{"PIG_CODING_AGENT_DIR", "~/custom-agent"},
		{"", ""},
	} {
		for _, key := range []string{"PIG_HOME", "XDG_CONFIG_HOME", "PIG_CODING_AGENT_DIR"} {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
		if env[0] != "" {
			t.Setenv(env[0], env[1])
		}
		want := codingagent.DefaultAgentDir()
		if configured := os.Getenv(codingagent.ENV_AGENT_DIR); configured != "" {
			want = codingagent.ExpandTildePath(configured)
		}
		if got := runNode(`console.log(pig.getAgentDir());`); got != want {
			t.Errorf("with %s=%q the runtime's agent directory is %q, PiG's %q", env[0], env[1], got, want)
		}
	}

	agentDir := filepath.Join(home, "agent")
	cwd := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(cwd, codingagent.CONFIG_DIR_NAME), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, "settings.json"), []byte(`{"piVim":{"exCommand":{"piDispatch":false}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	host := codingagent.NewSettingsManager(cwd, agentDir)
	if err := host.SetTheme("light"); err != nil {
		t.Fatal(err)
	}
	env := []string{"PIG_CODING_AGENT_DIR=" + agentDir}
	cmd := exec.CommandContext(t.Context(), node, "--input-type=module", "--eval", `
const pig = await import(process.argv[1]);
const s = pig.SettingsManager.create(process.argv[2]);
console.log(JSON.stringify([s.getTheme(), s.getProjectSettings().piVim]));
s.setTheme("dark");
await s.flush();
const errors = s.drainErrors();
if (errors.length) throw errors[0].error;
`, "file://"+filepath.ToSlash(shim), cwd)
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != `["light",{"exCommand":{"piDispatch":false}}]` {
		t.Fatalf("the extension read %s", got)
	}
	host.Reload()
	if got := host.GetTheme(); got != "dark" {
		t.Fatalf("PiG reads theme %q after the extension set dark", got)
	}
	if _, err := os.Stat(filepath.Join(agentDir, "settings.json.lock")); !os.IsNotExist(err) {
		t.Fatalf("settings lock left behind: %v", err)
	}
}
