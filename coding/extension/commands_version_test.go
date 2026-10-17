package extension

import "testing"

// The commands version moves with every write to an extension's registered commands, and only with those, so a reader that derived something from the commands knows when it is stale.
func TestExtensionCommandsVersionMovesWithEveryCommandWrite(t *testing.T) {
	ext := Extension{Name: "versioned"}
	if _, ok := ext.CommandsVersion(); ok {
		t.Fatal("an extension without a synchronized registry has a commands version")
	}
	ext.InitializeDeclarationRegistry()
	last, ok := ext.CommandsVersion()
	if !ok {
		t.Fatal("no commands version after the registry was initialized")
	}
	moved := func(step string) {
		t.Helper()
		got, _ := ext.CommandsVersion()
		if got <= last {
			t.Fatalf("%s did not move the version: %d after %d", step, got, last)
		}
		last = got
	}
	unchanged := func(step string) {
		t.Helper()
		if got, _ := ext.CommandsVersion(); got != last {
			t.Fatalf("%s moved the version: %d after %d", step, got, last)
		}
	}
	ext.SetRegisteredCommand("first", RegisteredCommand{Description: "one"})
	moved("a new command")
	ext.SetRegisteredCommand("first", RegisteredCommand{Description: "replaced"})
	moved("a replaced command")
	ext.SetCommandSourceInfo(SourceInfo{Path: "/p"})
	moved("source info")
	replacement := Extension{Name: "restarted"}
	replacement.SetRegisteredCommand("second", RegisteredCommand{Description: "two"})
	ext.ReplaceDeclarations(&replacement)
	moved("a restart's declarations")
	ext.RegisteredCommands()
	ext.SetRegisteredFlag("flag", ExtensionFlag{})
	unchanged("reading the commands or registering a flag")
}
