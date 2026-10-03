package sdk

import "testing"

// .upstream/v0.99.2/packages/coding-agent/src/core/extensions/loader.ts:302-311 (#10054): registerCommand rejects a
// command without a non-empty name or without a handler before it registers, so the extension fails to load instead
// of crashing pi when `/` lists its commands. A Go panic during registration fails the load as a thrown factory does.
func TestCommandValidationBeforeRegistration(t *testing.T) {
	handler := func(Context, string) error { return nil }
	for _, tc := range []struct {
		name    string
		command string
		options CommandOptions
		want    string
	}{
		{"empty name", "", CommandOptions{Description: "d", Handler: handler}, `Command registered by extension "commands" must have a non-empty string name. Use pi.registerCommand("name", { description, handler }).`},
		{"missing handler", "noop", CommandOptions{Description: "d"}, `Command "/noop" registered by extension "commands" must define handler().`},
		{"empty name without a handler", "", CommandOptions{}, `Command registered by extension "commands" must have a non-empty string name. Use pi.registerCommand("name", { description, handler }).`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext := New("commands")
			var caught any
			func() { defer func() { caught = recover() }(); ext.RegisterCommand(tc.command, tc.options) }()
			if caught == nil {
				t.Fatal("an invalid command did not fail synchronously")
			}
			if err, ok := caught.(error); !ok || err.Error() != tc.want {
				t.Fatalf("failure = %v; want %q", caught, tc.want)
			}
			if len(ext.commands) != 0 || len(ext.commandFuncs) != 0 || len(ext.commandCompletions) != 0 {
				t.Fatal("a failed declaration retained registration state")
			}
			ext.RegisterCommand("noop", CommandOptions{Description: "d", Handler: handler})
			if len(ext.commands) != 1 || len(ext.commandFuncs) != 1 {
				t.Fatal("a valid command did not register")
			}
		})
	}
}
