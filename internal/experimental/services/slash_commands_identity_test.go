package services

import (
	"context"
	"testing"
)

// upstream: packages/coding-agent/src/experimental/services/slash-commands-provider.ts:50-60 creates a fresh frozen command copy for every registration and retains it across list snapshots.
func TestSlashCommandRegistrationAndCallbackOriginIdentity(t *testing.T) {
	t.Parallel()
	registry := NewSlashCommandRegistry()
	command := SlashCommandContribution{Name: "same", Description: new("same"), Run: func(context.Context, string) (SlashCommandRunResult, error) { return nil, nil }}
	command, err := command.WithOrigin(SlashCommandOrigin{Owner: "node-owner", Handle: "callback-1"})
	requireModelsOK(t, err)
	closeFirst, err := registry.Register(command)
	requireModelsOK(t, err)
	first := registry.List()[0].RegistrationIdentity()
	if first == nil || registry.List()[0].RegistrationIdentity() != first {
		t.Fatal("list snapshots lost registration identity")
	}
	origin, present := registry.List()[0].Origin()
	checkModelsEqual(t, present, true)
	checkModelsEqual(t, origin, SlashCommandOrigin{Owner: "node-owner", Handle: "callback-1"})
	closeSecond, err := registry.Replace(command)
	requireModelsOK(t, err)
	if registry.List()[0].RegistrationIdentity() != first {
		t.Fatal("staged replacement replaced visible identity early")
	}
	closeFirst()
	second := registry.List()[0].RegistrationIdentity()
	if second == nil || second == first {
		t.Fatal("same-name/equal-metadata replacement reused registration identity")
	}
	origin, present = registry.List()[0].Origin()
	checkModelsEqual(t, present, true)
	checkModelsEqual(t, origin, SlashCommandOrigin{Owner: "node-owner", Handle: "callback-1"})
	closeSecond()
	if len(registry.List()) != 0 {
		t.Fatal("registration remained after close")
	}
	if _, err := command.WithOrigin(SlashCommandOrigin{Owner: "node-owner"}); err == nil {
		t.Fatal("accepted incomplete callback origin")
	}
}
