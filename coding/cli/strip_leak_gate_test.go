package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/internal/pigstrip/leakgate"
)

// The strip leak gate of the CLI surfaces, the half of internal/codingagent's TestStripLeakGate that needs cmd/pig. It
// takes its cases from the generated strip ID table (pigstrip.Lists, pigstrip.Known) and the names from leakgate's
// tables, so a new ID is covered without an edit here. The in-process surfaces record a runtime strip of each ID in
// pigstrip, as `pig` does for an active Piglet; the session surfaces run `pig --mode rpc --piglet` with a Piglet that
// strips the ID; the stripped-Binary surfaces run cmd/pig built with every strip build tag.
//
// Pi's getCommands and RPC get_commands list no built-in slash command, so the interactive gate covers those; the
// session gate covers the command IDs those surfaces do list: the Piglet extension's /piglet, which any session with an
// active Piglet shows. RPC commands of stripped slash commands stay (D92 decision #11).
//
// pig additive (D92): Stock PiG strips nothing, so every surface here is Pi's.

// cliLeakSurface renders one CLI surface as plain text. lists names the strip lists whose IDs the surface can show.
type cliLeakSurface struct {
	name   string
	lists  []string
	render func(t *testing.T) string
}

// cliLeakIncidental are lines of a surface that name an ID in another sense. Pi's system prompt says "read" as a verb
// in its docs section and names ls, grep and find as shell commands in the bash tool's line and, when bash is the only
// file tool left, in its bash guideline; --help's edit row says "find/replace".
var cliLeakIncidental = map[string][]string{
	"--help": {
		// The example session path is a file name, not the /session command.
		"~/.pig/agent/sessions/--path--/session.jsonl",
		"Edit files with find/replace",
	},
	"system prompt": {
		"- bash: Execute bash commands (ls, grep, find, etc.)",
		"- Use bash for file operations like ls, rg, find",
		"PiG documentation (read only when the user asks",
		"read the docs and examples",
		"Always read pig .md files completely",
	},
}

// modelProviders keeps the provider column of a model listing: a model ID such as amazon-bedrock's
// mistral.devstral-2-123b names the model's maker, not a provider.
func modelProviders(listing string) string {
	var providers []string
	for line := range strings.SplitSeq(listing, "\n") {
		if provider, _, _ := strings.Cut(line, " "); provider != "" {
			providers = append(providers, provider)
		}
	}
	return strings.Join(providers, "\n")
}

// stripLeakProviderEnv authenticates the providers each stripped API owns, with Anthropic as the provider no strip
// removes.
var stripLeakProviderEnv = map[string]string{
	"ANTHROPIC_API_KEY": "x", "MISTRAL_API_KEY": "x", "GOOGLE_CLOUD_API_KEY": "x",
	"AWS_ACCESS_KEY_ID": "x", "AWS_SECRET_ACCESS_KEY": "y", "AWS_REGION": "us-east-1",
}

// cliLeakSurfaces are the surfaces cmd/pig renders in this process from its registries.
func cliLeakSurfaces(agentDir string) []cliLeakSurface {
	return []cliLeakSurface{
		{name: "--help", lists: []string{pigstrip.ListTools, pigstrip.ListExtensions, pigstrip.ListAPIs, pigstrip.ListFeatures}, render: func(t *testing.T) string {
			// Help text depends on no credential: render it as a shell without any provider key sees it.
			for name := range stripLeakProviderEnv {
				t.Setenv(name, "")
			}
			var out bytes.Buffer
			printHelp(&out, false)
			return out.String()
		}},
		{name: "pig piglet usage", lists: []string{pigstrip.ListFeatures}, render: func(t *testing.T) string {
			var stdout, stderr bytes.Buffer
			if code := piglet.RunCommand([]string{"piglet"}, &stdout, &stderr); code != 0 {
				t.Fatalf("pig piglet exited %d: %s", code, stderr.String())
			}
			return stdout.String()
		}},
		{name: "pig config", lists: []string{pigstrip.ListExtensions, pigstrip.ListFeatures}, render: func(t *testing.T) string {
			cwd := t.TempDir()
			settings := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
			selector, err := newScopedConfigSelector(cwd, agentDir, settings, settings, false, false, cliBuiltinExtensionNames())
			if err != nil {
				t.Fatal(err)
			}
			selector.SetTerminalRows(500)
			return strings.Join(selector.Render(200), "\n")
		}},
		{name: "--list-models", lists: []string{pigstrip.ListAPIs}, render: func(t *testing.T) string {
			for name, value := range stripLeakProviderEnv {
				t.Setenv(name, value)
			}
			return modelProviders(captureStdout(func() { printModelList(codingagent.NewModelRegistry(agentDir), agentDir, "") }))
		}},
	}
}

