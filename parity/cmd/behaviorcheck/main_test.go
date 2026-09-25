package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

func TestBehaviorContractsCurrentLedgerPasses(t *testing.T) {
	inventory := fmt.Sprintf("../../interfaces/behavior-inputs-v%s.json", coding.UpstreamVersion)
	mapping := fmt.Sprintf("../../interfaces/behavior-input-mapping-v%s.json", coding.UpstreamVersion)
	if err := check("../../behavior-contracts.toml", "../../../.upstream/current", inventory, mapping, "../../families.toml", "", false); err != nil {
		t.Fatal(err)
	}
}

func TestBehaviorContractsRejectSourceAndKeybindingDrift(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "component.ts")
	segment := "handle() { kb.matches(key, \"tui.select.up\"); }\n"
	if err := os.WriteFile(path, []byte(segment+"END\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(segment)))
	ledgerPath := filepath.Join(t.TempDir(), "contracts.toml")
	inventoryPath := filepath.Join(t.TempDir(), "inventory.json")
	mappingPath := filepath.Join(t.TempDir(), "mapping.json")
	familiesPath := filepath.Join(t.TempDir(), "families.toml")
	if err := os.WriteFile(inventoryPath, fmt.Appendf(nil, `{"upstreamVersion":%q,"keybindings":[{"id":"tui.select.up","defaults":{"darwin":["up"],"linux":["up"],"linuxWsl":["up"],"win32":["up"]},"description":"Move selection up","path":"component.ts","line":1,"consumers":["fixture-handler"]}],"handlers":[{"id":"fixture-handler","path":"component.ts","owner":"Fixture","method":"handleInput","startLine":1,"endLine":1,"sourceHash":"sha256:0000000000000000000000000000000000000000000000000000000000000000","async":false,"branchCount":1,"bindings":["tui.select.up"],"rawInputs":[],"callbacks":[],"delegates":[],"mutations":[],"boundaryOperators":[]}]}`, coding.UpstreamVersion), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mappingPath, fmt.Appendf(nil, `{"upstreamVersion":%q,"mappings":[{"id":"fixture-handler","ownerFamily":"selectors","disposition":"pending","pigTargets":[],"evidence":[],"contracts":["fixture"]}]}`, coding.UpstreamVersion), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(familiesPath, []byte("[families.selectors]\nincludes = [\"component.ts\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeLedger := func(sourceHash string, keys []string) {
		t.Helper()
		quoted := make([]string, len(keys))
		for i, key := range keys {
			quoted[i] = fmt.Sprintf("%q", key)
		}
		text := fmt.Sprintf(`upstream_version = %q
+[[contract]]
+id = "fixture"
+upstream_id = "fixture-handler"
+family = "selectors"
+kind = "boundary-transition"
+claim = "wrap"
+status = "pending"
+[contract.upstream]
+path = "component.ts"
+start = "handle() {"
+end = "END"
+sha256 = %q
+keybindings = [%s]
+`, coding.UpstreamVersion, sourceHash, strings.Join(quoted, ","))
		text = strings.ReplaceAll(text, "\n+", "\n")
		if err := os.WriteFile(ledgerPath, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeLedger(hash, []string{"tui.select.up"})
	if err := check(ledgerPath, root, inventoryPath, mappingPath, familiesPath, "", false); err != nil {
		t.Fatalf("valid fixture failed: %v", err)
	}
	validLedger, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath, append([]byte("schema_"+"version = 1\n"), validLedger...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := check(ledgerPath, root, inventoryPath, mappingPath, familiesPath, "", false); err == nil || !strings.Contains(err.Error(), "unknown fields") {
		t.Fatalf("former schema_version field error = %v", err)
	}
	writeLedger(hash, []string{"tui.select.up"})
	writeLedger(strings.Repeat("0", 64), []string{"tui.select.up"})
	if err := check(ledgerPath, root, inventoryPath, mappingPath, familiesPath, "", false); err == nil || !strings.Contains(err.Error(), "source drift") {
		t.Fatalf("source drift error = %v", err)
	}
	writeLedger(hash, []string{"tui.select.down"})
	if err := check(ledgerPath, root, inventoryPath, mappingPath, familiesPath, "", false); err == nil || !strings.Contains(err.Error(), "keybindings") {
		t.Fatalf("keybinding drift error = %v", err)
	}
}

func TestGeneratePendingInputMappingCarriesStableOwnersAndResolvesNewScope(t *testing.T) {
	inventory := inputInventory{
		UpstreamVersion: "0.86.1",
		Handlers: []inputHandler{
			{ID: "input:stable", Path: "packages/stable.ts"},
			{ID: "input:new", Path: "packages/new.ts"},
			{ID: "input:ambiguous", Path: "packages/shared.ts"},
		},
		Renderers: []renderer{{ID: "render:new", Path: "packages/render.ts"}},
	}
	previous := inputMapping{UpstreamVersion: "0.84.0", Mappings: []inputMappingEntry{{ID: "input:stable", OwnerFamily: "stable", Disposition: "ported"}}}
	families := familiesConfig{Families: map[string]familyDefinition{
		"stable": {Includes: []string{"packages/stable.ts"}},
		"new":    {Includes: []string{"packages/new.ts", "packages/shared.ts"}},
		"other":  {Includes: []string{"packages/shared.ts"}},
		"render": {Includes: []string{"packages/render.ts"}},
	}}
	overrides := ownerOverrideDocument{UpstreamVersion: "0.86.1", Owners: []ownerOverride{{ID: "input:ambiguous", OwnerFamily: "new"}}}

	got, err := generatePendingInputMapping(inventory, previous, families, overrides)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpstreamVersion != "0.86.1" || len(got.Mappings) != 3 || len(got.RenderMappings) != 1 {
		t.Fatalf("mapping shape = %#v", got)
	}
	wantOwners := []string{"stable", "new", "new"}
	for index, entry := range got.Mappings {
		if entry.OwnerFamily != wantOwners[index] || entry.Disposition != "pending" || len(entry.PigTargets) != 0 || len(entry.Evidence) != 0 || len(entry.Contracts) != 0 || entry.Divergence != "" || entry.Rationale != "" {
			t.Fatalf("mapping[%d] = %#v, want pending owner %q", index, entry, wantOwners[index])
		}
	}
	if got.RenderMappings[0].OwnerFamily != "render" || got.RenderMappings[0].Disposition != "pending" {
		t.Fatalf("render mapping = %#v", got.RenderMappings[0])
	}
}

func TestGeneratePendingInputMappingRejectsUnresolvedAndStaleOwners(t *testing.T) {
	inventory := inputInventory{UpstreamVersion: "0.86.1", Handlers: []inputHandler{{ID: "input:missing", Path: "packages/missing.ts"}}}
	families := familiesConfig{Families: map[string]familyDefinition{"other": {Includes: []string{"packages/other.ts"}}}}
	if _, err := generatePendingInputMapping(inventory, inputMapping{}, families, ownerOverrideDocument{UpstreamVersion: "0.86.1"}); err == nil || !strings.Contains(err.Error(), "has no owner family") {
		t.Fatalf("unresolved owner error = %v", err)
	}

	inventory.Handlers[0].Path = "packages/other.ts"
	overrides := ownerOverrideDocument{UpstreamVersion: "0.86.1", Owners: []ownerOverride{{ID: "input:stale", OwnerFamily: "other"}}}
	if _, err := generatePendingInputMapping(inventory, inputMapping{}, families, overrides); err == nil || !strings.Contains(err.Error(), "unknown surface") {
		t.Fatalf("stale override error = %v", err)
	}
}

func TestInputMappingsRejectMissingAndIncompleteStrictClosure(t *testing.T) {
	handlers := map[string]inputHandler{"input:fixture#Picker.handleInput": {ID: "input:fixture#Picker.handleInput", Path: "fixture"}}
	mapping := inputMapping{Mappings: []inputMappingEntry{{ID: "input:fixture#Picker.handleInput", OwnerFamily: "selectors", Disposition: "partial"}}}
	families := familiesConfig{Families: map[string]familyDefinition{"selectors": {Includes: []string{"fixture"}}}}
	if _, err := checkInputMappings(mapping, handlers, nil, families, t.TempDir(), true); err == nil || !strings.Contains(err.Error(), "remains partial") {
		t.Fatalf("strict partial error = %v", err)
	}
	mapping.Mappings = nil
	if _, err := checkInputMappings(mapping, handlers, nil, families, t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "has no reviewed mapping") {
		t.Fatalf("missing mapping error = %v", err)
	}
}

func TestRenderMappingsRejectMissingAndIncompleteClosure(t *testing.T) {
	renderers := map[string]renderer{"render:fixture#Picker.render": {ID: "render:fixture#Picker.render", Path: "fixture"}}
	families := familiesConfig{Families: map[string]familyDefinition{"selectors": {Includes: []string{"fixture"}}}}
	if _, err := checkRenderMappings(nil, renderers, nil, families, t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "has no reviewed mapping") {
		t.Fatalf("missing render mapping error = %v", err)
	}
	mappings := []inputMappingEntry{{ID: "render:fixture#Picker.render", OwnerFamily: "selectors", Disposition: "partial"}}
	if _, err := checkRenderMappings(mappings, renderers, nil, families, t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "needs Pig targets, evidence, and contracts") {
		t.Fatalf("partial render mapping error = %v", err)
	}
}
