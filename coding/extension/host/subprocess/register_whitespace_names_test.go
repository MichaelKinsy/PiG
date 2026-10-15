package subprocess

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// loader.ts stores each registration under its exact name in a Map (extension.tools.set(tool.name, ...), commands.set(name, ...), flags.set(name, ...)):
// "a", "a ", " " and "A" are four keys (Map keys are exact, case included), and only a repeated exact name replaces an earlier registration.
func TestRegistrationNamesDifferingInWhitespaceStayDistinct(t *testing.T) {
	object := json.RawMessage(`{}`)
	reg := &RegisterPayload{
		Name:     "spaces",
		Tools:    []ToolDecl{{Name: "a", Parameters: object}, {Name: "a ", Parameters: object}, {Name: " ", Parameters: object}, {Name: "a", Description: "again", Parameters: object}, {Name: "A", Parameters: object}},
		Commands: []CommandDecl{{Name: "go"}, {Name: "go\u00a0"}, {Name: "\ufeff"}},
		Flags:    []FlagDecl{{Name: "mode", Type: "string"}, {Name: " mode", Type: "string"}},
	}
	if err := validateRegisterPayload("spaces", reg); err != nil {
		t.Fatalf("validateRegisterPayload: %v", err)
	}
	if len(reg.Tools) != 4 || reg.Tools[0].Description != "again" || reg.Tools[1].Name != "a " || reg.Tools[2].Name != " " || reg.Tools[3].Name != "A" {
		t.Fatalf("tools = %#v, want a (replaced), \"a \", \" \", A", reg.Tools)
	}
	if len(reg.Commands) != 3 {
		t.Fatalf("commands = %#v, want three distinct names", reg.Commands)
	}
	if len(reg.Flags) != 2 {
		t.Fatalf("flags = %#v, want two distinct names", reg.Flags)
	}
}

// A registerProvider call after load keys the provider by the same exact name the register payload does: a name of only whitespace is a name, the empty name is refused.
func TestLateProviderRegistrationKeysTheExactName(t *testing.T) {
	host := NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	var names []string
	host.SetProviderCallbacks(func(name string, _ extension.ProviderConfig) error {
		names = append(names, name)
		return nil
	}, nil)
	owner := withConn(&managedExt{}, NewConn("owner", nil))
	call := func(name string) error {
		args, err := json.Marshal(map[string]any{"name": name, "config": map[string]any{"baseUrl": "https://p.test/v1"}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = host.handleProviderRegistrationCall(t.Context(), owner, owner.connection(), "", &CallPayload{Method: "registerProvider", Args: args})
		return err
	}
	for _, name := range []string{" ", "p ", "p"} {
		if err := call(name); err != nil {
			t.Fatalf("registerProvider(%q): %v", name, err)
		}
	}
	if !slices.Equal(names, []string{" ", "p ", "p"}) {
		t.Fatalf("registered %q, want the three exact names", names)
	}
	if err := call(""); err == nil {
		t.Fatal("an empty provider name was accepted")
	}
}
