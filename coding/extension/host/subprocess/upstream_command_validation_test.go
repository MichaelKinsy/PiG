package subprocess

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

const commandNameFailure = `must have a non-empty string name. Use pi.registerCommand("name", { description, handler }).`

// .upstream/v0.99.2/packages/coding-agent/src/core/extensions/loader.ts:302-311 (#10054): an extension command registered
// without a non-empty string name or without a handler function fails the extension load with an error, instead of
// crashing pi when `/` lists its commands.
func TestNodeRunnerRejectsInvalidCommandRegistration(t *testing.T) {
	for _, tc := range []struct {
		name, call, want string
	}{
		{"empty name", `pi.registerCommand("", {description:"x", handler: async()=>{}})`, `Command registered by extension "%s" ` + commandNameFailure},
		{"undefined name", `pi.registerCommand(undefined, {description:"x", handler: async()=>{}})`, `Command registered by extension "%s" ` + commandNameFailure},
		{"non-string name", `pi.registerCommand(7, {description:"x", handler: async()=>{}})`, `Command registered by extension "%s" ` + commandNameFailure},
		{"missing handler", `pi.registerCommand("noop", {description:"x"})`, `Command "/noop" registered by extension "%s" must define handler().`},
		{"missing options", `pi.registerCommand("noop")`, `Command "/noop" registered by extension "%s" must define handler().`},
		{"non-function handler", `pi.registerCommand("noop", {description:"x", handler:"run"})`, `Command "/noop" registered by extension "%s" must define handler().`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "bad-command.js")
			write(t, path, "export default function(pi) {\n "+tc.call+";\n}")
			host := NewHost(root)
			t.Cleanup(func() { host.Shutdown("test done") })
			loaded, failures := host.LoadAll(t.Context(), []ExtConfig{{Name: "bad-command", Source: path, Enabled: true}})
			if len(loaded) != 0 || len(failures) != 1 {
				t.Fatalf("loaded=%v failures=%v", loaded, failures)
			}
			outer, ok := errors.AsType[*ExtensionLoadError](failures[0])
			if !ok || outer.Path != path {
				t.Fatalf("load error=%+v", failures[0])
			}
			failure, ok := errors.AsType[*FactoryLoadError](failures[0])
			if !ok {
				t.Fatalf("missing factory failure: %v", failures[0])
			}
			want := "Failed to load extension: " + fmt.Sprintf(tc.want, path)
			if failure.Message != want || outer.Err.Error() != want {
				t.Fatalf("source error=%q (reported as %q); want %q", failure.Message, outer.Err, want)
			}
		})
	}
}

// A valid command still loads beside the rejected forms.
func TestNodeRunnerLoadsValidCommandRegistration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "good-command.js")
	write(t, path, `export default function(pi) {
 pi.registerCommand("noop", {description:"x", handler: async()=>{}});
}`)
	host := NewHost(root)
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, failures := host.LoadAll(t.Context(), []ExtConfig{{Name: "good-command", Source: path, Enabled: true}})
	if len(loaded) != 1 || len(failures) != 0 {
		t.Fatalf("loaded=%v failures=%v", loaded, failures)
	}
}

// loader.ts:302-311 validates each registration before a later one replaces it, so the Host rejects an empty command name that a
// later valid command would otherwise hide. Every SDK sends the same register payload, so this covers Go, Rust, Python and packed cells.
func TestCommandNamesValidatedBeforeDeduplication(t *testing.T) {
	reg := &RegisterPayload{Name: "commands", Commands: []CommandDecl{{Name: "", Description: "d"}, {Name: "noop", Description: "d"}}}
	want := `Command registered by extension "commands" ` + commandNameFailure
	if err := validateRegisterPayload("commands", reg); err == nil || err.Error() != want {
		t.Fatalf("error = %v; want %q", err, want)
	}
	reg = &RegisterPayload{Name: "commands", Commands: []CommandDecl{{Name: "noop", Description: "d"}}}
	if err := validateRegisterPayload("commands", reg); err != nil {
		t.Fatal(err)
	}
}
