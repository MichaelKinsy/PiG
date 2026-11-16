//go:build !pig_strip_node_extensions

package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// agent-session.ts _bindExtensionCore binds the Session to its extensions in every mode, and createAgentSession over a DefaultResourceLoader that loads an extension gives that extension a context that reads the Session: its model, scopedModels and idle state, with no UI. A library caller gets what a child session an extension creates gets: the extension runs in the loader's Extension Host, which the Session binds to.
func TestCreateAgentSessionBindsItsStateToTheExtensionsOfItsLoader(t *testing.T) {
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	logFile := filepath.Join(root, "ctx.json")
	probe := filepath.Join(root, "probe.ts")
	writeExtensionFiles(t, map[string]string{
		probe:                      "import { writeFileSync } from \"node:fs\";\nexport default function (pi) { pi.on(\"session_start\", async (_event, ctx) => { writeFileSync(" + strconv.Quote(logFile) + ", JSON.stringify({ hasUI: ctx.hasUI, model: ctx.model?.id ?? null, scoped: ctx.scopedModels.map(s => s.model.id + \"@\" + s.thinkingLevel), isIdle: ctx.isIdle(), name: pi.getSessionName() ?? null, tools: pi.getActiveTools().length > 0 })); }); }\n",
		filepath.Join(cwd, "keep"): "",
	})
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir, NoExtensions: true, AdditionalExtensionPaths: []string{probe}})
	t.Cleanup(loader.Close)
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	model := fakeModel()
	result, err := CreateAgentSession(CreateAgentSessionOptions{
		CWD: cwd, AgentDir: agentDir, ResourceLoader: loader, Model: model,
		ScopedModels: []ScopedModel{{Model: model, ThinkingLevel: "high"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = result.Session.Close() })
	if err := result.Session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("the extension never received session_start: %v", err)
	}
	var got struct {
		HasUI  bool     `json:"hasUI"`
		Model  *string  `json:"model"`
		Scoped []string `json:"scoped"`
		IsIdle bool     `json:"isIdle"`
		Tools  bool     `json:"tools"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.HasUI || got.Model == nil || *got.Model != "fake-1" || !slices.Equal(got.Scoped, []string{"fake-1@high"}) || !got.IsIdle || !got.Tools {
		t.Fatalf("the extension's context = %s, want hasUI false, model fake-1, scopedModels [fake-1@high], idle, the session's tools", raw)
	}
}
