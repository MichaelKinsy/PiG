package extension

import (
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
)

func declarationNames(e *Extension) []string {
	var names []string
	for _, command := range e.RegisteredCommands() {
		names = append(names, "command:"+command.Name+"="+command.Description)
	}
	for _, flag := range e.RegisteredFlags() {
		names = append(names, "flag:"+flag.Name+"="+flag.Description)
	}
	for _, shortcut := range e.RegisteredShortcuts() {
		names = append(names, "shortcut:"+string(shortcut.Shortcut)+"="+shortcut.Description)
	}
	return names
}

// Pi keeps commands, flags and shortcuts in Maps (loader.ts registerCommand, registerFlag, registerShortcut): insertion order, and a re-registered name keeps its place with the new value. A copy of the Extension shares the registry once it exists, so a late registration reaches every holder.
func TestDeclarationRegistryKeepsRegistrationOrder(t *testing.T) {
	ext := &Extension{}
	ext.SetRegisteredCommand("zeta", RegisteredCommand{Name: "zeta", Description: "first"})
	ext.SetRegisteredCommand("alpha", RegisteredCommand{Name: "alpha", Description: "a"})
	ext.SetRegisteredFlag("zflag", ExtensionFlag{Name: "zflag", Description: "z"})
	ext.SetRegisteredShortcut("ctrl+z", ExtensionShortcut{Shortcut: "ctrl+z", Description: "z"})
	ext.SetRegisteredShortcut("ctrl+a", ExtensionShortcut{Shortcut: "ctrl+a", Description: "a"})
	ext.InitializeDeclarationRegistry()
	shared := *ext
	ext.SetRegisteredCommand("zeta", RegisteredCommand{Name: "zeta", Description: "second"})
	ext.SetRegisteredCommand("late", RegisteredCommand{Name: "late", Description: "late"})
	ext.SetRegisteredFlag("aflag", ExtensionFlag{Name: "aflag", Description: "a"})
	want := []string{"command:zeta=second", "command:alpha=a", "command:late=late", "flag:zflag=z", "flag:aflag=a", "shortcut:ctrl+z=z", "shortcut:ctrl+a=a"}
	if got := declarationNames(&shared); !slices.Equal(got, want) {
		t.Fatalf("declarations = %q, want %q", got, want)
	}
}

// The construction fields keep working without the registry: a direct Extension value lists its commands in CommandOrder, then the names the order omits, sorted, and a value without a name takes its key.
func TestDeclarationFieldsWithoutRegistry(t *testing.T) {
	ext := &Extension{
		Commands:     map[string]RegisteredCommand{"b": {Description: "b"}, "a": {Description: "a"}, "c": {Description: "c"}},
		CommandOrder: []string{"c"},
		Flags:        map[string]ExtensionFlag{"f": {Description: "f"}},
		Shortcuts:    map[KeyID]ExtensionShortcut{"ctrl+k": {Description: "k"}},
	}
	want := []string{"command:c=c", "command:a=a", "command:b=b", "flag:f=f", "shortcut:ctrl+k=k"}
	if got := declarationNames(ext); !slices.Equal(got, want) {
		t.Fatalf("declarations = %q, want %q", got, want)
	}
	ext.InitializeDeclarationRegistry()
	if got := declarationNames(ext); !slices.Equal(got, want) {
		t.Fatalf("declarations after initialization = %q, want %q", got, want)
	}
}

