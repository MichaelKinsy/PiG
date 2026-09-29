package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

func TestPortWave07ExperimentalSlashCommands(t *testing.T) {
	t.Parallel()
	// upstream: packages/coding-agent/test/experimental-slash-commands.test.ts:10.
	t.Run("registers and removes contributions", func(t *testing.T) {
		t.Parallel()
		registry := NewSlashCommandRegistry()
		var snapshots [][]string
		unsubscribe := registry.Subscribe(func(commands []SlashCommandContribution) { snapshots = append(snapshots, slashCommandNames(commands)) })
		t.Cleanup(unsubscribe)
		closeCommand, err := registry.Register(SlashCommandContribution{Name: "hello", Description: new("Hello"), Run: func(context.Context, string) (SlashCommandRunResult, error) { return nil, nil }})
		requireModelsOK(t, err)
		checkModelsEqual(t, slashCommandNames(registry.List()), []string{"hello"})
		_, err = registry.Register(SlashCommandContribution{Name: "hello", Run: func(context.Context, string) (SlashCommandRunResult, error) { return nil, nil }})
		if err == nil || !strings.Contains(err.Error(), "already registered") {
			t.Fatalf("duplicate registration error = %v", err)
		}
		closeCommand()
		closeCommand()
		checkModelsEqual(t, registry.List(), []SlashCommandContribution{})
		checkModelsEqual(t, snapshots, [][]string{{}, {"hello"}, {}})
		unsubscribe()
	})

	// upstream: packages/coding-agent/test/experimental-slash-commands.test.ts:24.
	t.Run("stages replacements until the previous registration retires", func(t *testing.T) {
		t.Parallel()
		registry := NewSlashCommandRegistry()
		var firstCalls, secondCalls int
		first := SlashCommandContribution{Name: "hello", Description: new("First"), Run: func(context.Context, string) (SlashCommandRunResult, error) { firstCalls++; return nil, nil }}
		second := SlashCommandContribution{Name: "hello", Description: new("Second"), Run: func(context.Context, string) (SlashCommandRunResult, error) { secondCalls++; return nil, nil }}
		closeFirst, err := registry.Register(first)
		requireModelsOK(t, err)
		closeSecond, err := registry.Replace(second)
		requireModelsOK(t, err)
		assertSlashCommand(t, registry.List(), first)
		_, err = registry.List()[0].Run(t.Context(), "")
		requireModelsOK(t, err)
		checkModelsEqual(t, []int{firstCalls, secondCalls}, []int{1, 0})

		closeSecond()
		assertSlashCommand(t, registry.List(), first)
		_, err = registry.List()[0].Run(t.Context(), "")
		requireModelsOK(t, err)
		checkModelsEqual(t, []int{firstCalls, secondCalls}, []int{2, 0})
		closeReplacement, err := registry.Replace(second)
		requireModelsOK(t, err)
		closeFirst()
		assertSlashCommand(t, registry.List(), second)
		_, err = registry.List()[0].Run(t.Context(), "")
		requireModelsOK(t, err)
		checkModelsEqual(t, []int{firstCalls, secondCalls}, []int{2, 1})
		closeReplacement()
		checkModelsEqual(t, registry.List(), []SlashCommandContribution{})
	})

	// upstream: packages/coding-agent/test/experimental-slash-commands.test.ts:41.
	t.Run("tracks plugin facet reload and unload", func(t *testing.T) {
		t.Parallel()
		registry := NewSlashCommandRegistry()
		var originalCalls, replacementCalls, failingCalls int
		originalRun := func(context.Context, string) (SlashCommandRunResult, error) { originalCalls++; return nil, nil }
		original := SlashCommandContribution{Name: "hello", Description: new("Original"), Run: originalRun}
		host, err := chord.CreateFacetHost(t.Context(), chord.FacetOptions{Facets: []chord.Facet{CreateSlashCommandsRuntimeFacet(registry), slashCommandTestFacet(original, nil)}})
		requireModelsOK(t, err)
		t.Cleanup(func() { requireModelsOK(t, host.Dispose(context.Background())) })
		checkModelsEqual(t, slashCommandNames(registry.List()), []string{"hello"})
		failure := errors.New("replacement failed")
		failing := SlashCommandContribution{Name: "hello", Description: new("Failing"), Run: func(context.Context, string) (SlashCommandRunResult, error) { failingCalls++; return nil, nil }}
		if err := host.Reload(t.Context(), []chord.Facet{slashCommandTestFacet(failing, failure)}); err != failure { //nolint:errorlint // Upstream rejects.toBe(failure) requires identity, not a wrapped match.
			t.Fatalf("reload error = %v, want original failure %v", err, failure)
		}
		assertSlashCommand(t, registry.List(), original)
		// Go functions are not comparable. Calling the retained callback proves the original spy remains installed rather than comparing closure code addresses.
		_, err = registry.List()[0].Run(t.Context(), "")
		requireModelsOK(t, err)
		checkModelsEqual(t, []int{originalCalls, failingCalls, replacementCalls}, []int{1, 0, 0})

		replacementRun := func(context.Context, string) (SlashCommandRunResult, error) { replacementCalls++; return nil, nil }
		replacement := SlashCommandContribution{Name: "hello", Description: new("Replacement"), Run: replacementRun}
		requireModelsOK(t, host.Reload(t.Context(), []chord.Facet{slashCommandTestFacet(replacement, nil)}))
		assertSlashCommand(t, registry.List(), replacement)
		_, err = registry.List()[0].Run(t.Context(), "")
		requireModelsOK(t, err)
		checkModelsEqual(t, []int{originalCalls, failingCalls, replacementCalls}, []int{1, 0, 1})
		requireModelsOK(t, host.Dispose(t.Context()))
		checkModelsEqual(t, registry.List(), []SlashCommandContribution{})
	})
}

func slashCommandTestFacet(command SlashCommandContribution, failure error) chord.Facet {
	return chord.Facet{Id: "@test/example-hello", Setup: func(env *chord.FacetEnvironment) error {
		commands, err := chord.UseService(env, SlashCommandsDefinition)
		if err != nil {
			return err
		}
		return env.OnActivate(func(context.Context) error {
			service, err := commands.Get()
			if err != nil {
				return err
			}
			closeCommand, err := service.Replace(command)
			if err != nil {
				return err
			}
			if err := env.Own(func(context.Context) error { closeCommand(); return nil }); err != nil {
				return err
			}
			return failure
		})
	}}
}
