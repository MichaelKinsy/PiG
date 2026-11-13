//go:build !pig_strip_mcp

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// runPromptFixture is the home, the project with its AGENTS.md, the user skill, the scripted OpenAI-compatible model and the Node extension that one comparison of the real binary with the pinned Pi runs on. Each implementation gets its own fixture.
type runPromptFixture struct {
	home, cwd, agentDir, sessionDir, probeDir, extension string
	chat                                                 *scriptedChatServer
}

func newRunPromptFixture(t *testing.T, replies int) *runPromptFixture {
	t.Helper()
	f := &runPromptFixture{home: t.TempDir(), cwd: t.TempDir(), sessionDir: t.TempDir(), probeDir: t.TempDir()}
	f.agentDir = filepath.Join(f.home, "pig")
	f.extension = filepath.Join(f.cwd, "options.mjs")
	steps := make([]chatStep, replies)
	for i := range steps {
		steps[i] = chatText("done")
	}
	f.chat = startScriptedChatServer(t, steps...)
	models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
		"api": "openai-completions", "baseUrl": f.chat.URL, "apiKey": "fixture",
		"models": []any{map[string]any{"id": "test", "name": "Test", "contextWindow": 100000, "maxTokens": 4096}},
	}}})
	// An existing settings.json skips the first-time setup dialog (D88) in interactive mode.
	f.write(t, filepath.Join(f.agentDir, "settings.json"), "{}")
	f.write(t, filepath.Join(f.agentDir, "models.json"), string(models))
	f.write(t, filepath.Join(f.cwd, "AGENTS.md"), "BASE-CONTEXT-MARK")
	f.skill(t, "demo", "DEMO-SKILL-MARK")
	return f
}

