//go:build !pig_strip_changelog && !pig_strip_codemode && !pig_strip_mcp && !pig_strip_pig_login && !pig_strip_tool_search

// The generator reads the IDs from the registrations compiled into its own binary, so these tests compare the committed table with what
// a stock build registers: a build that compiles out a tool, a command or a built-in extension has fewer registrations and cannot.
// The tags above are the ones that remove a registration; make piglet-strip-tests fails when another tag does, and joins this list.

package main

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// TestCommittedStripIDsMatchRegistrations fails when a tool, slash command,
// built-in extension or feature is added, removed or renamed without
// regenerating the strip ID table.
func TestCommittedStripIDsMatchRegistrations(t *testing.T) {
	t.Setenv(parityHarnessEnv, "")
	rendered, err := render(collect())
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile("../../../../internal/pigstrip/ids_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committed, rendered) {
		t.Fatal("internal/pigstrip/ids_generated.go is stale; run `make piglet-strip-ids`")
	}
}

// TestCommittedSchemaStripEnumsMatchRegistrations fails when the strip ID enums
// of the Piglet JSON Schema differ from the registrations.
func TestCommittedSchemaStripEnumsMatchRegistrations(t *testing.T) {
	t.Setenv(parityHarnessEnv, "")
	committed, err := os.ReadFile("../../piglet.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := renderSchema(committed, collect())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(committed, rendered) {
		t.Fatal("coding/piglet/piglet.schema.json strip ID enums are stale; run `make piglet-strip-ids`")
	}
}

// TestCollectReadsEveryRegistrationKind pins that each list comes from its
// registration: a known member of each registry appears in its list.
func TestCollectReadsEveryRegistrationKind(t *testing.T) {
	t.Setenv(parityHarnessEnv, "")
	got := collect()
	for _, want := range []struct {
		list []string
		id   string
	}{
		{got.tools, "grep"},
		{got.commands, "/share"},
		{got.commands, "/piglet"},
		{got.extensions, "mcp"},
		{got.extensions, "llama.cpp"},
		{got.apis, "bedrock-converse-stream"},
		{got.features, "node-extensions"},
		{got.features, pigstrip.Skills},
	} {
		if !slices.Contains(want.list, want.id) {
			t.Errorf("collected table lacks %q: %v", want.id, want.list)
		}
	}
	for _, command := range got.commands {
		if command == "/probe-select-list" {
			t.Fatalf("parity probe command %q leaked into the shipped table", command)
		}
	}
}
