package inproc

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestRPCCommandResolutionPreservesExtensionAndRegistrationOrder(t *testing.T) {
	firstSource := extension.SourceInfo{Path: "/ext/first", Source: "local", Scope: "user", Origin: "top-level"}
	secondSource := extension.SourceInfo{Path: "/ext/second", Source: "pkg:second", Scope: "project", Origin: "package", BaseDir: "/pkg"}
	first := extension.Extension{
		Name: "first", Path: "/ext/first", SourceInfo: firstSource,
		Commands: map[string]extension.RegisteredCommand{
			"z":   {Name: "z"},
			"dup": {Name: "dup"},
		},
		CommandOrder: []string{"z", "dup"},
	}
	second := extension.Extension{
		Name: "second", Path: "/ext/second", SourceInfo: secondSource,
		Commands: map[string]extension.RegisteredCommand{
			"dup": {Name: "dup"},
			"a":   {Name: "a"},
		},
		CommandOrder: []string{"dup", "a"},
	}
	runner := NewRunner([]extension.Extension{first, second}, t.TempDir())
	commands := runner.Commands()
	got := make([]string, len(commands))
	for i, command := range commands {
		got[i] = command.InvocationName
	}
	if want := []string{"z", "dup:1", "dup:2", "a"}; !slices.Equal(got, want) {
		t.Fatalf("commands=%v want=%v", got, want)
	}
	if commands[0].SourceInfo == (extension.SourceInfo{}) || commands[1].SourceInfo == (extension.SourceInfo{}) || commands[2].SourceInfo == (extension.SourceInfo{}) || commands[3].SourceInfo == (extension.SourceInfo{}) {
		t.Fatalf("sourceInfo not stamped: %#v", commands)
	}
	if commands[0].SourceInfo.Path != "/ext/first" || commands[2].SourceInfo.Path != "/ext/second" {
		t.Fatalf("sourceInfo=%#v", commands)
	}
}

func TestRPCExecuteCommandUsesCommandContextAndReportsHandlerError(t *testing.T) {
	var called bool
	ext := extension.Extension{
		Name: "command", Path: "/ext/command", CommandOrder: []string{"run", "fail"},
		Commands: map[string]extension.RegisteredCommand{
			"run": {
				Name: "run",
				Handler: func(ctx context.Context, args string) error {
					called = args == "now" && extension.CommandContextFromContext(ctx) != nil && extension.FromContext(ctx) != nil
					return nil
				},
			},
			"fail": {Name: "fail", Handler: func(context.Context, string) error { return errors.New("boom") }},
		},
	}
	runner := NewRunner([]extension.Extension{ext}, t.TempDir())
	var errorsSeen []extension.ExtensionError
	runner.AddErrorListener(func(err *extension.ExtensionError) { errorsSeen = append(errorsSeen, *err) })
	if !runner.ExecuteCommand(context.Background(), "run", "now") || !called {
		t.Fatalf("command handled=%v called=%v", true, called)
	}
	if !runner.ExecuteCommand(context.Background(), "fail", "") {
		t.Fatal("failing command was not handled")
	}
	if len(errorsSeen) != 1 || errorsSeen[0].ExtensionPath != "command:fail" || errorsSeen[0].Event != "command" || errorsSeen[0].Error != "boom" {
		t.Fatalf("errors=%#v", errorsSeen)
	}
	if runner.ExecuteCommand(context.Background(), "missing", "") {
		t.Fatal("missing command was handled")
	}
}

func TestRPCCommandsConcurrentAndAvailableAfterInvalidation(t *testing.T) {
	ext := extension.Extension{
		Name: "ordered", Path: "/ext/ordered", CommandOrder: []string{"one", "two"},
		Commands: map[string]extension.RegisteredCommand{"one": {Name: "one"}, "two": {Name: "two"}},
	}
	runner := NewRunner([]extension.Extension{ext}, t.TempDir())
	runner.Invalidate("replaced")
	var wait sync.WaitGroup
	for range 32 {
		wait.Go(func() {
			commands := runner.Commands()
			if len(commands) != 2 || commands[0].InvocationName != "one" || commands[1].InvocationName != "two" {
				t.Errorf("commands=%#v", commands)
			}
		})
	}
	wait.Wait()
}
