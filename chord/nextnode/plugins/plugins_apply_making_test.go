package plugins

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/build"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// Ports Pi v1.7.0 packages/chord/test/node/plugins/plugins.test.ts:
// - describe("apply (round 2 of the perf pass, ERNDL-1060)")
// - shared first-making of a kind's current set (ERNDL-1060, ERNDL-1061)

func TestPluginsApplyOneAtATime(t *testing.T) {
	root := t.TempDir()
	writeItemPlugin(t, root, "a", "a1")
	var buildCalls atomic.Int32
	plugins := CreatePlugins(PluginsOptions{
		Build: build.BuildFacet,
		Roots: []model.PluginRoot{{Path: root}},
		Kinds: []KindEntry{{Name: "worker", Definition: KindDefinition{Platform: model.PlatformNode}}},
	}, chordctx.Background())
	defer plugins.Close()

	origBuild := plugins.Build
	plugins.applyBuild = func(names []string, ctx context.Context) (*Built, error) {
		buildCalls.Add(1)
		return origBuild(names, ctx)
	}

	began := make(chan struct{})
	held := make(chan struct{})
	heldStep := RolloutStep{
		Name:  "held",
		Kinds: []string{"worker"},
		Reload: func(_ *Built, _ map[string][]PluginScript, _ context.Context) (StepResult, error) {
			close(began)
			<-held
			return StepResult{}, nil
		},
	}

	firstDone := make(chan RolloutResult, 1)
	firstErr := make(chan error, 1)
	go func() {
		res, err := plugins.Apply([]RolloutStep{heldStep}, chordctx.Background())
		firstDone <- res
		firstErr <- err
	}()
	<-began
	buildCalls.Store(0)

	secondDone := make(chan RolloutResult, 1)
	secondErr := make(chan error, 1)
	go func() {
		res, err := plugins.Apply(nil, chordctx.Background())
		secondDone <- res
		secondErr <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if got := buildCalls.Load(); got != 0 {
		t.Fatalf("build calls while first apply held = %d, want 0", got)
	}

	close(held)
	if err := <-firstErr; err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if res := <-firstDone; res.Stopped != nil {
		t.Fatalf("first apply stopped: %+v", res.Stopped)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if res := <-secondDone; res.Stopped != nil {
		t.Fatalf("second apply stopped: %+v", res.Stopped)
	}
	if got := buildCalls.Load(); got != 1 {
		t.Fatalf("build calls after second apply = %d, want 1", got)
	}
}

func TestPluginsApplyQueuedCallerAbortBuildsNothing(t *testing.T) {
	root := t.TempDir()
	writeItemPlugin(t, root, "a", "a1")
	var buildCalls atomic.Int32
	plugins := CreatePlugins(PluginsOptions{
		Build: build.BuildFacet,
		Roots: []model.PluginRoot{{Path: root}},
		Kinds: []KindEntry{{Name: "worker", Definition: KindDefinition{Platform: model.PlatformNode}}},
	}, chordctx.Background())
	defer plugins.Close()

	origBuild := plugins.Build
	plugins.applyBuild = func(names []string, ctx context.Context) (*Built, error) {
		buildCalls.Add(1)
		return origBuild(names, ctx)
	}

	began := make(chan struct{})
	held := make(chan struct{})
	heldStep := RolloutStep{
		Name:  "held",
		Kinds: []string{"worker"},
		Reload: func(_ *Built, _ map[string][]PluginScript, _ context.Context) (StepResult, error) {
			close(began)
			<-held
			return StepResult{}, nil
		},
	}

	firstErr := make(chan error, 1)
	go func() {
		_, err := plugins.Apply([]RolloutStep{heldStep}, chordctx.Background())
		firstErr <- err
	}()
	<-began
	buildCalls.Store(0)

	ctx2, cancel2 := context.WithCancelCause(chordctx.Background())
	secondErr := make(chan error, 1)
	go func() {
		_, err := plugins.Apply(nil, ctx2)
		secondErr <- err
	}()
	cancel2(errors.New("the caller left"))
	if err := <-secondErr; err == nil || !strings.Contains(err.Error(), "the caller left") {
		t.Fatalf("second apply err = %v, want \"the caller left\"", err)
	}

	thirdDone := make(chan RolloutResult, 1)
	thirdErr := make(chan error, 1)
	go func() {
		res, err := plugins.Apply(nil, chordctx.Background())
		thirdDone <- res
		thirdErr <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if got := buildCalls.Load(); got != 0 {
		t.Fatalf("build calls while first apply held = %d, want 0", got)
	}

	close(held)
	if err := <-firstErr; err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := <-thirdErr; err != nil {
		t.Fatalf("third apply: %v", err)
	}
	if res := <-thirdDone; res.Stopped != nil {
		t.Fatalf("third apply stopped: %+v", res.Stopped)
	}
	if got := buildCalls.Load(); got != 1 {
		t.Fatalf("build calls = %d, want 1", got)
	}
}

func TestPluginsScriptsFirstUsesShareOneMaking(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"a", "b", "c"} {
		writeItemPlugin(t, root, dir, dir)
	}
	var builds atomic.Int32
	plugins := CreatePlugins(PluginsOptions{
		Build: func(plugin model.Plugin, kind string, options model.BuildOptions, ctx context.Context) (model.BuiltFacet, error) {
			builds.Add(1)
			return build.BuildFacet(plugin, kind, options, ctx)
		},
		Roots: []model.PluginRoot{{Path: root}},
		Kinds: []KindEntry{{Name: "worker", Definition: KindDefinition{Platform: model.PlatformNode}}},
	}, chordctx.Background())
	defer plugins.Close()

	const callers = 8
	results := make([][]PluginScript, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			results[i], errs[i] = plugins.Scripts("worker", chordctx.Background())
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if got := builds.Load(); got != 3 {
		t.Fatalf("builds = %d, want 3 (each facet built once)", got)
	}
	for i := 1; i < callers; i++ {
		if len(results[i]) != len(results[0]) {
			t.Fatalf("caller %d len = %d, want %d", i, len(results[i]), len(results[0]))
		}
		for j := range results[0] {
			if results[i][j].Built.Digest != results[0][j].Built.Digest {
				t.Fatalf("caller %d script %d digest mismatch", i, j)
			}
		}
	}
}

func TestPluginsScriptsForgetsFailedFirstMaking(t *testing.T) {
	root := t.TempDir()
	writeItemPlugin(t, root, "a", "a")
	var fail atomic.Bool
	fail.Store(true)
	plugins := CreatePlugins(PluginsOptions{
		Build: func(plugin model.Plugin, kind string, options model.BuildOptions, ctx context.Context) (model.BuiltFacet, error) {
			if fail.Swap(false) {
				return model.BuiltFacet{}, errors.New("the build service is gone")
			}
			return build.BuildFacet(plugin, kind, options, ctx)
		},
		Roots: []model.PluginRoot{{Path: root}},
		Kinds: []KindEntry{{Name: "worker", Definition: KindDefinition{Platform: model.PlatformNode}}},
	}, chordctx.Background())
	defer plugins.Close()

	if _, err := plugins.Scripts("worker", chordctx.Background()); err == nil || !strings.Contains(err.Error(), "the build service is gone") {
		t.Fatalf("first Scripts err = %v, want \"the build service is gone\"", err)
	}
	scripts, err := plugins.Scripts("worker", chordctx.Background())
	if err != nil {
		t.Fatalf("retry Scripts: %v", err)
	}
	if len(scripts) != 1 || scripts[0].Plugin.Name != "@test/a" {
		t.Fatalf("scripts = %+v, want [@test/a]", scripts)
	}
}

func TestPluginsScriptsFirstUseAbortFailsOnlyItsOwnWait(t *testing.T) {
	for _, firstAborts := range []bool{true, false} {
		root := t.TempDir()
		writeItemPlugin(t, root, "a", "a")
		started := make(chan struct{})
		release := make(chan struct{})
		plugins := CreatePlugins(PluginsOptions{
			Build: func(plugin model.Plugin, kind string, options model.BuildOptions, ctx context.Context) (model.BuiltFacet, error) {
				close(started)
				<-release
				return build.BuildFacet(plugin, kind, options, ctx)
			},
			Roots: []model.PluginRoot{{Path: root}},
			Kinds: []KindEntry{{Name: "worker", Definition: KindDefinition{Platform: model.PlatformNode}}},
		}, chordctx.Background())

		goneCtx, cancelGone := context.WithCancelCause(chordctx.Background())
		stayingCtx := chordctx.Background()

		leftErr := make(chan error, 1)
		stayedRes := make(chan []PluginScript, 1)
		stayedErr := make(chan error, 1)

		if firstAborts {
			go func() { _, err := plugins.Scripts("worker", goneCtx); leftErr <- err }()
			<-started
			go func() { s, err := plugins.Scripts("worker", stayingCtx); stayedRes <- s; stayedErr <- err }()
		} else {
			go func() { s, err := plugins.Scripts("worker", stayingCtx); stayedRes <- s; stayedErr <- err }()
			<-started
			go func() { _, err := plugins.Scripts("worker", goneCtx); leftErr <- err }()
		}
		cancelGone(errors.New("the hold ended"))
		if err := <-leftErr; err == nil || !strings.Contains(err.Error(), "the hold ended") {
			t.Fatalf("firstAborts=%v: aborted caller err = %v, want \"the hold ended\"", firstAborts, err)
		}
		close(release)
		if err := <-stayedErr; err != nil {
			t.Fatalf("firstAborts=%v: staying caller err = %v", firstAborts, err)
		}
		if s := <-stayedRes; len(s) != 1 || s[0].Plugin.Name != "@test/a" {
			t.Fatalf("firstAborts=%v: scripts = %+v, want [@test/a]", firstAborts, s)
		}
		plugins.Close()
	}
}

func TestPluginsCloseAbortsFirstMakingUnderWay(t *testing.T) {
	root := t.TempDir()
	writeItemPlugin(t, root, "a", "a")
	started := make(chan struct{})
	plugins := CreatePlugins(PluginsOptions{
		Build: func(plugin model.Plugin, kind string, options model.BuildOptions, ctx context.Context) (model.BuiltFacet, error) {
			close(started)
			<-ctx.Done()
			return model.BuiltFacet{}, context.Cause(ctx)
		},
		Roots: []model.PluginRoot{{Path: root}},
		Kinds: []KindEntry{{Name: "worker", Definition: KindDefinition{Platform: model.PlatformNode}}},
	}, chordctx.Background())

	errCh := make(chan error, 1)
	go func() {
		_, err := plugins.Scripts("worker", chordctx.Background())
		errCh <- err
	}()
	<-started
	plugins.Close()
	if err := <-errCh; err == nil || !strings.Contains(err.Error(), "plugins: the component closed") {
		t.Fatalf("Scripts err = %v, want \"plugins: the component closed\"", err)
	}
	if got, ok := plugins.Current("worker"); ok || got != nil {
		t.Fatalf("Current(worker) = %+v, want nil", got)
	}
}

func TestPluginsCloseAbortsFirstMakingWhenBuildsAlreadyDone(t *testing.T) {
	root := t.TempDir()
	writeItemPlugin(t, root, "a", "a")
	plugins := CreatePlugins(PluginsOptions{
		Build: build.BuildFacet,
		Roots: []model.PluginRoot{{Path: root}},
		Kinds: []KindEntry{{Name: "worker", Definition: KindDefinition{Platform: model.PlatformNode}}},
	}, chordctx.Background())

	if _, err := plugins.Build(nil, chordctx.Background()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	plugins.Close()
	if _, err := plugins.Scripts("worker", chordctx.Background()); err == nil || !strings.Contains(err.Error(), "plugins: the component closed") {
		t.Fatalf("Scripts after Close err = %v, want \"plugins: the component closed\"", err)
	}
	if got, ok := plugins.Current("worker"); ok || got != nil {
		t.Fatalf("Current(worker) = %+v, want nil", got)
	}
}

// writeItemPlugin writes a minimal single-facet worker plugin in root/dir named "@test/<dir>".
func writeItemPlugin(t *testing.T, root, dir, label string) {
	t.Helper()
	writeFiles(t, root+"/"+dir, map[string]string{
		"package.json": `{"name":"@test/` + dir + `","type":"module"}`,
		"worker.ts":    `export const label = "` + label + `";`,
	})
}