func (f *runPromptFixture) write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *runPromptFixture) skill(t *testing.T, name, description string) {
	t.Helper()
	f.write(t, filepath.Join(f.agentDir, "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: "+description+"\n---\nbody\n")
}

// command is the real binary, or the pinned Pi for impl "pi", started in the project with the fixture's model, session directory, extension and an appended BASE-APPEND-MARK.
func (f *runPromptFixture) command(t *testing.T, impl, binary string, extra ...string) *exec.Cmd {
	t.Helper()
	args := append([]string{"--model", "fixture/test", "--no-prompt-templates", "--session-dir", f.sessionDir, "--append-system-prompt", "BASE-APPEND-MARK", "-e", f.extension}, extra...)
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Env = append(os.Environ(), "HOME="+f.home, "PIG_HOME="+filepath.Join(f.home, ".pig"), "PIG_CODING_AGENT_DIR="+f.agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "RUN_PROMPT_PROBE_DIR="+f.probeDir)
	if impl == "pi" {
		cmd = exec.CommandContext(t.Context(), "node", append([]string{pinnedPiCLI(t)}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+f.home, "PI_CODING_AGENT_DIR="+f.agentDir, "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "RUN_PROMPT_PROBE_DIR="+f.probeDir)
	}
	cmd.Dir = f.cwd
	return cmd
}

// probed reports whether the extension has written the probe name.
func (f *runPromptFixture) probed(name string) bool {
	_, err := os.Stat(filepath.Join(f.probeDir, name))
	return err == nil
}

// probes lists the probes the extension has written.
func (f *runPromptFixture) probes() []string {
	entries, _ := os.ReadDir(f.probeDir)
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

func (f *runPromptFixture) probe(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.probeDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// pinnedPiCLI is the command line of the pinned Pi in the SDK's node_modules, the Pi the other comparisons of this package run.
func pinnedPiCLI(t *testing.T) string {
	t.Helper()
	piRoot, err := filepath.Abs("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := os.ReadFile(filepath.Join(piRoot, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(metadata, &pkg); err != nil || pkg.Version != pigversion.UpstreamPackageVersion {
		t.Fatalf("Pi version = %q, %v", pkg.Version, err)
	}
	return filepath.Join(piRoot, "dist", "cli.js")
}

// checkPromptMarks reports every mark of want that text lacks and every mark of absent that it keeps.
func checkPromptMarks(t *testing.T, what, text string, want, absent []string) {
	t.Helper()
	for _, mark := range want {
		if !strings.Contains(text, mark) {
			t.Errorf("%s lacks %q:\n%s", what, mark, text)
		}
	}
	for _, mark := range absent {
		if strings.Contains(text, mark) {
			t.Errorf("%s keeps %q:\n%s", what, mark, text)
		}
	}
}

const (
	skillReaderBash = "Use bash to load a skill's file"
	skillReaderRead = "Use the read tool to load a skill's file"
)

// Pi builds the run's prompt once, from the options the before_agent_start handlers shared, after it applied the live loadout to them (.upstream/v1.5.2/packages/coding-agent/src/core/agent-session.ts:2005-2043 and 1828-1840; runner.ts:1434-1470 hands every handler one options object and stores a returned systemPrompt as forceSystemPrompt). A handler that also narrows the loadout to bash therefore still sees its option edits in the request, and the skills section names bash as the reader (system-prompt.ts:177, skills.ts:368-370). Every shape runs under the pinned Pi too, with the same extension, skill, arguments and scripted OpenAI-compatible model, and Pi's request has to meet the same expectations.
func TestRunPromptOptionEditsSurviveALoadoutChangeInEveryMode(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	const narrow = `pi.setActiveTools(["bash"]);`
	const edit = `const o = event.systemPromptOptions; o.appendSystemPrompt = "RUN-APPEND-MARK"; o.contextFiles.push({ path: "/run/AGENTS.md", content: "RUN-CONTEXT-MARK" });`
	shapes := []struct {
		name, source string
		args         []string
		want, absent []string
		exact        string
	}{
		{
			name:   "options",
			source: `export default function (pi) { pi.on("before_agent_start", (event) => { ` + narrow + edit + ` }); }`,
			want:   []string{"RUN-APPEND-MARK", "RUN-CONTEXT-MARK", "BASE-CONTEXT-MARK"},
		},
		{
			name:   "custom prompt option",
			source: `export default function (pi) { pi.on("before_agent_start", (event) => { ` + narrow + ` event.systemPromptOptions.customPrompt = "RUN-CUSTOM-MARK"; }); }`,
			want:   []string{"RUN-CUSTOM-MARK", "BASE-APPEND-MARK", "BASE-CONTEXT-MARK"},
		},
		{
			name:   "custom base prompt",
			source: `export default function (pi) { pi.on("before_agent_start", (event) => { ` + narrow + edit + ` }); }`,
			args:   []string{"--system-prompt", "CUSTOM-BASE-MARK"},
			want:   []string{"CUSTOM-BASE-MARK", "RUN-APPEND-MARK", "RUN-CONTEXT-MARK", "BASE-CONTEXT-MARK"},
		},
		{
			name:   "two handlers",
			source: `export default function (pi) { pi.on("before_agent_start", (event) => { ` + narrow + ` event.systemPromptOptions.appendSystemPrompt = "FIRST-MARK"; }); pi.on("before_agent_start", (event) => { event.systemPromptOptions.appendSystemPrompt += " SECOND-MARK"; }); }`,
			want:   []string{"FIRST-MARK SECOND-MARK", "BASE-CONTEXT-MARK"},
		},
		{
			name:   "replaced prompt",
			source: `export default function (pi) { pi.on("before_agent_start", (event) => { ` + narrow + edit + ` return { systemPrompt: "FORCED-PROMPT-MARK" }; }); }`,
			exact:  "FORCED-PROMPT-MARK",
		},
	}
	for _, shape := range shapes {
		for _, mode := range []string{"print", "interactive"} {
			for _, impl := range []string{"pig", "pi"} {
				t.Run(shape.name+"/"+mode+"/"+impl, func(t *testing.T) {
					f := newRunPromptFixture(t, 1)
					f.write(t, f.extension, shape.source)
					args := shape.args
					if mode == "print" {
						args = append(slices.Clone(args), "--print", "hello")
					}
					cmd := f.command(t, impl, binary, args...)
					var output strings.Builder
					if mode == "interactive" {
						driveInteractivePrompts(t, cmd, []string{"hello"}, func() int { return len(f.chat.recorded()) }, 1, &output)
					} else {
						cmd.Stdin = strings.NewReader("")
						if out, err := cmd.CombinedOutput(); err != nil {
							t.Fatalf("print mode: %v\n%s", err, out)
						}
					}
					requests := f.chat.recorded()
					if len(requests) != 1 {
						t.Fatalf("requests = %d, want 1\n%s", len(requests), output.String())
					}
					system := requests[0].System
					if shape.exact != "" {
						if system != shape.exact {
							t.Fatalf("system prompt = %q, want %q", system, shape.exact)
						}
						return
					}
					absent := []string{skillReaderRead}
					if !slices.Contains(shape.want, "BASE-APPEND-MARK") {
						absent = append(absent, "BASE-APPEND-MARK")
					}
					checkPromptMarks(t, "the request", system, append([]string{"DEMO-SKILL-MARK", skillReaderBash}, shape.want...), absent)
				})
			}
		}
	}
}

// promptProbeExtension is a Node extension that writes what ctx.getSystemPrompt() returns to the probe directory: at the first turn of a run as <tag>-turn, once the run has settled as <tag>-settled, and for the command /probe <name> as <name>. A reload makes the tag "reloaded" and writes the empty probe reloaded: Pi imports an unchanged module path once, so the instance a reload starts is told apart by its session_start, not by new source. handler is the body of its before_agent_start handler.
func promptProbeExtension(tag, handler string) string {
	return `import { readFileSync, renameSync, writeFileSync } from "node:fs";
import { join } from "node:path";
const probe = (name, text) => {
	const file = join(process.env.RUN_PROMPT_PROBE_DIR, name);
	writeFileSync(file + ".tmp", text);
	renameSync(file + ".tmp", file);
};
const mark = () => readFileSync(join(process.env.RUN_PROMPT_PROBE_DIR, "handler-mark"), "utf8");
export default function (pi) {
	let tag = "` + tag + `";
	pi.registerCommand("probe", { description: "writes the system prompt", handler: async (args, ctx) => probe(args.trim(), ctx.getSystemPrompt()) });
	pi.on("session_start", (event) => { if (event.reason === "reload") { tag = "reloaded"; probe("reloaded", ""); } });
	pi.on("turn_start", (_event, ctx) => probe(tag + "-turn", ctx.getSystemPrompt()));
	pi.on("agent_settled", (_event, ctx) => probe(tag + "-settled", ctx.getSystemPrompt()));
	pi.on("before_agent_start", (event) => { ` + handler + ` });
}
`
}

// The prompt an extension reads and the prompt the next run sends stay in step across a loadout change in print and interactive mode, and across a /reload and a resume in interactive mode, as with Pi's one AgentSession: session.systemPrompt is built from the run's options during a run and from the base options outside one (agent-session.ts:1554-1556), _rebuildSystemPrompt rebuilds the base options from the resource loader whenever the loadout changes (1611-1616, 1790-1816), the run's options are dropped before agent_settled (1983), reload() reloads the resources and builds the runtime again (3838-3859), and a resumed session restores the loadout its transcript recorded before its first run. Every scenario runs under the pinned Pi too, with the same extension, and Pi has to meet the same expectations.
func TestExtensionPromptFollowsTheLoadoutAReloadAndAResume(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	const narrow = `pi.setActiveTools(["bash"]);`
	const edit = `const o = event.systemPromptOptions; o.appendSystemPrompt = "RUN-APPEND-MARK"; o.contextFiles.push({ path: "/run/AGENTS.md", content: "RUN-CONTEXT-MARK" });`
	// answered is the done test of a prompt: the model saw requests requests and the run that tag's extension probed has settled.
	answered := func(f *runPromptFixture, requests int, tag string) (func() bool, func() string) {
		return func() bool { return len(f.chat.recorded()) >= requests && f.probed(tag+"-settled") }, func() string {
			return fmt.Sprintf("the model saw %d of %d requests, the run settled: %t", len(f.chat.recorded()), requests, f.probed(tag+"-settled"))
		}
	}
	probeStep := func(f *runPromptFixture, name string) interactiveStep {
		return interactiveStep{input: "/probe " + name, done: func() bool { return f.probed(name) }, pending: func() string { return fmt.Sprintf("no probe %s (probes %v)", name, f.probes()) }}
	}
	for _, impl := range []string{"pig", "pi"} {
		for _, mode := range []string{"print", "interactive"} {
			t.Run("loadout/"+mode+"/"+impl, func(t *testing.T) {
				f := newRunPromptFixture(t, 1)
				f.write(t, f.extension, promptProbeExtension("first", narrow+edit))
				idle := []string{"first-settled"}
				if mode == "print" {
					cmd := f.command(t, impl, binary, "--print", "hello")
					cmd.Stdin = strings.NewReader("")
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("print mode: %v\n%s", err, out)
					}
				} else {
					done, pending := answered(f, 1, "first")
					var output strings.Builder
					driveInteractiveSteps(t, f.command(t, impl, binary), []interactiveStep{
						probeStep(f, "start"),
						{input: "hello", done: done, pending: pending},
						probeStep(f, "idle"),
					}, &output)
					checkPromptMarks(t, "the prompt an extension reads before the first run", f.probe(t, "start"),
						[]string{skillReaderRead, "BASE-APPEND-MARK", "BASE-CONTEXT-MARK", "DEMO-SKILL-MARK"}, []string{skillReaderBash})
					idle = append(idle, "idle")
				}
				requests := f.chat.recorded()
				if len(requests) != 1 {
					t.Fatalf("requests = %d, want 1", len(requests))
				}
				for what, text := range map[string]string{"the request": requests[0].System, "the prompt an extension reads during the run": f.probe(t, "first-turn")} {
					checkPromptMarks(t, what, text,
						[]string{skillReaderBash, "RUN-APPEND-MARK", "RUN-CONTEXT-MARK", "BASE-CONTEXT-MARK", "DEMO-SKILL-MARK"}, []string{skillReaderRead, "BASE-APPEND-MARK"})
				}
				for _, name := range idle {
					checkPromptMarks(t, "the prompt an extension reads after the run ("+name+")", f.probe(t, name),
						[]string{skillReaderBash, "BASE-APPEND-MARK", "BASE-CONTEXT-MARK", "DEMO-SKILL-MARK"}, []string{skillReaderRead, "RUN-APPEND-MARK", "RUN-CONTEXT-MARK"})
				}
			})
		}
		t.Run("reload/"+impl, func(t *testing.T) {
			f := newRunPromptFixture(t, 2)
			// The handler appends the mark it reads from a file at each run, so the run after the reload is told apart without new extension source.
			f.write(t, f.extension, promptProbeExtension("first", narrow+`event.systemPromptOptions.appendSystemPrompt += " " + mark();`))
			f.write(t, filepath.Join(f.probeDir, "handler-mark"), "FIRST-HANDLER-MARK")
			first, firstPending := answered(f, 1, "first")
			second, secondPending := answered(f, 2, "reloaded")
			var output strings.Builder
			driveInteractiveSteps(t, f.command(t, impl, binary), []interactiveStep{
				{input: "hello", done: first, pending: firstPending},
				{
					before: func() {
						f.write(t, filepath.Join(f.cwd, "AGENTS.md"), "RELOADED-CONTEXT-MARK")
						f.skill(t, "added", "RELOADED-SKILL-MARK")
						f.write(t, filepath.Join(f.probeDir, "handler-mark"), "SECOND-HANDLER-MARK")
					},
					input: "/reload", shows: "Reloaded keybindings",
					done: func() bool { return f.probed("reloaded") }, pending: func() string { return fmt.Sprintf("the reload has not finished (probes %v)", f.probes()) },
				},
				probeStep(f, "after-reload"),
				{input: "again", done: second, pending: secondPending},
			}, &output)
			reloaded := []string{skillReaderBash, "RELOADED-CONTEXT-MARK", "RELOADED-SKILL-MARK", "DEMO-SKILL-MARK", "BASE-APPEND-MARK"}
			checkPromptMarks(t, "the prompt an extension reads after the reload", f.probe(t, "after-reload"), reloaded,
				[]string{skillReaderRead, "BASE-CONTEXT-MARK", "FIRST-HANDLER-MARK", "SECOND-HANDLER-MARK"})
			requests := f.chat.recorded()
			if len(requests) != 2 {
				t.Fatalf("requests = %d, want 2\n%s", len(requests), output.String())
			}
			checkPromptMarks(t, "the first request", requests[0].System,
				[]string{skillReaderBash, "BASE-CONTEXT-MARK", "DEMO-SKILL-MARK", "BASE-APPEND-MARK FIRST-HANDLER-MARK"}, []string{skillReaderRead, "RELOADED-SKILL-MARK"})
			for what, text := range map[string]string{"the request after the reload": requests[1].System, "the prompt an extension reads during the run after the reload": f.probe(t, "reloaded-turn")} {
				checkPromptMarks(t, what, text, append(slices.Clone(reloaded), "BASE-APPEND-MARK SECOND-HANDLER-MARK"), []string{skillReaderRead, "BASE-CONTEXT-MARK", "FIRST-HANDLER-MARK"})
			}
		})
		t.Run("resume/"+impl, func(t *testing.T) {
			f := newRunPromptFixture(t, 2)
			// The first process records a loadout of bash alone in the session file.
			t.Run("seed", func(t *testing.T) {
				f.write(t, f.extension, promptProbeExtension("first", narrow))
				done, pending := answered(f, 1, "first")
				var output strings.Builder
				driveInteractiveSteps(t, f.command(t, impl, binary), []interactiveStep{{input: "hello", done: done, pending: pending}}, &output)
			})
			// The second resumes it with a handler that edits the options and leaves the loadout alone.
			f.write(t, f.extension, promptProbeExtension("second", edit))
			done, pending := answered(f, 2, "second")
			var output strings.Builder
			driveInteractiveSteps(t, f.command(t, impl, binary, "--continue"), []interactiveStep{
				probeStep(f, "resumed"),
				{input: "again", done: done, pending: pending},
			}, &output)
			checkPromptMarks(t, "the prompt an extension reads in the resumed session", f.probe(t, "resumed"),
				[]string{skillReaderBash, "BASE-APPEND-MARK", "BASE-CONTEXT-MARK", "DEMO-SKILL-MARK"}, []string{skillReaderRead})
			requests := f.chat.recorded()
			if len(requests) != 2 {
				t.Fatalf("requests = %d, want 2\n%s", len(requests), output.String())
			}
			if !slices.Equal(requests[1].Tools, []string{"bash"}) {
				t.Errorf("the resumed run declares %v, want the recorded loadout [bash]", requests[1].Tools)
			}
			for what, text := range map[string]string{"the resumed run's request": requests[1].System, "the prompt an extension reads during the resumed run": f.probe(t, "second-turn")} {
				checkPromptMarks(t, what, text,
					[]string{skillReaderBash, "RUN-APPEND-MARK", "RUN-CONTEXT-MARK", "BASE-CONTEXT-MARK", "DEMO-SKILL-MARK"}, []string{skillReaderRead, "BASE-APPEND-MARK"})
			}
		})
	}
}
