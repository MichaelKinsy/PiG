package subprocess

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Pi imports an ES module extension through Node's own import, which keeps the module for the life of the process, so its /reload runs an edited ES module extension's old code (Pi 1.0.2, jiti 2.7.0). PiG evaluates an ES module extension again when a local module it imported was edited, its local imports included, and otherwise keeps the module and its state as Pi does; an installed package keeps its module either way (D93).
func TestHostReloadEvaluatesOnlyAnEditedESModuleExtensionAgain(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name, ext, packageJSON, isolation string
	}{
		{"mjs-packed", ".mjs", "", "shared-ok"},
		{"mjs-isolated", ".mjs", "", "isolated"},
		{"type-module-js-packed", ".js", `{"type":"module"}`, "shared-ok"},
		{"type-module-js-isolated", ".js", `{"type":"module"}`, "isolated"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			marker := filepath.Join(root, "counter")
			entry := filepath.Join(root, "counter"+fixture.ext)
			// lazy is imported after the factory returned, and late only from a module's second call on.
			files := map[string]string{
				"counter" + fixture.ext: fmt.Sprintf(`import { appendFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { VERSION } from "./helper%[1]s";
import { id as dependency } from "dep";
const evaluation = randomUUID();
let count = 0;
export default function (pi) {
  pi.on("tool_call", async () => {
    count++;
    const { LAZY } = await import("./lazy%[1]s");
    const { LATE } = count >= 2 ? await import("./late%[1]s") : { LATE: "-" };
    appendFileSync(%[2]q, [evaluation, count, VERSION, LAZY, LATE, dependency].join(" ") + "\n");
    return { block: false };
  });
}
`, fixture.ext, marker),
				"helper" + fixture.ext:          "export const VERSION = \"v1\";\n",
				"lazy" + fixture.ext:            "export const LAZY = \"l1\";\n",
				"late" + fixture.ext:            "export const LATE = \"t1\";\n",
				"node_modules/dep/package.json": `{"name":"dep","type":"module","exports":"./index.js"}`,
				"node_modules/dep/index.js":     "import { randomUUID } from \"node:crypto\";\nexport const id = randomUUID();\n",
			}
			if fixture.packageJSON != "" {
				files["package.json"] = fixture.packageJSON
			}
			for name, content := range files {
				path := filepath.Join(root, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// edit rewrites a source file with a later modification time, so a file system with a coarse clock still records the edit.
			edit := func(name, old, replacement string) {
				t.Helper()
				path := filepath.Join(root, name)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(strings.Replace(string(data), old, replacement, 1)), 0o600); err != nil {
					t.Fatal(err)
				}
				later := time.Now().Add(2 * time.Second)
				if err := os.Chtimes(path, later, later); err != nil {
					t.Fatal(err)
				}
			}

			config := ExtConfig{Name: "counter", Source: entry, Enabled: true, Isolation: fixture.isolation}
			host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
			host.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{config}, nil })
			t.Cleanup(func() { host.Shutdown("test") })
			current, errs := host.LoadAll(t.Context(), []ExtConfig{config})
			if len(errs) != 0 || len(current) != 1 {
				t.Fatalf("startup loaded=%v errors=%v", current, errs)
			}
			calls := 0
			// emit sends one tool_call to the current generation and returns the line the extension logged for it.
			emit := func() []string {
				t.Helper()
				calls++
				if _, err := inproc.NewRunner(current, root).EmitToolCall(t.Context(), extension.CustomToolCallEvent{
					ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: fmt.Sprint("call-", calls)},
					ToolName:          "fixture",
					Input:             map[string]any{},
				}); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(data)), "\n")
				if len(lines) != calls {
					t.Fatalf("marker after call %d = %q", calls, data)
				}
				return strings.Fields(lines[calls-1])
			}
			reload := func() {
				t.Helper()
				reloaded, err := host.Reload(t.Context())
				if err != nil || len(reloaded) != 1 {
					t.Fatalf("reload = %v, %v", reloaded, err)
				}
				current = reloaded
			}
			want := func(step string, got []string, fields ...string) {
				t.Helper()
				if strings.Join(got, " ") != strings.Join(fields, " ") {
					t.Fatalf("%s: got %q, want %q", step, got, fields)
				}
			}
			reevaluated := func(step string, got []string, previous string) string {
				t.Helper()
				if len(got) != 6 || got[0] == previous {
					t.Fatalf("%s: got %q, want a new evaluation of the module %s", step, got, previous)
				}
				return got[0]
			}

			first := emit()
			if len(first) != 6 || first[1] != "1" || first[2] != "v1" || first[3] != "l1" || first[4] != "-" {
				t.Fatalf("first call = %q", first)
			}
			moduleA, dependency := first[0], first[5]

			// The unedited reload keeps the module, although lazy had no source stamp yet.
			reload()
			want("unedited reload", emit(), moduleA, "2", "v1", "l1", "t1", dependency)

			// late was imported after the last reload, so the reload finds its edit by its modification time.
			edit("late"+fixture.ext, "t1", "t2")
			reload()
			third := emit()
			moduleB := reevaluated("edited module imported since the last reload", third, moduleA)
			want("edited module imported since the last reload", third, moduleB, "1", "v1", "l1", "-", dependency)
			want("its second call", emit(), moduleB, "2", "v1", "l1", "t2", dependency)

			reload()
			want("unedited reload after an edit", emit(), moduleB, "3", "v1", "l1", "t2", dependency)

			edit("helper"+fixture.ext, "v1", "v2")
			reload()
			sixth := emit()
			moduleC := reevaluated("edited static import", sixth, moduleB)
			want("edited static import", sixth, moduleC, "1", "v2", "l1", "-", dependency)

			edit("lazy"+fixture.ext, "l1", "l2")
			reload()
			seventh := emit()
			moduleD := reevaluated("edited dynamic import", seventh, moduleC)
			want("edited dynamic import", seventh, moduleD, "1", "v2", "l2", "-", dependency)
		})
	}
}