// A restart replaces every declaration with the new registration's, and a command the new registration leaves without source info takes the extension's.
func TestReplaceDeclarationsAdoptsTheNewRegistration(t *testing.T) {
	info := SourceInfo{Path: "/ext.mjs", Source: "local"}
	held := &Extension{}
	held.SetRegisteredCommand("old", RegisteredCommand{Name: "old", Description: "old"})
	held.SetRegisteredCommand("kept", RegisteredCommand{Name: "kept", Description: "gen 1"})
	held.SetRegisteredFlag("flag", ExtensionFlag{Name: "flag", Description: "gen 1", Type: FlagString, Default: "gen 1"})
	held.SetRegisteredShortcut("ctrl+g", ExtensionShortcut{Shortcut: "ctrl+g", Description: "gen 1"})
	held.InitializeDeclarationRegistry()
	held.SetCommandSourceInfo(info)
	built := &Extension{}
	built.SetRegisteredCommand("kept", RegisteredCommand{Name: "kept", Description: "gen 2"})
	built.SetRegisteredFlag("flag", ExtensionFlag{Name: "flag", Description: "gen 2", Type: FlagBoolean, Default: true})
	built.SetRegisteredShortcut("ctrl+g", ExtensionShortcut{Shortcut: "ctrl+g", Description: "gen 2"})
	held.ReplaceDeclarations(built)
	want := []string{"command:kept=gen 2", "flag:flag=gen 2", "shortcut:ctrl+g=gen 2"}
	if got := declarationNames(held); !slices.Equal(got, want) {
		t.Fatalf("declarations = %q, want %q", got, want)
	}
	if command, _ := held.Command("kept"); command.SourceInfo != info {
		t.Fatalf("restarted command source info = %+v, want %+v", command.SourceInfo, info)
	}
	if flag, _ := held.RegisteredFlag("flag"); flag.Type != FlagBoolean || flag.Default != true {
		t.Fatalf("restarted flag = %+v", flag)
	}
	if _, ok := held.Command("old"); ok {
		t.Fatal("a command only the old registration had survived the restart")
	}
}

// Runners, the CLI and the host read the declarations while a late registration or a restart writes them. Run with -race: every access goes through the registry's lock.
func TestDeclarationRegistryConcurrentReadersAndWriters(t *testing.T) {
	held := &Extension{}
	held.SetRegisteredCommand("probe", RegisteredCommand{Name: "probe"})
	held.InitializeDeclarationRegistry()
	holders := []Extension{*held, *held}
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for j := range 200 {
				switch i {
				case 0:
					built := &Extension{}
					built.SetRegisteredCommand("probe", RegisteredCommand{Name: "probe", Description: fmt.Sprint(j)})
					built.SetRegisteredFlag("flag", ExtensionFlag{Name: "flag", Description: fmt.Sprint(j)})
					built.SetRegisteredShortcut("ctrl+g", ExtensionShortcut{Shortcut: "ctrl+g"})
					held.ReplaceDeclarations(built)
				case 1:
					held.SetRegisteredCommand(fmt.Sprint("late", j%5), RegisteredCommand{Name: fmt.Sprint("late", j%5)})
					held.SetRegisteredFlag("late", ExtensionFlag{Name: "late"})
					held.SetRegisteredShortcut("ctrl+l", ExtensionShortcut{Shortcut: "ctrl+l"})
					held.SetCommandSourceInfo(SourceInfo{Path: fmt.Sprint(j)})
				default:
					holder := &holders[i-2]
					_ = holder.RegisteredCommands()
					_, _ = holder.Command("probe")
					_ = holder.RegisteredFlags()
					_, _ = holder.RegisteredFlag("flag")
					_ = holder.RegisteredShortcuts()
				}
			}
		})
	}
	wg.Wait()
	if _, ok := holders[0].Command("probe"); !ok {
		t.Fatal("the shared registry lost the restarted command")
	}
}

// A restart makes the message and entry renderers exactly the new registration's: a custom type the new process registers renders, and one only the old process registered (at load or later through registerDeclaration) is gone instead of reaching an extension that no longer serves it.
func TestReplaceDeclarationsAdoptsTheNewRenderers(t *testing.T) {
	held := &Extension{}
	held.SetMessageRenderer("old", nil)
	held.SetEntryRenderer("old", nil)
	held.InitializeDeclarationRegistry()
	held.SetMessageRenderer("late", nil)
	built := &Extension{}
	built.SetMessageRenderer("new", nil)
	built.SetEntryRenderer("new", nil)
	built.InitializeDeclarationRegistry()
	held.ReplaceDeclarations(built)
	messages, entries := held.RegisteredRenderers()
	if got := slices.Sorted(maps.Keys(messages)); !slices.Equal(got, []string{"new"}) {
		t.Fatalf("message renderers after restart = %q, want [new]", got)
	}
	if got := slices.Sorted(maps.Keys(entries)); !slices.Equal(got, []string{"new"}) {
		t.Fatalf("entry renderers after restart = %q, want [new]", got)
	}
	if _, ok := held.MessageRendererFor("new"); !ok {
		t.Fatal("a message renderer the restarted process registered is not found")
	}
}
