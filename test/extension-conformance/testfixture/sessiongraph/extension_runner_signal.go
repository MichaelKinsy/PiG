package sessiongraph

import (
	"context"
	"runtime"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// extensionRunnerSignalScript runs the two sequences of testdata/extension-runner-signal.mjs through the Go SDK's ExtensionRunner: a runner over the signal probe whose getSignal answers the context of the client's current run. Every run of Pi has its own AbortSignal (agent.ts:518-527); here a run is a context.WithCancel.
func extensionRunnerSignalScript(ctx sdk.Context, args map[string]any) map[string]any {
	str := func(key string) string { return args[key].(string) }
	cwd, agentDir := str("cwd"), str("agentDir")
	models, store := str("mrModels"), str("mrStore")

	// current is the client's run: the one signal getSignal answers.
	var (
		mu      sync.Mutex
		current context.Context
	)
	set := func(run context.Context) {
		mu.Lock()
		defer mu.Unlock()
		current = run
	}
	newRun := func() (context.Context, context.CancelFunc) {
		return context.WithCancel(context.Background())
	}

	rig := func() (*sdk.ExtensionRunner, *runnerLog, func() []any, []any) {
		rt := ok(sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: str("mrAuth"), ModelsPath: &models, ModelsStorePath: store}))
		settings := ok(sdk.SettingsManagerInMemory(ctx, map[string]any{
			"compaction": map[string]any{"enabled": false}, "retry": map[string]any{"enabled": false}, "defaultProvider": "mr-local", "defaultModel": "m1",
		}, nil))
		loader := ok(sdk.NewDefaultResourceLoader(ctx, sdk.DefaultResourceLoaderOptions{
			Cwd: cwd, AgentDir: agentDir, SettingsManager: &settings, Runtime: ok(sdk.CreateExtensionRuntime(ctx)), NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
			AdditionalExtensionPaths: []string{str("signalProbe")},
		}))
		if err := loader.Reload(nil); err != nil {
			panic(err)
		}
		loaded := ok(loader.GetExtensions())
		manager := ok(sdk.SessionManagerInMemory(ctx, cwd, nil, nil))
		runner := ok(sdk.NewExtensionRunner(ctx, loaded.Extensions, loaded.Runtime, cwd, &manager, rt))
		log := &runnerLog{}
		var errorsMu sync.Mutex
		reported := []any{}
		ok(runner.OnError(func(report sdk.ExtensionError) {
			errorsMu.Lock()
			defer errorsMu.Unlock()
			reported = append(reported, map[string]any{"event": report.Event, "error": report.Error})
		}))
		if err := runner.BindCore(
			sdk.ExtensionActions{AppendEntry: func(customType string, data any) error { log.record("appendEntry", customType, data); return nil }},
			sdk.ExtensionContextActions{GetSignal: func() context.Context {
				mu.Lock()
				defer mu.Unlock()
				return current
			}},
			nil,
		); err != nil {
			panic(err)
		}
		loadErrors := []any{}
		for _, loadErr := range loaded.Errors {
			loadErrors = append(loadErrors, map[string]any{"path": loadErr.Path, "error": loadErr.Error})
		}
		return runner, log, func() []any {
			errorsMu.Lock()
			defer errorsMu.Unlock()
			return append([]any{}, reported...)
		}, loadErrors
	}
	// entries is the log as the Node row lists it: [customType, data] of each appendEntry.
	entries := func(log *runnerLog) []any {
		out := []any{}
		for _, entry := range log.snapshot() {
			call := entry.([]any)
			out = append(out, []any{call[1], call[2]})
		}
		return out
	}
	report := func(log *runnerLog, errs func() []any, loadErrors []any) map[string]any {
		return map[string]any{"entries": entries(log), "errors": errs(), "loadErrors": loadErrors}
	}
	turn := map[string]any{"type": "turn_start", "turnIndex": 0, "timestamp": 1}
	message := map[string]any{"type": "message_start", "message": map[string]any{"role": "user", "content": "hi", "timestamp": 1}}

	r := map[string]any{}

	// Sequence 1, two live runs.
	first, cancelFirst := newRun()
	defer cancelFirst()
	set(first)
	runner, log, errs, loadErrors := rig()
	ok(runner.Emit(map[string]any{"type": "agent_start"}))
	second, cancelSecond := newRun()
	defer cancelSecond()
	set(second)
	ok(runner.Emit(turn))
	cancelSecond()
	ok(runner.Emit(message))
	r["twoLiveRuns"] = report(log, errs, loadErrors)

	// Sequence 2, the own abort missed.
	third, cancelThird := newRun()
	set(third)
	runner, log, errs, loadErrors = rig()
	ok(runner.Emit(map[string]any{"type": "agent_start"}))
	cancelThird()
	fourth, cancelFourth := newRun()
	defer cancelFourth()
	set(fourth)
	// The client has dropped its own reference to run 1's aborted signal (third, cancelThird: no later use) and collects before run 2 is read: the wrapper must hold the signal itself, or it cannot say run 1 had aborted.
	runtime.GC()
	runtime.GC()
	ok(runner.Emit(turn))
	cancelFourth()
	ok(runner.Emit(message))
	r["abortedThenLive"] = report(log, errs, loadErrors)
	return r
}