// writeStripLeakResources gives agentDir one skill, one prompt template and one theme, so the surfaces that list
// resources have one of each to leave out.
func writeStripLeakResources(t *testing.T, agentDir string) {
	t.Helper()
	files := map[string]string{
		"settings.json":                    "{}",
		"skills/skill-probe/SKILL.md":      "---\nname: skill-probe\ndescription: Probe skill\n---\nProbe.\n",
		"prompts/prompt-template-probe.md": "---\ndescription: Probe prompt\n---\nProbe.\n",
		"themes/themes-probe.json":         `{"name": "themes-probe", "vars": {}, "colors": {}}`,
	}
	for name, content := range files {
		path := filepath.Join(agentDir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestStripLeakGate strips every generated strip ID in turn and fails when a CLI, extension API, RPC or system prompt
// surface still shows it, and when cmd/pig built with every strip build tag shows a built-in it compiled out. It fails
// too when a strip list maps to no surface here.
func TestStripLeakGate(t *testing.T) {
	sessionLists := []string{pigstrip.ListTools, pigstrip.ListCommands, pigstrip.ListExtensions, pigstrip.ListAPIs, pigstrip.ListFeatures}
	for _, surface := range stripLeakSessionSurfaces {
		for _, list := range surface.lists {
			if !slices.Contains(sessionLists, list) {
				t.Fatalf("session surface %s maps list %s that the gate runs no session for", surface.name, list)
			}
		}
	}
	for _, list := range pigstrip.Lists() {
		mapped := slices.ContainsFunc(cliLeakSurfaces(""), func(s cliLeakSurface) bool { return slices.Contains(s.lists, list) }) ||
			slices.ContainsFunc(stripLeakSessionSurfaces, func(s stripLeakSessionSurface) bool { return slices.Contains(s.lists, list) })
		if !mapped {
			t.Fatalf("strip list %q maps to no CLI surface", list)
		}
	}
	t.Run("in-process", func(t *testing.T) {
		agentDir := t.TempDir()
		writeStripLeakResources(t, agentDir)
		setStripTestEnv(t, stripLeakProviderEnv)
		t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
		surfaces := cliLeakSurfaces(agentDir)
		for _, surface := range surfaces {
			if strings.TrimSpace(surface.render(t)) == "" {
				t.Fatalf("stock %s renders nothing; the gate would pass vacuously", surface.name)
			}
		}
		for _, list := range pigstrip.Lists() {
			for _, id := range pigstrip.Known(list) {
				t.Run(list+"/"+id, func(t *testing.T) {
					if pigstrip.Has(list, id) {
						t.Skipf("this build compiles %s out", id)
					}
					patterns, err := leakgate.Names(list, id)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(pigstrip.Strip(list, id))
					for _, surface := range surfaces {
						if !slices.Contains(surface.lists, list) {
							continue
						}
						if found := leakgate.Find(surface.render(t), cliLeakIncidental[surface.name], patterns); len(found) > 0 {
							t.Errorf("%s still shows stripped %s %s:\n  %s", surface.name, list, id, strings.Join(found, "\n  "))
						}
					}
				})
			}
		}
	})
	t.Run("session", func(t *testing.T) {
		if testing.Short() {
			t.Skip("runs pig --mode rpc for every strip ID")
		}
		stock := runStripLeakSession(t, nil)
		for _, surface := range stripLeakSessionSurfaces {
			if strings.TrimSpace(stock[surface.name]) == "" {
				t.Fatalf("stock %s renders nothing; the gate would pass vacuously", surface.name)
			}
		}
		// The command surfaces list only extension commands; /piglet needs an active Piglet. A Piglet that strips
		// nothing shows which command IDs a session surface lists, and only those get a stripped session.
		withPiglet := runStripLeakSession(t, map[string][]string{})
		sessionCommands := 0
		for _, list := range sessionLists {
			for _, id := range pigstrip.Known(list) {
				patterns, err := leakgate.Names(list, id)
				if err != nil {
					t.Fatal(err)
				}
				if list == pigstrip.ListCommands {
					if !slices.ContainsFunc(stripLeakSessionSurfaces, func(s stripLeakSessionSurface) bool {
						return slices.Contains(s.lists, list) && len(leakgate.Find(withPiglet[s.name], cliLeakIncidental[s.name], patterns)) > 0
					}) {
						continue
					}
					sessionCommands++
				}
				if len(patterns) == 0 || (list == pigstrip.ListFeatures && id == pigstrip.NodeExtensions) {
					// No surface names the feature; a session without the Node runtime cannot run the probe.
					continue
				}
				t.Run(list+"/"+id, func(t *testing.T) {
					t.Parallel()
					rendered := runStripLeakSession(t, map[string][]string{list: {id}})
					for _, surface := range stripLeakSessionSurfaces {
						if !slices.Contains(surface.lists, list) {
							continue
						}
						if found := leakgate.Find(rendered[surface.name], cliLeakIncidental[surface.name], patterns); len(found) > 0 {
							t.Errorf("%s still shows stripped %s %s:\n  %s", surface.name, list, id, strings.Join(found, "\n  "))
						}
					}
				})
			}
		}
		if sessionCommands == 0 {
			t.Fatal("no session surface lists a command ID under an active Piglet; the commands gate would pass vacuously")
		}
	})
	t.Run("session keep mode", func(t *testing.T) {
		if testing.Short() {
			t.Skip("runs pig --mode rpc")
		}
		// A keep-mode Piglet strips every ID of its keep-mode lists but the kept ones; the same surfaces leave the
		// expanded IDs out, and the kept tools stay.
		kept := map[string][]string{pigstrip.ListTools: {"read", "bash", "edit", "write"}, pigstrip.ListExtensions: {}}
		rendered := runStripLeakSessionYAML(t, "name: leak\nstrip:\n  keep:\n    tools: [read, bash, edit, write]\n    extensions: []\n")
		for _, tool := range kept[pigstrip.ListTools] {
			if !slices.ContainsFunc(strings.Split(rendered["getAllTools"], "\n"), func(line string) bool { return strings.HasPrefix(line, tool+" ") }) {
				t.Fatalf("keep-mode session lacks the kept tool %s; the gate would pass vacuously:\n%s", tool, rendered["getAllTools"])
			}
		}
		for list, keep := range kept {
			for _, id := range pigstrip.Known(list) {
				if slices.Contains(keep, id) {
					continue
				}
				patterns, err := leakgate.Names(list, id)
				if err != nil {
					t.Fatal(err)
				}
				for _, surface := range stripLeakSessionSurfaces {
					if !slices.Contains(surface.lists, list) {
						continue
					}
					if found := leakgate.Find(rendered[surface.name], cliLeakIncidental[surface.name], patterns); len(found) > 0 {
						t.Errorf("%s still shows %s %s, which keep mode strips:\n  %s", surface.name, list, id, strings.Join(found, "\n  "))
					}
				}
			}
		}
	})
	t.Run("stripped binary", func(t *testing.T) {
		tags := piglet.StripBuildTags()
		type strippedID struct{ list, id string }
		var compiledOut []strippedID
		for _, list := range pigstrip.Lists() {
			for _, id := range pigstrip.Known(list) {
				if slices.Contains(tags, pigstrip.Tag(id)) {
					compiledOut = append(compiledOut, strippedID{list, id})
				}
			}
		}
		if len(compiledOut) == 0 {
			t.Fatal("no strip ID has a build tag; the gate would pass vacuously")
		}
		filterAllProviderEnv(t)
		for _, args := range [][]string{{"--help"}, {"piglet"}, {"--list-models"}} {
			if args[0] == "--list-models" {
				for name, value := range stripLeakProviderEnv {
					t.Setenv(name, value)
				}
			}
			stdout, stderr, code := runStrippedPig(t, "", args...)
			if code != 0 {
				t.Fatalf("stripped pig %s exited %d: %s", strings.Join(args, " "), code, stderr)
			}
			if args[0] == "--list-models" {
				stdout = modelProviders(stdout)
			}
			for _, stripped := range compiledOut {
				patterns, err := leakgate.Names(stripped.list, stripped.id)
				if err != nil {
					t.Fatal(err)
				}
				if found := leakgate.Find(stdout, cliLeakIncidental["--help"], patterns); len(found) > 0 {
					t.Errorf("stripped pig %s still shows compiled-out %s %s:\n  %s", strings.Join(args, " "), stripped.list, stripped.id, strings.Join(found, "\n  "))
				}
			}
		}
	})
}

// stripLeakSessionSurface names a surface a session reports: the probe extension's view of the extension API and the
// system prompt, and RPC responses.
type stripLeakSessionSurface struct {
	name  string
	lists []string
}

var stripLeakSessionSurfaces = []stripLeakSessionSurface{
	{name: "system prompt", lists: []string{pigstrip.ListTools, pigstrip.ListExtensions, pigstrip.ListFeatures}},
	{name: "getAllTools", lists: []string{pigstrip.ListTools, pigstrip.ListExtensions}},
	{name: "getCommands", lists: []string{pigstrip.ListCommands, pigstrip.ListExtensions, pigstrip.ListFeatures}},
	{name: "model registry", lists: []string{pigstrip.ListAPIs}},
	{name: "RPC get_commands", lists: []string{pigstrip.ListCommands, pigstrip.ListExtensions, pigstrip.ListFeatures}},
	{name: "RPC get_available_models", lists: []string{pigstrip.ListAPIs}},
}

// stripLeakProbe reports what the extension API shows: every command as the user types it (with its slash, as a strip
// ID names it), every registered tool with the first line of its description, every model of the registry, and the
// system prompt.
const stripLeakProbe = `export default function(pi) {
  pi.registerCommand("leak-dump", {description: "dump", handler: async (_args, ctx) => {
    ctx.ui.notify("LEAKDUMP:" + JSON.stringify({
      "getCommands": pi.getCommands().filter((c) => c.name !== "leak-dump").map((c) => ["/" + c.name, c.description ?? "", c.source, c.sourceInfo?.path ?? ""].join(" ")).join("\n"),
      "getAllTools": pi.getAllTools().map((t) => t.name + " " + (t.description ?? "").split("\n")[0]).join("\n"),
      "model registry": ctx.modelRegistry.getAll().map((m) => m.provider + " " + m.api).join("\n"),
      "system prompt": ctx.getSystemPrompt(),
    }));
  }});
}
`

// runStripLeakSession runs `pig --mode rpc` with every built-in extension, codemode and tool search active, the probe
// extension, a skill and a prompt template, under a Piglet that strips strip (no Piglet when strip is nil, a Piglet that
// strips nothing when it is empty), and returns each session surface.
func runStripLeakSession(t *testing.T, strip map[string][]string) map[string]string {
	t.Helper()
	if strip == nil {
		return runStripLeakSessionYAML(t, "")
	}
	doc := "name: leak\n"
	var yaml strings.Builder
	for list, ids := range strip {
		quoted := make([]string, len(ids))
		for i, id := range ids {
			quoted[i] = fmt.Sprintf("%q", id)
		}
		fmt.Fprintf(&yaml, "  %s: [%s]\n", list, strings.Join(quoted, ", "))
	}
	if yaml.Len() > 0 {
		doc += "strip:\n" + yaml.String()
	}
	return runStripLeakSessionYAML(t, doc)
}

// runStripLeakSessionYAML is runStripLeakSession with the Piglet document as YAML; empty runs without a Piglet.
func runStripLeakSessionYAML(t *testing.T, piglet string) map[string]string {
	t.Helper()
	home, cwd := t.TempDir(), t.TempDir()
	agentDir := filepath.Join(home, "agent")
	writeStripLeakResources(t, agentDir)
	probe := filepath.Join(cwd, "probe.mjs")
	if err := os.WriteFile(probe, []byte(stripLeakProbe), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + agentDir, "PIG_TEST_FAUX=1", "PIG_OFFLINE=1", "NO_COLOR=1"}
	for _, provider := range ai.ListRuntimeProviders() {
		for _, key := range ai.FindEnvKeys(provider, nil) {
			env = append(env, key+"=")
		}
	}
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, "AWS_") || strings.HasPrefix(name, "GOOGLE_") || strings.HasPrefix(name, "ANTHROPIC_") {
			env = append(env, name+"=")
		}
	}
	for name, value := range stripLeakProviderEnv {
		env = append(env, name+"="+value)
	}
	tools := append(pigstrip.Known(pigstrip.ListTools), "codemode", "tool_search")
	args := []string{"--no-session", "--no-context-files", "--no-extensions", "--provider", "test-faux", "--model", "faux-1", "--tools", strings.Join(tools, ","), "-e", probe}
	for _, name := range pigstrip.Known(pigstrip.ListExtensions) {
		args = append(args, "-e", "builtin:"+name)
	}
	if piglet != "" {
		path := filepath.Join(t.TempDir(), "leak.yaml")
		if err := os.WriteFile(path, []byte(piglet), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "--piglet", path)
	}
	p := startRPCProcessAt(t, cwd, env, args...)
	rendered := map[string]string{}
	p.send(`{"id":"commands","type":"get_commands"}`)
	p.send(`{"id":"models","type":"get_available_models"}`)
	p.send(`{"id":"dump","type":"prompt","message":"/leak-dump"}`)
	pending := 3
	p.await("strip leak surfaces", func(record rpcRecord) bool {
		switch {
		case record["type"] == "response" && record["id"] == "commands":
			data, _ := record["data"].(map[string]any)
			commands, _ := data["commands"].([]any)
			var lines []string
			for _, value := range commands {
				command, _ := value.(map[string]any)
				if command["name"] == "leak-dump" {
					continue
				}
				// The command as the user types it, then the record, so a command strip ID's /name matches.
				encoded, _ := json.Marshal(command)
				lines = append(lines, fmt.Sprintf("/%v %s", command["name"], encoded))
			}
			rendered["RPC get_commands"] = strings.Join(lines, "\n")
			pending--
		case record["type"] == "response" && record["id"] == "models":
			data, _ := record["data"].(map[string]any)
			models, _ := data["models"].([]any)
			var lines []string
			for _, value := range models {
				model, _ := value.(map[string]any)
				lines = append(lines, fmt.Sprintf("%v %v", model["provider"], model["api"]))
			}
			rendered["RPC get_available_models"] = strings.Join(lines, "\n")
			pending--
		case record["type"] == "extension_ui_request" && record["method"] == "notify":
			message, _ := record["message"].(string)
			if payload, ok := strings.CutPrefix(message, "LEAKDUMP:"); ok {
				var dump map[string]string
				if err := json.Unmarshal([]byte(payload), &dump); err != nil {
					t.Fatalf("probe dump %q: %v", payload, err)
				}
				maps.Copy(rendered, dump)
				pending--
			}
		}
		return pending == 0
	})
	p.closeAndWait("after the strip leak surfaces")
	return rendered
}
