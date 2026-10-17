package tui

import (
	"reflect"
	"testing"
)

// The model selector and the config selector order names with String.prototype.localeCompare (model-selector.ts sortModels, config-selector.ts buildGroups), not by code unit. Node sorts ["b","B","a","A","_x","é","e"] with localeCompare as below.
var localeOrder = []string{"_x", "a", "A", "b", "B", "e", "é"}

var codeUnitShuffled = []string{"b", "é", "B", "e", "_x", "A", "a"}

func TestModelSelectorSortsProvidersWithLocaleCompare(t *testing.T) {
	items := make([]ModelSelectorItem, len(codeUnitShuffled))
	for i, provider := range codeUnitShuffled {
		items[i] = ModelSelectorItem{Provider: provider, ID: "m"}
	}
	var got []string
	for _, item := range sortModelItems(items, "", "") {
		got = append(got, item.Provider)
	}
	if !reflect.DeepEqual(got, localeOrder) {
		t.Fatalf("provider order = %q, want %q", got, localeOrder)
	}
}

func TestConfigSelectorSortsSourcesAndItemsWithLocaleCompare(t *testing.T) {
	var groups []*ResourceGroup
	subgroup := &ResourceSubgroup{Type: ResourceSkills}
	for _, name := range codeUnitShuffled {
		groups = append(groups, &ResourceGroup{Origin: "top-level", Scope: "user", Source: name})
		subgroup.Items = append(subgroup.Items, &ResourceItem{DisplayName: name})
	}
	groups[0].Subgroups = []*ResourceSubgroup{subgroup}
	sortGroups(groups)
	var sources, names []string
	for _, group := range groups {
		sources = append(sources, group.Source)
	}
	for _, item := range subgroup.Items {
		names = append(names, item.DisplayName)
	}
	if !reflect.DeepEqual(sources, localeOrder) {
		t.Errorf("group source order = %q, want %q", sources, localeOrder)
	}
	if !reflect.DeepEqual(names, localeOrder) {
		t.Errorf("item order = %q, want %q", names, localeOrder)
	}
}
