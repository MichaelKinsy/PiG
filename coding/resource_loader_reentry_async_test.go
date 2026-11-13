package coding

// pi: packages/coding-agent/src/core/resource-loader.ts

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// awaitReload fails the test when a reload started on another goroutine does not return: a call made from one of its callbacks waits for the operation it is made from.
func awaitReload(t *testing.T, done <-chan error, what string) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never returned: a call made from its callback waits for the reload it is made from", what)
	}
}

// resource-loader.ts has no lock, so a callback of a running reload() may call the loader: reload() starts at once and extendResources() runs to its end (:541-710). The override functions get no context to carry the operation, and neither do ExtendResources and Close: a call made on the goroutine of the operation in progress runs inside it.
func TestDefaultResourceLoaderOverrideMayCallTheLoader(t *testing.T) {
	for _, override := range []string{"skills", "prompts", "themes", "extensions", "agentsFiles", "systemPrompt", "appendSystemPrompt"} {
		t.Run(override, func(t *testing.T) {
			var f *reloadFixture
			var calls atomic.Int32
			var nestedErr atomic.Value
			reenter := func() {
				if calls.Add(1) != 1 {
					return
				}
				if err := f.loader.Reload(); err != nil {
					nestedErr.Store(err)
				}
				if err := f.loader.ExtendResources(ResourceExtensionPaths{}); err != nil {
					nestedErr.Store(err)
				}
				f.loader.Close()
			}
			f = newReloadFixtureWith(t, nil, func(options *DefaultResourceLoaderOptions) {
				switch override {
				case "skills":
					options.SkillsOverride = func(result SkillsResult) SkillsResult { reenter(); return result }
				case "prompts":
					options.PromptsOverride = func(result PromptsResult) PromptsResult { reenter(); return result }
				case "themes":
					options.ThemesOverride = func(result ThemesResult) ThemesResult { reenter(); return result }
				case "extensions":
					options.ExtensionsOverride = func(result LoadExtensionsResult) LoadExtensionsResult { reenter(); return result }
				case "agentsFiles":
					options.AgentsFilesOverride = func(result AgentsFilesResult) AgentsFilesResult { reenter(); return result }
				case "systemPrompt":
					options.SystemPromptOverride = func(prompt *string) *string { reenter(); return prompt }
				case "appendSystemPrompt":
					options.AppendSystemPromptOverride = func(appended []string) []string { reenter(); return appended }
				}
			})
			done := make(chan error, 1)
			go func() { done <- f.loader.Reload() }()
			awaitReload(t, done, "a reload whose "+override+" override calls the loader")
			if calls.Load() == 0 {
				t.Fatal("the override never ran")
			}
			if err, _ := nestedErr.Load().(error); err != nil {
				t.Fatalf("nested call: %v", err)
			}
			if got, want := f.state(), `skills=[skill-x] system="one"`; got != want {
				t.Fatalf("final state = %s, want %s", got, want)
			}
		})
	}
}

// A callback that receives a context (the project-trust callback and the extension load) may also call ExtendResources and Close, which take none.
func TestDefaultResourceLoaderCallbackMayExtendAndClose(t *testing.T) {
	for _, where := range []string{"trust callback", "bootstrap extension load", "final extension load"} {
		t.Run(where, func(t *testing.T) {
			var f *reloadFixture
			var calls atomic.Int32
			var nestedErr atomic.Value
			reenter := func() {
				if calls.Add(1) != 1 {
					return
				}
				if err := f.loader.ExtendResources(ResourceExtensionPaths{}); err != nil {
					nestedErr.Store(err)
				}
				f.loader.Close()
			}
			f = newReloadFixture(t, func(_ context.Context, request ExtensionLoadRequest) error {
				if (where == "bootstrap extension load" && request.Bootstrap) || (where == "final extension load" && !request.Bootstrap) {
					reenter()
				}
				return nil
			})
			done := make(chan error, 1)
			go func() {
				done <- f.loader.ReloadWith(t.Context(), ResourceLoaderReloadOptions{ResolveProjectTrust: func(context.Context, ResolveProjectTrustInput) (bool, error) {
					if where == "trust callback" {
						reenter()
					}
					return false, nil
				}})
			}()
			awaitReload(t, done, "a reload whose "+where+" extends and closes the loader")
			if calls.Load() == 0 {
				t.Fatal("the callback never ran")
			}
			if err, _ := nestedErr.Load().(error); err != nil {
				t.Fatalf("nested call: %v", err)
			}
		})
	}
}

