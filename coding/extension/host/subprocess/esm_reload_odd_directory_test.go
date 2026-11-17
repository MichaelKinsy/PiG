//go:build !windows

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

// D93: an absolute path used as an import specifier is read as a URL path, as Node reads it (runtime-node/register-hooks.mjs nodeImportPath), so jiti's import of an extension whose directory name holds a percent escape or a tab still loads, and after an edit of its static import /reload loads the edited module (the path is read as a URL path, so Node and Bun find no file by it and jiti evaluates the module itself; an unedited reload keeps no promise here).
func TestHostReloadEvaluatesAnEditedESModuleInADirectoryWithAnEscapeOrATab(t *testing.T) {
	t.Parallel()
	for name, dir := range map[string]string{"percent escape": "ext%41dir", "percent twenty": "ext%20dir", "tab": "ext\tdir"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(t.TempDir(), dir)
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Skipf("the file system takes no directory named %q: %v", dir, err)
			}
			marker := filepath.Join(t.TempDir(), "calls")
			entry := filepath.Join(root, "counter.mjs")
			files := map[string]string{
				entry: fmt.Sprintf(`import { appendFileSync } from "node:fs";
import { randomUUID } from "node:crypto";
import { VERSION } from "./helper.mjs";
const evaluation = randomUUID();
export default function (pi) {
  pi.on("tool_call", async () => {
    appendFileSync(%q, [evaluation, VERSION].join(" ") + "\n");
    return { block: false };
  });
}
`, marker),
				filepath.Join(root, "helper.mjs"): "export const VERSION = \"v1\";\n",
			}
			for path, content := range files {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			config := ExtConfig{Name: "counter", Source: entry, Enabled: true, Isolation: "isolated"}
			host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
			host.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{config}, nil })
			t.Cleanup(func() { host.Shutdown("test") })
			current, errs := host.LoadAll(t.Context(), []ExtConfig{config})
			if len(errs) != 0 || len(current) != 1 {
				t.Fatalf("startup loaded=%v errors=%v", current, errs)
			}
			calls := 0
			emit := func() []string {
				t.Helper()
				calls++
				if _, err := inproc.NewRunner(current, root).EmitToolCall(t.Context(), extension.CustomToolCallEvent{Type: "tool_call", ToolCallID: fmt.Sprint("call-", calls), ToolName: "fixture", Input: map[string]any{}}); err != nil {
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
			first := emit()
			if len(first) != 2 || first[1] != "v1" {
				t.Fatalf("first call = %q", first)
			}
			helper := filepath.Join(root, "helper.mjs")
			if err := os.WriteFile(helper, []byte("export const VERSION = \"v2\";\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			later := time.Now().Add(2 * time.Second)
			if err := os.Chtimes(helper, later, later); err != nil {
				t.Fatal(err)
			}
			reload()
			if got := emit(); len(got) != 2 || got[1] != "v2" {
				t.Fatalf("reload after an edit = %q, want the edited helper (v2)", got)
			}
		})
	}
}
