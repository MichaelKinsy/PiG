//go:build !pig_strip_codemode && !pig_strip_tool_search

package coding

// Ports .upstream/v0.99.1/packages/coding-agent/test/suite/agent-session-tool-orchestration.test.ts case 2 ("registers codemode and
// tool_search inactive until they are named", :115-131) over the native builtin:codemode and builtin:tool-search extensions, which a
// Piglet Binary built with pig_strip_codemode or pig_strip_tool_search does not have (D92). The other cases are in
// session_tool_orchestration_upstream_test.go.

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/builtin"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// upstream agent-session-tool-orchestration.test.ts:115-131. `initialActiveToolNames` of upstream's harness is the SDK's initial selection
// (sdk.ts:264-269: options.tools, else the defaultTools setting); `allowedToolNames` is --tools.
func TestSessionToolOrchestrationRegistersCodemodeAndToolSearchInactiveUntilNamed(t *testing.T) {
	open := func(t *testing.T, settings icodingagent.Settings, allowed map[string]struct{}) *Session {
		t.Helper()
		services := newTestServicesWithSettings(t, settings)
		faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
		if err := services.ModelRuntime().RegisterNativeProvider(faux.Provider()); err != nil {
			t.Fatal(err)
		}
		configureFauxAuth(t, services)
		var extensions []extension.Extension
		for _, path := range []string{"builtin:codemode", "builtin:tool-search"} {
			entry, err := builtin.Resolve(path, builtin.Options{})
			if err != nil {
				t.Fatal(err)
			}
			ext, err := factoryload.LoadExtensionFromFactory(entry.Factory, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), entry.Path())
			if err != nil {
				t.Fatal(err)
			}
			extensions = append(extensions, ext)
		}
		session, err := NewSession(services, SessionOptions{Model: services.ModelRuntime().GetModel("faux", "faux-1"), Runner: inproc.NewRunner(extensions, t.TempDir()), AllowedTools: allowed})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		eventsCh := session.Events()
		go func() {
			defer close(done)
			for event := range eventsCh {
				AcknowledgeEvent(event)
			}
		}()
		t.Cleanup(func() { _ = session.Close(); <-done })
		if err := session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
			t.Fatal(err)
		}
		return session
	}

	plain := open(t, icodingagent.Settings{}, nil)
	var names []string
	for _, tool := range plain.GetAllTools() {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"codemode", "tool_search"} {
		if !slices.Contains(names, want) {
			t.Fatalf("getAllTools = %v, want %s", names, want)
		}
	}
	assertEqual(t, "plain active", plain.ActiveToolNames(), []string{"read", "bash", "edit", "write"})

	// --tools and the defaultTools setting name them explicitly.
	allowed := open(t, icodingagent.Settings{}, map[string]struct{}{"read": {}, "codemode": {}})
	assertEqual(t, "allowed active", allowed.ActiveToolNames(), []string{"read", "codemode"})
	initial := open(t, icodingagent.Settings{DefaultTools: []string{"tool_search"}}, nil)
	assertEqual(t, "initial active", initial.ActiveToolNames(), []string{"tool_search"})
}
