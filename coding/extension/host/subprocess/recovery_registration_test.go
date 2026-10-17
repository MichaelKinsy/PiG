package subprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRecoveredExtensionTakesItsNewToolRegistration kills the process behind a Node extension and checks that, once the host recovers it, the extension's tool is the one the new process registered. Packed and isolated recovery must agree (D20): each keeps the Extension the runner holds and replaces its handlers and tools with the new registration (adoptRestartedExtension).
func TestRecoveredExtensionTakesItsNewToolRegistration(t *testing.T) {
	skipWithoutNodeExtensions(t)
	nodeCellRequireNode(t)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			root := t.TempDir()
			counter := filepath.Join(root, "generation")
			var configs []ExtConfig
			for _, name := range []string{"rr-gen", "rr-peer"} {
				entry := filepath.Join(root, name+".mjs")
				src := fmt.Sprintf(`import fs from "node:fs";
let generation = 1;
if (%[1]q === "rr-gen") {
  try { generation = Number(fs.readFileSync(%[2]q, "utf8")) + 1; } catch {}
  fs.writeFileSync(%[2]q, String(generation));
}
export default function (pi) {
  pi.registerTool({
    name: %[1]q,
    label: %[1]q,
    description: "generation " + generation,
    parameters: {type: "object", properties: {}},
    async execute() { return {content: [{type: "text", text: "ok"}]}; },
  });
}
`, name, counter)
				if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
					t.Fatal(err)
				}
				configs = append(configs, ExtConfig{Name: name, Source: entry, Enabled: true, Isolation: isolation})
			}
			h := NewHost(t.TempDir())
			h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
			t.Cleanup(func() { h.Shutdown("test done") })
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			if _, err := h.Reload(ctx); err != nil {
				t.Fatalf("reload: %v", err)
			}
			description := func() string {
				for _, ext := range h.Extensions() {
					if ext.Name != "rr-gen" {
						continue
					}
					for _, tool := range ext.RegisteredTools() {
						if tool.Definition.Name == "rr-gen" {
							return tool.Definition.Description
						}
					}
				}
				return ""
			}
			if got := description(); got != "generation 1" {
				t.Fatalf("loaded tool description = %q, want generation 1", got)
			}
			pids := nodeCellProcessesForMarker(t, root)
			if len(pids) == 0 {
				t.Fatal("no Node process hosts rr-gen")
			}
			for _, pid := range pids {
				if err := killTestProcess(pid); err != nil {
					t.Fatalf("kill %d: %v", pid, err)
				}
			}
			deadline := time.Now().Add(30 * time.Second)
			for description() != "generation 2" && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			if got := description(); got != "generation 2" {
				written, _ := os.ReadFile(counter)
				t.Fatalf("recovered tool description = %q, want the new process's generation 2 (the newest process wrote generation %s)", got, written)
			}
		})
	}
}
