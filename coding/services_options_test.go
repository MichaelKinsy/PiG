package coding

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func flagExtension(flags ...extension.ExtensionFlag) extension.Extension {
	registered := map[string]extension.ExtensionFlag{}
	for _, flag := range flags {
		registered[flag.Name] = flag
	}
	return extension.Extension{Flags: registered}
}

// Ports agent-session-services.ts applyExtensionFlagValues: a boolean flag is true, a string flag needs a string, and names no
// extension registered are listed in one error, in command-line order, with a plural only for several.
// Pi: packages/coding-agent/src/core/agent-session-services.ts:79 (AgentSessionServices.diagnostics).
func TestApplyExtensionFlagValuesFollowsUpstream(t *testing.T) {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), ExtensionFlagValues: []ExtensionFlagValue{
		{Name: "zeta", Value: true}, {Name: "plan", Value: "yes"}, {Name: "mode", Value: true}, {Name: "name", Value: "pi"}, {Name: "alpha", Value: "x"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	values, diagnostics := services.ApplyExtensionFlagValues([]extension.Extension{flagExtension(
		extension.ExtensionFlag{Name: "plan", Type: extension.FlagBoolean},
		extension.ExtensionFlag{Name: "mode", Type: extension.FlagString},
		extension.ExtensionFlag{Name: "name", Type: extension.FlagString},
	)})
	if want := map[string]any{"plan": true, "name": "pi"}; len(values) != len(want) || values["plan"] != true || values["name"] != "pi" {
		t.Fatalf("values = %v, want %v", values, want)
	}
	var messages []string
	for _, d := range diagnostics {
		if d.Type != "error" {
			t.Errorf("diagnostic type %q, want error", d.Type)
		}
		messages = append(messages, d.Message)
	}
	want := []string{`Extension flag "--mode" requires a value`, "Unknown options: --zeta, --alpha"}
	if !slices.Equal(messages, want) {
		t.Fatalf("diagnostics = %q, want %q", messages, want)
	}
	if got := services.Diagnostics(); len(got) != 2 {
		t.Fatalf("Services.Diagnostics = %v", got)
	}
}

func TestApplyExtensionFlagValuesUsesTheSingularForOneUnknownName(t *testing.T) {
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), ExtensionFlagValues: []ExtensionFlagValue{{Name: "bogus", Value: true}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	_, diagnostics := services.ApplyExtensionFlagValues(nil)
	if len(diagnostics) != 1 || diagnostics[0].Message != "Unknown option: --bogus" {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	none, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(none.Close)
	if values, diagnostics := none.ApplyExtensionFlagValues(nil); values != nil || diagnostics != nil {
		t.Fatalf("no flag values: %v %v", values, diagnostics)
	}
}

// createAgentSessionServices: `options.modelRuntime ?? ModelRuntime.create(...)`. Services built around a caller's runtime share it and
// leave it running when they close.
// Pi: packages/coding-agent/src/core/agent-session-services.ts:41 (CreateAgentSessionServicesOptions.modelRuntime).
func TestServicesShareAnInjectedModelRuntimeAndDoNotCloseIt(t *testing.T) {
	agentDir := t.TempDir()
	owner, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	runtime := owner.ModelRuntime()
	other, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), ModelRuntime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	if other.ModelRuntime() != runtime || other.Registry() != owner.Registry() || other.Credentials() != owner.Credentials() {
		t.Fatal("Services did not share the injected runtime")
	}
	if other.AgentDir() != agentDir {
		t.Fatalf("agent dir = %q, want the runtime's %q", other.AgentDir(), agentDir)
	}
	other.Close()
	// The runtime still admits model work after the borrowing Services closed.
	if !runtime.startBackground(func(context.Context) {}) {
		t.Fatal("closing borrowing Services closed the shared runtime")
	}
}

// resourceLoaderOptions and resourceLoaderReloadOptions: the Services load their resources once with the options,
// resolveProjectTrust runs first and sets the project trust the load sees, and a Session built from the Services uses that loader.
// Pi: packages/coding-agent/src/core/agent-session-services.ts:44 (CreateAgentSessionServicesOptions.resourceLoaderOptions); packages/coding-agent/src/core/agent-session-services.ts:45 (CreateAgentSessionServicesOptions.resourceLoaderReloadOptions).
func TestServicesLoadResourcesWithTheirOptionsAndTheSessionUsesTheLoader(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	skill := filepath.Join(cwd, ".pig", "skills", "local", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: local\ndescription: project skill\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	system := "custom system prompt"
	var asked int
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{
		CWD: cwd, AgentDir: agentDir, ProjectTrusted: new(false),
		ResourceLoaderOptions: &DefaultResourceLoaderOptions{SystemPrompt: &system, NoContextFiles: true},
		ResourceLoaderReloadOptions: &ResourceLoaderReloadOptions{ResolveProjectTrust: func(context.Context, ResolveProjectTrustInput) (bool, error) {
			asked++
			return true, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	loader := services.ResourceLoader()
	if loader == nil || asked != 1 {
		t.Fatalf("loader = %v, trust asked %d times", loader, asked)
	}
	if !services.SettingsManager().IsProjectTrusted() {
		t.Fatal("the trust decision was not applied to the settings")
	}
	if text, ok := loader.GetSystemPrompt(); !ok || text != system {
		t.Fatalf("system prompt = %q, %v", text, ok)
	}
	if names := skillNames(loader); !slices.Contains(names, "local") {
		t.Fatalf("skills = %v, want the project skill loaded after trust was granted", names)
	}
	ref, err := resolveResourceLoader(services, nil)
	if err != nil || ref.loader != ResourceLoader(loader) {
		t.Fatalf("a Session did not use the Services' loader: %v %v", ref, err)
	}
}

func TestServicesResourceReloadFailsWhenTheTrustDecisionFails(t *testing.T) {
	_, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), ResourceLoaderReloadOptions: &ResourceLoaderReloadOptions{
		ResolveProjectTrust: func(context.Context, ResolveProjectTrustInput) (bool, error) { return false, os.ErrPermission },
	}})
	if err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("err = %v, want the trust decision's failure", err)
	}
	plain, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(plain.Close)
	if plain.ResourceLoader() != nil {
		t.Fatal("Services created a loader nobody asked for")
	}
}
