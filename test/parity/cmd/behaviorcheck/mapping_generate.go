package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/BurntSushi/toml"
)

type ownerOverrideDocument struct {
	UpstreamVersion string          `json:"upstreamVersion"`
	Owners          []ownerOverride `json:"owners"`
}

type ownerOverride struct {
	ID          string `json:"id"`
	OwnerFamily string `json:"ownerFamily"`
}

// unchangedSurfaces names the handlers and renderers whose inventory record is identical in both inventories and whose keybindings are too, so a reviewed mapping entry for them still describes the same source. Any difference, including a moved line, leaves the surface pending.
func unchangedSurfaces(before, after inputInventory) map[string]bool {
	record := func(value any) string {
		data, _ := json.Marshal(value)
		return string(data)
	}
	oldRecords := make(map[string]string, len(before.Handlers)+len(before.Renderers))
	for _, handler := range before.Handlers {
		oldRecords[handler.ID] = record(handler)
	}
	for _, item := range before.Renderers {
		oldRecords[item.ID] = record(item)
	}
	oldBindings := make(map[string]string, len(before.Keybindings))
	for _, binding := range before.Keybindings {
		oldBindings[binding.ID] = record(binding)
	}
	changedBindings := make(map[string]bool)
	for _, binding := range after.Keybindings {
		if oldBindings[binding.ID] != record(binding) {
			changedBindings[binding.ID] = true
		}
	}
	unchanged := make(map[string]bool)
	for _, handler := range after.Handlers {
		if oldRecords[handler.ID] == record(handler) && !slices.ContainsFunc(handler.Bindings, func(id string) bool { return changedBindings[id] }) {
			unchanged[handler.ID] = true
		}
	}
	for _, item := range after.Renderers {
		if oldRecords[item.ID] == record(item) {
			unchanged[item.ID] = true
		}
	}
	return unchanged
}

