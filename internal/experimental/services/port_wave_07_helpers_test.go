package services

import "testing"

func slashCommandNames(commands []SlashCommandContribution) []string {
	names := make([]string, len(commands))
	for i, command := range commands {
		names[i] = command.Name
	}
	return names
}

func assertSlashCommand(t *testing.T, got []SlashCommandContribution, want SlashCommandContribution) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("commands = %#v, want one command", got)
	}
	checkModelsEqual(t, got[0].Name, want.Name)
	checkModelsEqual(t, got[0].Description, want.Description)
	checkModelsEqual(t, got[0].ArgumentHint, want.ArgumentHint)
	checkModelsEqual(t, got[0].GetArgumentCompletions == nil, want.GetArgumentCompletions == nil)
	if got[0].Run == nil {
		t.Fatal("command lost its run callback")
	}
}