// A callback may hand its context to another goroutine and wait for the reload that goroutine makes with it, as a Pi callback awaits a reload() it started: the context carries the operation, so the nested reload does not wait for the lock its caller holds.
func TestDefaultResourceLoaderCallbackMayReloadWithItsContextOnAnotherGoroutine(t *testing.T) {
	var f *reloadFixture
	var calls atomic.Int32
	var nestedErr atomic.Value
	f = newReloadFixture(t, func(ctx context.Context, request ExtensionLoadRequest) error {
		if request.Bootstrap || calls.Add(1) != 1 {
			return nil
		}
		nested := make(chan error, 1)
		go func() { nested <- f.loader.ReloadWith(ctx) }()
		if err := <-nested; err != nil {
			nestedErr.Store(err)
		}
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- f.loader.ReloadWith(t.Context()) }()
	awaitReload(t, done, "a reload whose extension load waits for a reload made with its context on another goroutine")
	if err, _ := nestedErr.Load().(error); err != nil {
		t.Fatalf("nested reload: %v", err)
	}
	if calls.Load() < 2 {
		t.Fatalf("extension loads = %d, want the outer and the nested one", calls.Load())
	}
}

// The context a callback received marks its operation only while that operation runs. A reload made with it after the operation ended is a reload from outside: it waits for the reload in progress instead of running beside it, so the final state is the one of the reload that started last.
func TestDefaultResourceLoaderContextOfAnEndedReloadDoesNotBypassTheOperationLock(t *testing.T) {
	var kept context.Context
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	f := newReloadFixture(t, func(ctx context.Context, request ExtensionLoadRequest) error {
		switch calls.Add(1) {
		case 1:
			kept = ctx
		case 2:
			close(entered)
			<-release
		}
		return nil
	})
	// One goroutine makes the first reload and then the second, so the second is also a reload from outside for the goroutine of the ended one.
	first, second, stale := make(chan error, 1), make(chan error, 1), make(chan error, 1)
	go func() {
		first <- f.loader.ReloadWith(t.Context())
		second <- f.loader.Reload()
	}()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	<-entered
	if kept == nil {
		t.Fatal("the first reload handed its extension load no context")
	}
	f.setInputs(t, "y", "two")
	go func() { stale <- f.loader.ReloadWith(kept) }()
	select {
	case err := <-stale:
		t.Fatalf("a reload made with the context of an ended reload ran beside the reload in progress (error %v, %d extension loads, state %s)", err, calls.Load(), f.state())
	case <-time.After(50 * time.Millisecond):
	}
	releaseOnce()
	awaitReload(t, second, "the reload in progress")
	awaitReload(t, stale, "the reload made with the kept context")
	if got, want := f.state(), `skills=[skill-y] system="two"`; got != want {
		t.Fatalf("final state = %s, want %s", got, want)
	}
}

// D632, recorded from Pi: resource-loader.ts reload() has no lock. A reload suspended in its extension load (:618) lets a later reload run to its end, and when it resumes it assigns the skill paths it resolved before the await (:585-590, :635) next to a system prompt it reads after it (:689-691): the loader ends with the first reload's skills and the second reload's system prompt. PiG's reloads wait for each other and end in the state of the reload that started last (TestDefaultResourceLoaderOverlappingReloadsEndInTheLastInputsState). This test fails when Pi stops mixing, which is when D632 goes.
func TestPiOverlappingReloadsMixTheirResultsD632(t *testing.T) {
	var pi struct {
		AfterSecond string `json:"afterSecond"`
		Final       string `json:"final"`
	}
	pioracle.Run(t, `
const { DefaultResourceLoader } = await load("pi-coding-agent/core/resource-loader.js");
const fs = await import("node:fs");
const os = await import("node:os");
const path = await import("node:path");
const root = fs.mkdtempSync(path.join(os.tmpdir(), "reload-overlap-"));
const agent = path.join(root, "agent");
const project = path.join(root, "project");
fs.mkdirSync(agent); fs.mkdirSync(project);
for (const name of ["x", "y"]) {
	const dir = path.join(root, "skills-" + name, "skill-" + name);
	fs.mkdirSync(dir, { recursive: true });
	fs.writeFileSync(path.join(dir, "SKILL.md"), "---\nname: skill-" + name + "\ndescription: skill " + name + "\n---\nbody\n");
}
const setInputs = (skill, system) => {
	fs.writeFileSync(path.join(agent, "settings.json"), JSON.stringify({ skills: [path.join(root, "skills-" + skill)] }));
	fs.writeFileSync(path.join(agent, "SYSTEM.md"), system);
};
setInputs("x", "one");
let calls = 0;
let release;
const gate = new Promise((resolve) => { release = resolve; });
let entered;
const suspended = new Promise((resolve) => { entered = resolve; });
const loader = new DefaultResourceLoader({ cwd: project, agentDir: agent, noContextFiles: true, extensionFactories: [async () => {
	if (++calls === 1) { entered(); await gate; }
}] });
const state = () => "skills=[" + loader.getSkills().skills.map((skill) => skill.name).join(" ") + "] system=" + JSON.stringify(loader.getSystemPrompt());
const first = loader.reload();
await suspended;
setInputs("y", "two");
await loader.reload();
const afterSecond = state();
release();
await first;
emit({ afterSecond, final: state() });
fs.rmSync(root, { recursive: true, force: true });
`, nil, &pi)
	if want := `skills=[skill-y] system="two"`; pi.AfterSecond != want {
		t.Fatalf("Pi after the second reload = %s, want %s", pi.AfterSecond, want)
	}
	if want := `skills=[skill-x] system="two"`; pi.Final != want {
		t.Fatalf("Pi after the first reload finished last = %s, want the mixed state %s that D632 records", pi.Final, want)
	}
}