// generatePendingInputMapping returns a mapping with every surface pending, except that a surface listed in carried keeps the reviewed entry of previous.
func generatePendingInputMapping(inventory inputInventory, previous inputMapping, carried map[string]bool, families familiesConfig, overrides ownerOverrideDocument) (inputMapping, error) {
	if inventory.UpstreamVersion == "" {
		return inputMapping{}, fmt.Errorf("behavior input inventory has no upstream version")
	}
	if overrides.UpstreamVersion != inventory.UpstreamVersion {
		return inputMapping{}, fmt.Errorf("owner override version = %q, want %q", overrides.UpstreamVersion, inventory.UpstreamVersion)
	}
	previousEntries := make(map[string]inputMappingEntry, len(previous.Mappings)+len(previous.RenderMappings))
	previousOwners := make(map[string]string, len(previous.Mappings)+len(previous.RenderMappings))
	for _, entry := range append(append([]inputMappingEntry{}, previous.Mappings...), previous.RenderMappings...) {
		if entry.ID == "" || entry.OwnerFamily == "" {
			return inputMapping{}, fmt.Errorf("previous mapping contains an empty id or owner family")
		}
		if _, duplicate := previousOwners[entry.ID]; duplicate {
			return inputMapping{}, fmt.Errorf("previous mapping contains duplicate id %q", entry.ID)
		}
		previousOwners[entry.ID] = entry.OwnerFamily
		previousEntries[entry.ID] = entry
	}
	reviewedOwners := make(map[string]string, len(overrides.Owners))
	for _, entry := range overrides.Owners {
		if entry.ID == "" || entry.OwnerFamily == "" {
			return inputMapping{}, fmt.Errorf("owner override contains an empty id or owner family")
		}
		if _, duplicate := reviewedOwners[entry.ID]; duplicate {
			return inputMapping{}, fmt.Errorf("owner override contains duplicate id %q", entry.ID)
		}
		reviewedOwners[entry.ID] = entry.OwnerFamily
	}
	seen := make(map[string]bool, len(inventory.Handlers)+len(inventory.Renderers))
	makeEntry := func(id, path string) (inputMappingEntry, error) {
		if id == "" || seen[id] {
			return inputMappingEntry{}, fmt.Errorf("behavior inventory contains empty or duplicate id %q", id)
		}
		seen[id] = true
		owner := previousOwners[id]
		if owner != "" {
			if reviewedOwners[id] != "" {
				return inputMappingEntry{}, fmt.Errorf("owner override for stable surface %q is unnecessary", id)
			}
			family, exists := families.Families[owner]
			if !exists || !matchesFamily(path, family.Includes) {
				return inputMappingEntry{}, fmt.Errorf("previous owner family %q no longer owns %s", owner, path)
			}
		} else {
			var candidates []string
			for name, family := range families.Families {
				if matchesFamily(path, family.Includes) {
					candidates = append(candidates, name)
				}
			}
			slices.Sort(candidates)
			switch len(candidates) {
			case 0:
				return inputMappingEntry{}, fmt.Errorf("surface %q path %q has no owner family", id, path)
			case 1:
				owner = candidates[0]
				if reviewedOwners[id] != "" {
					return inputMappingEntry{}, fmt.Errorf("owner override for unambiguous surface %q is unnecessary", id)
				}
			default:
				owner = reviewedOwners[id]
				if owner == "" || !slices.Contains(candidates, owner) {
					return inputMappingEntry{}, fmt.Errorf("surface %q has ambiguous owner families %v and no valid reviewed override", id, candidates)
				}
			}
		}
		delete(reviewedOwners, id)
		if kept, ok := previousEntries[id]; ok && carried[id] {
			return kept, nil
		}
		return inputMappingEntry{
			ID: id, OwnerFamily: owner, Disposition: "pending",
			PigTargets: []string{}, Evidence: []string{}, Contracts: []string{},
		}, nil
	}
	output := inputMapping{UpstreamVersion: inventory.UpstreamVersion}
	for _, handler := range inventory.Handlers {
		entry, err := makeEntry(handler.ID, handler.Path)
		if err != nil {
			return inputMapping{}, err
		}
		output.Mappings = append(output.Mappings, entry)
	}
	for _, renderer := range inventory.Renderers {
		entry, err := makeEntry(renderer.ID, renderer.Path)
		if err != nil {
			return inputMapping{}, err
		}
		output.RenderMappings = append(output.RenderMappings, entry)
	}
	if len(reviewedOwners) > 0 {
		ids := make([]string, 0, len(reviewedOwners))
		for id := range reviewedOwners {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		return inputMapping{}, fmt.Errorf("owner override references unknown surface %q", ids[0])
	}
	return output, nil
}

func generatePendingInputMappingFile(inventoryPath, previousPath, previousInventoryPath, familiesPath, overridesPath, outputPath string) error {
	if inventoryPath == "" || previousPath == "" || familiesPath == "" || overridesPath == "" || outputPath == "" {
		return fmt.Errorf("mapping generation requires inventory, previous mapping, families, owner overrides, and output paths")
	}
	var inventory inputInventory
	if err := decodeJSONFile(inventoryPath, &inventory); err != nil {
		return err
	}
	var previous inputMapping
	if err := decodeJSONFile(previousPath, &previous); err != nil {
		return err
	}
	var families familiesConfig
	if _, err := toml.DecodeFile(familiesPath, &families); err != nil {
		return err
	}
	var overrides ownerOverrideDocument
	if err := decodeJSONFile(overridesPath, &overrides); err != nil {
		return err
	}
	var carried map[string]bool
	if previousInventoryPath != "" {
		var earlier inputInventory
		if err := decodeJSONFile(previousInventoryPath, &earlier); err != nil {
			return err
		}
		carried = unchangedSurfaces(earlier, inventory)
	}
	mapping, err := generatePendingInputMapping(inventory, previous, carried, families, overrides)
	if err != nil {
		return err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(mapping); err != nil {
		return err
	}
	data := buffer.Bytes()
	directory := filepath.Dir(outputPath)
	stage, err := os.CreateTemp(directory, ".behavior-input-mapping-*.stage")
	if err != nil {
		return err
	}
	stagePath := stage.Name()
	defer func() { _ = os.Remove(stagePath) }()
	if _, err := stage.Write(data); err != nil {
		_ = stage.Close()
		return err
	}
	if err := stage.Chmod(0o644); err != nil {
		_ = stage.Close()
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	return os.Rename(stagePath, outputPath)
}
