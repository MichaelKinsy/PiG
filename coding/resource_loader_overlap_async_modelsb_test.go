package coding

// pi: packages/coding-agent/src/core/resource-loader.ts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// reloadFixture is a loader whose skills come from the settings' skill directory and whose system prompt from agentDir/SYSTEM.md, so a test changes
// the reload's inputs between two reloads. hook runs inside every extension load pass.
type reloadFixture struct {
	loader *DefaultResourceLoader
	root   string
	agent  string
}

func newReloadFixture(t *testing.T, hook func(context.Context, ExtensionLoadRequest) error) *reloadFixture {
	t.Helper()
	return newReloadFixtureWith(t, hook, nil)
}

// newReloadFixtureWith is newReloadFixture whose loader options configure may change before the loader is built.
func newReloadFixtureWith(t *testing.T, hook func(context.Context, ExtensionLoadRequest) error, configure func(*DefaultResourceLoaderOptions)) *reloadFixture {
	t.Helper()
	root := t.TempDir()
	f := &reloadFixture{root: root, agent: filepath.Join(root, "agent")}
	for _, dir := range []string{"project", "agent"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"x", "y"} {
		dir := filepath.Join(root, "skills-"+name, "skill-"+name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: skill-" + name + "\ndescription: skill " + name + "\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	options := DefaultResourceLoaderOptions{
		CWD: filepath.Join(root, "project"), AgentDir: f.agent, NoContextFiles: true,
		LoadExtensions: func(ctx context.Context, request ExtensionLoadRequest) (LoadExtensionsResult, error) {
			if hook != nil {
				if err := hook(ctx, request); err != nil {
					return LoadExtensionsResult{}, err
				}
			}
			return LoadExtensionsResult{Extensions: []extension.Extension{}}, nil
		},
	}
	if configure != nil {
		configure(&options)
	}
	f.loader = NewDefaultResourceLoader(options)
	// A reload that never returns holds the operation lock, so a Close behind it would hang the package instead of failing the test.
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() { f.loader.Close(); close(closed) }()
		select {
		case <-closed:
		case <-time.After(time.Second):
		}
	})
	f.setInputs(t, "x", "one")
	return f
}

// setInputs points the settings at skills-<skill> and writes the system prompt.
func (f *reloadFixture) setInputs(t *testing.T, skill, system string) {
	t.Helper()
	settings := `{"skills": [` + strconv.Quote(filepath.Join(f.root, "skills-"+skill)) + `]}`
	if err := os.WriteFile(filepath.Join(f.agent, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.agent, "SYSTEM.md"), []byte(system), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *reloadFixture) state() string {
	var names []string
	for _, skill := range f.loader.GetSkills().Skills {
		names = append(names, skill.Name)
	}
	system, _ := f.loader.GetSystemPrompt()
	return fmt.Sprintf("skills=%v system=%q", names, system)
}

// resource-loader.ts:541-710 has no lock: a reload suspended at an await (:618) lets a later reload run and finish, and each assigns its results when
// it reaches the end. PiG serialises the reloads (opMu), each atomic: the second starts only after the first finished, so inputs changed during the
// first are in the final state, whereas Pi's first reload would finish last with the skill paths it resolved before the await (:585-590) next to a system
// prompt read after it (:689).
func TestDefaultResourceLoaderOverlappingReloadsEndInTheLastInputsState(t *testing.T) {
	var calls atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	f := newReloadFixture(t, func(_ context.Context, request ExtensionLoadRequest) error {
		if request.Bootstrap {
			return nil
		}
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- f.loader.Reload() }()
	<-entered
	f.setInputs(t, "y", "two")
	go func() { second <- f.loader.Reload() }()
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("the second reload reached its extension load (%d loads) while the first was suspended in its own", n)
	}
	releaseOnce()
	for i, ch := range []chan error{first, second} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatalf("reload %d: %v", i+1, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("reload %d did not finish", i+1)
		}
	}
	if got, want := f.state(), `skills=[skill-y] system="two"`; got != want {
		t.Fatalf("final state = %s, want %s", got, want)
	}
}

// Overlapping reloads with unchanged inputs end in the state one reload gives, with concurrent readers; run under -race.
func TestDefaultResourceLoaderManyOverlappingReloadsEndInTheSameState(t *testing.T) {
	f := newReloadFixture(t, nil)
	f.setInputs(t, "y", "last")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 2 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					_ = f.state()
				}
			}
		})
	}
	for range 8 {
		wg.Go(func() { errs <- f.loader.Reload() })
	}
	wg.Wait()
	close(stop)
	readers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got, want := f.state(), `skills=[skill-y] system="last"`; got != want {
		t.Fatalf("final state = %s, want %s", got, want)
	}
}

// A callback of a running reload that calls Reload with the context it was given returns: in Pi the nested reload() interleaves at its awaits and
// resolves, in PiG the operation lock the outer reload holds must not make the nested call wait for it.
func TestDefaultResourceLoaderCallbackMayReloadWithItsContext(t *testing.T) {
	for _, where := range []string{"trust callback", "bootstrap extension load", "final extension load"} {
		t.Run(where, func(t *testing.T) {
			var nested atomic.Int32
			var f *reloadFixture
			reenter := func(ctx context.Context) error {
				if nested.Add(1) != 1 {
					return nil
				}
				return f.loader.ReloadWith(ctx)
			}
			var hookErr atomic.Value
			f = newReloadFixture(t, func(ctx context.Context, request ExtensionLoadRequest) error {
				if (where == "bootstrap extension load" && request.Bootstrap) || (where == "final extension load" && !request.Bootstrap) {
					if err := reenter(ctx); err != nil {
						hookErr.Store(err)
					}
				}
				return nil
			})
			done := make(chan error, 1)
			var callbackErr atomic.Value
			go func() {
				done <- f.loader.ReloadWith(t.Context(), ResourceLoaderReloadOptions{ResolveProjectTrust: func(ctx context.Context, _ ResolveProjectTrustInput) (bool, error) {
					if where == "trust callback" {
						if err := reenter(ctx); err != nil {
							callbackErr.Store(err)
						}
					}
					return false, nil
				}})
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("outer reload: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("a %s that reloads with its context never returned: the nested reload waits for the outer one", where)
			}
			if err, _ := hookErr.Load().(error); err != nil {
				t.Fatalf("nested reload: %v", err)
			}
			if err, _ := callbackErr.Load().(error); err != nil {
				t.Fatalf("nested reload: %v", err)
			}
			if nested.Load() == 0 {
				t.Fatal("the callback never ran")
			}
			if got, want := f.state(), `skills=[skill-x] system="one"`; got != want {
				t.Fatalf("final state = %s, want %s", got, want)
			}
		})
	}
}
