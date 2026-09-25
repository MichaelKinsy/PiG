package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

const inventoryPath = "../../interfaces/upstream-v" + coding.UpstreamVersion + ".json"

func TestGenerateIsDeterministicAndClassifiesEverySelectedMember(t *testing.T) {
	first, err := generate(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generate(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("generation is not deterministic")
	}
	var got ledger
	if err := json.Unmarshal(first, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Members) == 0 {
		t.Fatal("ledger has no members")
	}
	seenRoots := make(map[string]bool)
	for _, entry := range got.Members {
		seenRoots[entry.Root] = true
	}
	allMembers := append(append([]member(nil), got.Members...), got.RuntimeReachable...)
	customCallTiming := ""
	for _, entry := range allMembers {
		if entry.ID == "pkg:coding-agent/.#ExtensionUIContext::property:custom::call:0" {
			customCallTiming = entry.Timing
		}
		if entry.Reachability == "" || entry.Safety == "" || entry.Disposition == "" || entry.IntendedDisposition == "" || entry.Timing == "" || entry.Behavior == "" || entry.ReturnBehavior == "" || entry.Identity == "" || entry.ErrorBehavior == "" || entry.Optionality == "" || entry.ImplementationCitation == "" {
			t.Errorf("member %s is not fully classified", entry.ID)
		}
		if len(entry.Shape) == 0 || entry.ShapeHash == "" {
			t.Errorf("member %s lacks exact generated shape", entry.ID)
		}
		if len(entry.Realizations) == 0 || len(entry.PigTargets) == 0 {
			t.Errorf("member %s has no target or realization disposition", entry.ID)
		}
		for _, realization := range entry.Realizations {
			if realization.Name == "" || realization.Status == "" || realization.Evidence == "" {
				t.Errorf("member %s has incomplete realization: %+v", entry.ID, realization)
			}
		}
		if entry.Compatibility.FullNodeSource == "" || entry.Compatibility.PinnedExamples == "" || entry.Compatibility.LanguageNative == "" {
			t.Errorf("member %s lacks separated compatibility dispositions", entry.ID)
		}
	}
	var sourceInventory inventory
	data, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &sourceInventory); err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]inventoryItem, len(sourceInventory.Interfaces))
	for _, item := range sourceInventory.Interfaces {
		byID[item.ID] = item
	}
	roots, err := sourceDerivedRoots(sourceInventory.Interfaces, byID)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range roots {
		if !seenRoots[root] {
			t.Errorf("root %s has no classified members", root)
		}
	}
	if seenRoots[runtimeReachableFocusable] {
		t.Fatal("runtime-reachable Focusable leaked into the source-derived signature denominator")
	}
	if len(got.RuntimeReachable) != 2 || got.RuntimeReachable[0].Root != runtimeReachableFocusable || got.RuntimeReachable[1].Root != runtimeReachableFocusable {
		t.Fatalf("runtime-reachable surface = %+v, want Focusable root and property", got.RuntimeReachable)
	}
	for _, entry := range got.RuntimeReachable {
		if !strings.Contains(entry.Reachability, "runtime-reachable") {
			t.Errorf("runtime member %s lacks explicit reachability: %q", entry.ID, entry.Reachability)
		}
	}
	if customCallTiming != "asynchronous-promise" {
		t.Fatalf("custom call timing = %q, want asynchronous-promise", customCallTiming)
	}
	if strings.Join(got.Factory.ArgumentOrder, ",") != "tui,theme,keybindings,done" {
		t.Fatalf("factory argument order = %v", got.Factory.ArgumentOrder)
	}
	if got.Factory.Signature == "" {
		t.Fatal("factory signature is empty")
	}
	if len(got.RuntimeOnly) == 0 {
		t.Fatal("runtime-only surface is not separately classified")
	}
	if got.CompatibilityClaim == "" || !strings.Contains(got.CompatibilityClaim, "none") {
		t.Fatalf("draft made an implicit compatibility claim: %q", got.CompatibilityClaim)
	}
}

func TestReviewedMemberRejectsGenericFallback(t *testing.T) {
	if _, ok := reviewedMember("pkg:tui/.#Component", "pkg:tui/.#Component::property:invented"); ok {
		t.Fatal("unreviewed member received a generic semantic classification")
	}
}

func TestCheckedInLedgerHasNoDrift(t *testing.T) {
	generated, err := generate(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("../../interfaces", outputName)
	if err := verify(path, generated); err != nil {
		t.Fatal(err)
	}

	drifted := append([]byte(nil), generated...)
	drifted[0] = '['
	temp := filepath.Join(t.TempDir(), "ledger.json")
	if err := os.WriteFile(temp, drifted, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verify(temp, generated); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("verify drift error = %v", err)
	}
}

func TestGenerateRejectsNewReferencedExportedSupportType(t *testing.T) {
	data, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	var source inventory
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	for i := range source.Interfaces {
		if source.Interfaces[i].ID == customFactoryRoot {
			var shape map[string]any
			if err := json.Unmarshal(source.Interfaces[i].Shape, &shape); err != nil {
				t.Fatal(err)
			}
			shape["type"] = shape["type"].(string) + " & NewFactorySupport"
			source.Interfaces[i].Shape, err = json.Marshal(shape)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	source.Interfaces = append(source.Interfaces,
		inventoryItem{ID: "pkg:coding-agent/.#NewFactorySupport", Kind: "interface", ShapeHash: "mutation-root", Shape: json.RawMessage(`{"name":"NewFactorySupport","type":"NewFactorySupport"}`)},
		inventoryItem{ID: "pkg:coding-agent/.#NewFactorySupport::property:newMember", Kind: "property", ShapeHash: "mutation-member", Shape: json.RawMessage(`{"name":"newMember","optional":false,"type":"string"}`)},
	)
	mutated, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := generate(path); err == nil || !strings.Contains(err.Error(), "unclassified custom-factory member pkg:coding-agent/.#NewFactorySupport") {
		t.Fatalf("generate mutation error = %v", err)
	}
}

func TestGenerateRejectsInventoryVersionDrift(t *testing.T) {
	data, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	source["upstreamVersion"] = "0.0.0"
	data, err = json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := generate(path); err == nil || !strings.Contains(err.Error(), "coding.UpstreamVersion") {
		t.Fatalf("generate version error = %v", err)
	}
}
