//go:build !pig_strip_node_extensions

package subprocess

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

// .upstream/v0.99.2/packages/coding-agent/src/core/extensions/loader.ts:302-311 (#10054) rejects every command whose first
// argument is not a non-empty string, including the object form `pi.registerCommand({ name, handler })`, which the error
// text itself steers authors away from. Probe of real Pi 0.99.2 (`pi --no-extensions -e cmd.mjs -p /auditcmd`):
//
//	Error: Failed to load extension "<path>": Failed to load extension: Command registered by extension "<path>" must have a non-empty string name. Use pi.registerCommand("name", { description, handler }).
//
// PiG's Node runtime (runtime-node/runtime.mjs registerCommand) accepts the object form and runs the command.
func TestAuditNodeRunnerRejectsObjectFormCommandRegistration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "object-command.js")
	write(t, path, "export default function(pi) {\n pi.registerCommand({name:\"noop\", description:\"x\", handler: async()=>{}});\n}")
	host := NewHost(root)
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, failures := host.LoadAll(t.Context(), []ExtConfig{{Name: "object-command", Source: path, Enabled: true}})
	if len(loaded) != 0 || len(failures) != 1 {
		t.Fatalf("loaded=%v failures=%v", loaded, failures)
	}
	failure, ok := errors.AsType[*FactoryLoadError](failures[0])
	if !ok {
		t.Fatalf("missing factory failure: %v", failures[0])
	}
	want := "Failed to load extension: " + fmt.Sprintf(`Command registered by extension "%s" `, path) + commandNameFailure
	if failure.Message != want {
		t.Fatalf("source error=%q; want %q", failure.Message, want)
	}
}
