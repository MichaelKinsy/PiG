//go:build !pig_strip_node_extensions

package subprocess

import (
	"path/filepath"
	"testing"
)

// Pi's registerCommand (packages/coding-agent/src/core/extensions/loader.ts:303-318) spreads the whole options object into
// the RegisteredCommand and reads only description, getArgumentCompletions and handler, so an option outside that set
// (here `args`, which the Node runtime used to forward as the deleted CommandDecl.Args) is ignored and the command
// loads. The host decodes the register payload strictly, so the runtime must declare only the members the host knows.
func TestNodeCommandWithAnUnknownOptionLoadsLikePi(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "extra-option.js")
	write(t, path, "export default function(pi) {\n pi.registerCommand(\"withargs\", {description:\"d\", args:\"<name>\", handler: async()=>{}});\n}")
	host := NewHost(root)
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, failures := host.LoadAll(t.Context(), []ExtConfig{{Name: "extra-option", Source: path, Enabled: true}})
	if len(failures) != 0 || len(loaded) != 1 {
		t.Fatalf("loaded=%v failures=%v", loaded, failures)
	}
	command, ok := loaded[0].Commands["withargs"]
	if len(loaded[0].Commands) != 1 || !ok || command.Description != "d" {
		t.Fatalf("commands=%+v", loaded[0].Commands)
	}
}
