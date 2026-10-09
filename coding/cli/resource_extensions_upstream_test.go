package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func resourceExtensionFixture(t *testing.T) (root, cwd, agentDir string) {
	t.Helper()
	root = t.TempDir()
	cwd, agentDir = filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIG_HOME", filepath.Join(root, "config"))
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	return root, cwd, agentDir
}

// Ports packages/coding-agent/test/resource-loader.test.ts:213 through the CLI's actual pre-trust and final load phases.
func TestUpstreamResourceLoaderUserExtensionsBeforeTrust(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	trace := filepath.Join(root, "module-loads")
	writeResourceTestFiles(t, root, map[string]string{
		"agent/extensions/user.ts": fmt.Sprintf(`import { appendFileSync } from "node:fs";
appendFileSync(%q,"user\n");
export default function(pi) {
 pi.on("project_trust", () => ({ trusted: "yes" }));
 pi.registerCommand("user-trust", {description:"user trust",handler:async()=>{}});
}`, trace),
		"project/.pig/extensions/project.ts": `export default function(pi) { pi.registerCommand("project-trusted", {description:"project trusted",handler:async()=>{}}); }`,
	})
	sm := codingagent.NewSettingsManager(cwd, agentDir)
	registry := codingagent.NewModelRegistry(agentDir)
	preloaded := &startupExtensionSet{}
	t.Cleanup(preloaded.close)
	userScope := []string{"user"}
	configs := collectExtensionConfigs(cwd, agentDir, sm, Args{}, &userScope)
	loaded, _, _, errs := loadFinalSubprocessExtensions(t.Context(), cwd, extension.ModeTUI, registry, configs, nil, nil, preloaded)
	if len(errs) != 0 || len(loaded) != 1 || loaded[0].Path != filepath.Join(agentDir, "extensions", "user.ts") {
		t.Fatalf("pretrust loaded=%#v errors=%v", loaded, errs)
	}
	configs = collectExtensionConfigs(cwd, agentDir, sm, Args{}, nil)
	loaded, _, _, errs = loadFinalSubprocessExtensions(t.Context(), cwd, extension.ModeTUI, registry, configs, nil, nil, preloaded)
	var paths []string
	for _, ext := range loaded {
		paths = append(paths, ext.Path)
	}
	want := []string{filepath.Join(cwd, ".pig", "extensions", "project.ts"), filepath.Join(agentDir, "extensions", "user.ts")}
	if len(errs) != 0 || !slices.Equal(paths, want) {
		t.Fatalf("final paths=%q errors=%v; want %q", paths, errs, want)
	}
	data, err := os.ReadFile(trace)
	if err != nil || string(data) != "user\n" {
		t.Fatalf("user module evaluations=%q error=%v; want one", data, err)
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:260,868,912 through selection, the subprocess Host, conflict diagnostics and the actual command/tool runner.
func TestUpstreamResourceLoaderExtensionConflicts(t *testing.T) {
	toolFactory := func(description, result string) string {
		return fmt.Sprintf(`import { Type } from "typebox";
export default function(pi) {
 pi.registerTool({name:"duplicate-tool",description:%q,parameters:Type.Object({}),execute:async()=>({result:%q})});
}`, description, result)
	}
	for _, kind := range []string{"command collisions", "tool conflicts", "explicit CLI precedence"} {
		t.Run(kind, func(t *testing.T) {
			root, cwd, agentDir := resourceExtensionFixture(t)
			flags := Args{}
			switch kind {
			case "command collisions":
				writeResourceTestFiles(t, root, map[string]string{
					"project/.pig/extensions/project.ts": `export default function(pi) { pi.registerCommand("deploy",{description:"project deploy",handler:async()=>{}}); pi.registerCommand("project-only",{description:"project only",handler:async()=>{}}); }`,
					"agent/extensions/user.ts":           `export default function(pi) { pi.registerCommand("deploy",{description:"user deploy",handler:async()=>{}}); pi.registerCommand("user-only",{description:"user only",handler:async()=>{}}); }`,
				})
			case "tool conflicts":
				writeResourceTestFiles(t, root, map[string]string{"agent/extensions/ext1/index.ts": toolFactory("First", "1"), "agent/extensions/ext2/index.ts": toolFactory("Second", "2")})
			case "explicit CLI precedence":
				for _, row := range []struct{ path, scope string }{{"agent/extensions/global.ts", "global"}, {"explicit-extension.ts", "explicit"}} {
					source := strings.Replace(toolFactory(row.scope+" tool", row.scope), "\n}", fmt.Sprintf(`
 pi.registerCommand("deploy",{description:%q,handler:async()=>{}});
}`, row.scope+" command"), 1)
					writeResourceTestFiles(t, root, map[string]string{row.path: source})
				}
				flags.Extensions = []string{filepath.Join(root, "explicit-extension.ts")}
			}
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			configs := collectExtensionConfigs(cwd, agentDir, sm, flags, nil)
			host := subprocess.NewHostWithConfigRoot(cwd, filepath.Join(root, "host"))
			t.Cleanup(func() { host.Shutdown("test done") })
			loaded, errs := host.LoadAll(t.Context(), configs)
			if len(errs) != 0 || len(loaded) != 2 {
				t.Fatalf("loaded=%#v errors=%v; want two extensions", loaded, errs)
			}
			conflicts := codingagent.DetectExtensionConflicts(loaded)
			runner := inproc.NewRunner(loaded, cwd, host.Runtime())
			t.Cleanup(func() { runner.Invalidate("") })
			command := func(name, want string) {
				t.Helper()
				got, ok := runner.Command(name)
				if !ok || got.Description != want {
					t.Errorf("command %q=%+v found=%t; want %q", name, got, ok, want)
				}
			}
			switch kind {
			case "command collisions":
				for _, conflict := range conflicts {
					if strings.Contains(conflict.Message, `Command "/deploy" conflicts`) {
						t.Errorf("command collision rejected an extension: %v", conflict)
					}
				}
				command("deploy:1", "project deploy")
				command("deploy:2", "user deploy")
				command("project-only", "project only")
				command("user-only", "user only")
				var names []string
				for _, cmd := range runner.Commands() {
					names = append(names, cmd.InvocationName)
				}
				if want := []string{"deploy:1", "project-only", "deploy:2", "user-only"}; !slices.Equal(names, want) {
					t.Errorf("command order=%q; want %q", names, want)
				}
			case "tool conflicts":
				if !slices.ContainsFunc(conflicts, func(conflict codingagent.ExtensionConflict) bool {
					return strings.Contains(conflict.Message, "duplicate-tool") && strings.Contains(conflict.Message, "conflicts")
				}) {
					t.Fatalf("tool conflict errors=%v", conflicts)
				}
			case "explicit CLI precedence":
				if loaded[0].Path != flags.Extensions[0] {
					t.Errorf("first path=%q; want explicit %q", loaded[0].Path, flags.Extensions[0])
				}
				command("deploy:1", "explicit command")
				command("deploy:2", "global command")
				tool, ok := runner.GetToolDefinition("duplicate-tool")
				if !ok || tool.Description != "explicit tool" {
					t.Fatalf("winning tool=%+v found=%t", tool, ok)
				}
			}
		})
	}
}

// resource-loader.ts detectExtensionConflicts reports a later extension's duplicate tools in its ext.tools Map order. The subprocess loader records that order, and startup reports conflicts in it.
func TestExtensionConflictDiagnosticsUseToolRegistrationOrder(t *testing.T) {
	root, cwd, agentDir := resourceExtensionFixture(t)
	factory := `import { Type } from "typebox"; export default function(pi) {
 for (const name of ["zeta", "alpha"]) pi.registerTool({name, description: name, parameters: Type.Object({}), execute: async () => ({content: []})});
}`
	writeResourceTestFiles(t, root, map[string]string{"agent/extensions/ext1/index.ts": factory, "agent/extensions/ext2/index.ts": factory})
	configs := collectExtensionConfigs(cwd, agentDir, codingagent.NewSettingsManager(cwd, agentDir), Args{}, nil)
	host := subprocess.NewHostWithConfigRoot(cwd, filepath.Join(root, "host"))
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, errs := host.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(loaded) != 2 {
		t.Fatalf("loaded=%#v errors=%v; want two extensions", loaded, errs)
	}
	first, second := filepath.Join(agentDir, "extensions", "ext1", "index.ts"), filepath.Join(agentDir, "extensions", "ext2", "index.ts")
	var got []string
	for _, diagnostic := range extensionConflictDiagnostics(codingagent.DetectExtensionConflicts(loaded)) {
		got = append(got, diagnostic.Message)
	}
	want := []string{
		fmt.Sprintf(`Failed to load extension "%s": Tool "zeta" conflicts with %s`, second, first),
		fmt.Sprintf(`Failed to load extension "%s": Tool "alpha" conflicts with %s`, second, first),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("conflict diagnostics = %q, want %q", got, want)
	}
}

// Upstream main.ts formats resource-loader extension errors, including conflicts, as `Failed to load extension "${path}": ${error}` with no escaping, so a double quote in the path is printed verbatim.
func TestExtensionConflictDiagnosticsInterpolatePathVerbatim(t *testing.T) {
	got := extensionConflictDiagnostics([]codingagent.ExtensionConflict{{Path: `/x/a"b/index.ts`, Message: "m"}})
	if len(got) != 1 || got[0].Message != `Failed to load extension "/x/a"b/index.ts": m` {
		t.Fatalf("diagnostics = %#v", got)
	}
}
