package services

// pi: packages/coding-agent/src/experimental/services/slash-commands-provider.ts

import (
	"reflect"
	"testing"
)

// upstream: slash-commands-provider.ts:#validate. Names start with a lower-case letter or digit and continue with lower-case letters, digits, ':' or '-'.
func TestSlashCommandRegistryNameValidation(t *testing.T) {
	for name, valid := range map[string]bool{
		"a": true, "0": true, "a-b": true, "a:b": true, "a0:-": true,
		"": false, "A": false, "a_b": false, "-a": false, ":a": false, "a b": false, "a/b": false, "é": false, "a\n": false,
	} {
		registry := NewSlashCommandRegistry()
		_, registerErr := registry.Register(SlashCommandContribution{Name: name})
		_, replaceErr := registry.Replace(SlashCommandContribution{Name: name})
		if (registerErr == nil) != valid || (replaceErr == nil) != valid {
			t.Errorf("%q: register=%v replace=%v, want valid=%v", name, registerErr, replaceErr, valid)
		}
		if !valid && registerErr != nil && registerErr.Error() != "Invalid slash command name: "+name {
			t.Errorf("%q: error = %v", name, registerErr)
		}
	}
}

// upstream: slash-commands-provider.ts:#add. Only the visible registration's retirement publishes: staging a replacement and retiring a staged replacement leave the listener's snapshot untouched.
func TestSlashCommandRegistryPublishesOnlyVisibleChanges(t *testing.T) {
	registry := NewSlashCommandRegistry()
	var snapshots [][]string
	stop := registry.Subscribe(func(commands []SlashCommandContribution) { snapshots = append(snapshots, slashCommandNames(commands)) })
	defer stop()
	closeFirst, err := registry.Register(SlashCommandContribution{Name: "hello"})
	requireModelsOK(t, err)
	closeStaged, err := registry.Replace(SlashCommandContribution{Name: "hello"})
	requireModelsOK(t, err)
	closeStaged()
	closeStaged()
	if want := [][]string{{}, {"hello"}}; !reflect.DeepEqual(snapshots, want) {
		t.Fatalf("after staging and retiring a replacement: %v, want %v", snapshots, want)
	}
	closeFirst()
	if want := [][]string{{}, {"hello"}, {}}; !reflect.DeepEqual(snapshots, want) {
		t.Fatalf("after retiring the visible command: %v, want %v", snapshots, want)
	}
}

// upstream: slash-commands-provider.ts:107-121. Facet identities are the published ids; an omitted registry selects a fresh one.
func TestSlashCommandFacetIdentities(t *testing.T) {
	if got := CreateSlashCommandsRuntimeFacet(nil).Id; got != "@pi/slash-commands-runtime" {
		t.Fatalf("runtime facet id = %q", got)
	}
	if got := CreateBuiltInSlashCommandsFacet(BuiltInSlashCommandsOptions{}).Id; got != "@pi/slash-commands-builtin" {
		t.Fatalf("built-in facet id = %q", got)
	}
}
