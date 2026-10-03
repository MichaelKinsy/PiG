package experimental

// Ports packages/coding-agent/src/experimental/session-worker.ts (coding Harness factory and process entry) and packages/coding-agent/src/experimental/durable/harness-setup.ts (initial model).

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableadapter"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

// RunSessionWorkerProcess runs the real coding Harness factory through the worker's owned control and resource lifetime. The executable validates and consumes the internal role before calling it.
func RunSessionWorkerProcess(ctx context.Context, args []string) error {
	return RunSessionWorkerWithHarness(ctx, args, createCodingAgentHarness)
}

// createCodingAgentHarness opens the Session's durable Harness over its SQLite storage with pi's coding registry, settings and execution environments, and its root conversation with the initial model on first open (session-worker.ts createCodingAgentHarness).
func createCodingAgentHarness(ctx context.Context, databasePath string, options SessionWorkerOptions) (SessionWorkerRuntime, error) {
	cwd := options.Metadata.Cwd
	collaborators, err := coding.NewServices(coding.ServicesOptions{CWD: cwd, AgentDir: codingagent.AgentDir()})
	if err != nil {
		return SessionWorkerRuntime{}, err
	}
	modelRuntime := collaborators.ModelRuntime()
	// ModelRuntime.create awaits an offline refresh by default and leaves provider diagnostics in the runtime.
	_ = modelRuntime.Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
	settings := collaborators.SettingsManager()
	if err := durableagent.ConfigureHarnessHTTP(settings); err != nil {
		return SessionWorkerRuntime{}, err
	}
	envs := durableagent.NewExecutionEnvs(cwd)
	var opened harness.Harness
	runtime, err := func() (SessionWorkerRuntime, error) {
		registry, err := durableagent.CreateCodingRegistry(settings, cwd)
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		store, err := sqlitenode.OpenNodeSqliteStorage(databasePath, sqlitenode.NodeSqliteStorageOptions{})
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		opened, err = harness.OpenHarness(ctx, store, harness.HarnessOptions{
			Models:   modelRuntime,
			Registry: registry,
			Settings: durableagent.CreateHarnessSettings(settings),
			Env:      envs.Env,
			OnReport: func(err error) { fmt.Fprintln(os.Stderr, err) },
		})
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		conversation, err := openRootConversation(ctx, opened, cwd, options, durableagent.ModelSelection{Runtime: modelRuntime, ConfiguredAuth: collaborators.Registry().HasConfiguredAuth}, settings)
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		loader, err := CreateSessionPluginFacetLoader(options.PluginManifestPaths)
		if err != nil {
			return SessionWorkerRuntime{}, err
		}
		session := &durableadapter.Session{Harness: opened, Conversation: conversation}
		return SessionWorkerRuntime{
			Harness: session, Conversation: session, FacetLoader: loader,
			ModelRuntime: workerModelsServiceRuntime{runtime: modelRuntime}, SettingsManager: settings,
			Cleanup: envs.Cleanup,
		}, nil
	}()
	if err != nil {
		// Close the Harness, then the environments; a failure of the first skips the second, as the awaited calls do upstream.
		var cleanupErr error
		if opened != nil {
			cleanupErr = opened.Close(context.WithoutCancel(ctx))
		}
		if cleanupErr == nil {
			cleanupErr = envs.Cleanup(context.WithoutCancel(ctx))
		}
		if cleanupErr != nil {
			return SessionWorkerRuntime{}, fmt.Errorf("Session worker Harness startup and cleanup failed: %w", errors.Join(err, cleanupErr))
		}
		return SessionWorkerRuntime{}, err
	}
	return runtime, nil
}

// openRootConversation returns the Harness's root conversation. The initial model applies only when the root conversation is created; later starts keep its durable model.
func openRootConversation(ctx context.Context, opened harness.Harness, cwd string, options SessionWorkerOptions, selection durableagent.ModelSelection, settings *coding.SettingsManager) (harness.Conversation, error) {
	existing, err := opened.Conversation(ctx, durable.ROOT_CONVERSATION_ID)
	if err != nil {
		return nil, err
	}
	agent := harness.AgentChange{Cwd: harness.SetTo(cwd)}
	if existing == nil {
		initial, err := durableagent.FindInitialAgentModel(selection, settings, options.Provider, options.Model)
		if err != nil {
			return nil, err
		}
		if initial.Model != nil {
			agent.Model = harness.SetTo(*initial.Model)
		}
		if initial.ThinkingLevel != "" {
			agent.ThinkingLevel = harness.SetTo(ai.ModelThinkingLevel(initial.ThinkingLevel))
		}
	}
	return opened.Root(ctx, &harness.RootOptions{Agent: &agent})
}

type workerModelsServiceRuntime struct{ runtime *coding.ModelRuntime }

func (view workerModelsServiceRuntime) GetAvailableSnapshot() []*ai.Model {
	return view.runtime.GetAvailableSnapshot()
}
func (view workerModelsServiceRuntime) GetModel(provider, id string) *ai.Model {
	return view.runtime.GetModel(provider, id)
}
func (view workerModelsServiceRuntime) Refresh(ctx context.Context) (services.ModelsRefreshResult, error) {
	result := view.runtime.Refresh(ctx)
	return services.ModelsRefreshResult{Aborted: result.Aborted, Errors: result.Errors}, nil
}
