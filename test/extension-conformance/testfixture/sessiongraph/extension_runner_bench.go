package sessiongraph

import (
	"runtime"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// extensionRunnerBench times n repetitions of one operation of the extension_runner row's objects, from inside the extension (BenchmarkSessionGraphExtensionRunner reads "nsPerOp"):
//   - create: a runner over the two loaded probe extensions and the runtime of their load;
//   - emit: one session_before_switch event through the runner to the handlers of both probes;
//   - spawn: a loader that loads and starts both probe extensions.
//
// The loader's first load, the model runtime and the session manager are set up before the clock starts.
func extensionRunnerBench(ctx sdk.Context, args map[string]any) map[string]any {
	str := func(key string) string { return args[key].(string) }
	op, n := str("op"), int(args["n"].(float64))
	cwd, agentDir := str("cwd"), str("agentDir")
	models, store := str("mrModels"), str("mrStore")
	rt := ok(sdk.ModelRuntimeCreate(ctx, sdk.ModelRuntimeCreateOptions{AuthPath: str("mrAuth"), ModelsPath: &models, ModelsStorePath: store}))
	settings := ok(sdk.SettingsManagerInMemory(ctx, map[string]any{"compaction": map[string]any{"enabled": false}, "retry": map[string]any{"enabled": false}}, nil))
	load := func() (*sdk.DefaultResourceLoader, sdk.LoadExtensionsResult) {
		loader := ok(sdk.NewDefaultResourceLoader(ctx, sdk.DefaultResourceLoaderOptions{
			Cwd: cwd, AgentDir: agentDir, SettingsManager: &settings, Runtime: ok(sdk.CreateExtensionRuntime(ctx)), NoExtensions: true, NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
			AdditionalExtensionPaths: []string{str("runnerProbeA"), str("runnerProbeB")},
		}))
		if err := loader.Reload(nil); err != nil {
			panic(err)
		}
		return loader, ok(loader.GetExtensions())
	}
	loader, loaded := load()
	manager := ok(sdk.SessionManagerInMemory(ctx, cwd, nil, nil))
	runner := ok(sdk.NewExtensionRunner(ctx, loaded.Extensions, loaded.Runtime, cwd, &manager, rt))
	ok(runner.Emit(map[string]any{"type": "session_start", "reason": "startup"}))

	var step func()
	switch op {
	case "create":
		step = func() {
			ok(sdk.NewExtensionRunner(ctx, loaded.Extensions, loaded.Runtime, cwd, &manager, rt))
		}
	case "emit":
		step = func() { ok(runner.Emit(map[string]any{"type": "session_before_switch", "reason": "new"})) }
	case "spawn":
		step = func() { load() }
	default:
		panic("unknown benchmark operation " + op)
	}
	start := time.Now()
	for range n {
		step()
	}
	elapsed := time.Since(start)
	// The runners hold the loader of their extensions only while it lives (a loader released with its handle takes its extensions with it).
	runtime.KeepAlive(loader)
	return map[string]any{"nsPerOp": float64(elapsed.Nanoseconds()) / float64(n)}
}
