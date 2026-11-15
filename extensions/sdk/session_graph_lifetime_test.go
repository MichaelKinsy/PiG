package sdk

import (
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"weak"
)

// A session graph object the extension no longer refers to is released by a cleanup (adoptGraphObject), and the host then refuses every call that
// names its handle: "Unknown or released session graph object". The handle is a string on the wire, so the host's view of an object ends when the
// Go value does, not when the last use of the handle does. A value that is only named (by a LoadedExtension, or by a call that has put its handle
// on the wire but has not been answered) must stay reachable: a collection at that point releases the handle before the host reads it, which is the
// failure the session graph rows saw once in a race pass (the loader of an extension_runner script, released between GetExtensions and the runner's
// construction). A weak pointer is cleared by the collection that finds its object unreachable, before the cleanup that releases the handle runs, so
// the check needs no wait: after runtime.GC, an object whose weak pointer is nil has been released.

// graphLifetimeProbe holds weak pointers to the objects a scenario must keep reachable.
type graphLifetimeProbe struct {
	t     *testing.T
	named []weak.Pointer[graphObject]
	names []string
}

// track records that object, which the scenario no longer holds in a variable, must be reachable when require runs.
func (p *graphLifetimeProbe) track(name string, object *graphObject) {
	p.named = append(p.named, weak.Make(object))
	p.names = append(p.names, name)
}

// require collects garbage and reports every tracked object that is gone.
func (p *graphLifetimeProbe) require(when string) {
	p.t.Helper()
	runtime.GC()
	runtime.GC()
	for i, w := range p.named {
		if w.Value() == nil {
			p.t.Errorf("%s: the %s object was collected, so its handle is released while it is still named", when, p.names[i])
		}
	}
}

// graphLifetimeCall is the kind and op of a sessionGraph.new call, or the method of a sessionGraph.call or sessionGraph.async call.
func graphLifetimeCall(call *callMsg) (kind, op, method, handle string) {
	var wire struct {
		Kind   string `json:"kind"`
		Op     string `json:"op"`
		Method string `json:"method"`
		Handle string `json:"handle"`
	}
	_ = json.Unmarshal(call.Args, &wire)
	return wire.Kind, wire.Op, wire.Method, wire.Handle
}

// graphLifetimeAnswer is a host that issues a fresh handle for every object and answers null to everything else, but a loader's extensions.
func graphLifetimeAnswer(call *callMsg, issued *int) *callResultMsg {
	_, _, method, _ := graphLifetimeCall(call)
	switch {
	case call.Method == graphNew:
		*issued++
		return &callResultMsg{Result: json.RawMessage(fmt.Sprintf(`{"handle":"h%d"}`, *issued))}
	case method == "getExtensions":
		return &callResultMsg{Result: json.RawMessage(`{"extensions":[{"path":"/w/a.mjs","resolvedPath":"/w/a.mjs","sourceInfo":{"path":"/w/a.mjs","source":"local","scope":"temporary","origin":"top-level"},"tools":[]}],"errors":[],"warnings":[]}`)}
	}
	return &callResultMsg{Result: json.RawMessage(`null`)}
}

func TestSessionGraphObjectsStayReachableWhileTheirHandleIsNamed(t *testing.T) {
	isNew := func(wantKind string) func(*callMsg) bool {
		return func(call *callMsg) bool {
			kind, _, _, _ := graphLifetimeCall(call)
			return call.Method == graphNew && kind == wantKind
		}
	}
	cases := []struct {
		name string
		// target is the host call whose answer must find every tracked object reachable; nil checks inside run.
		target func(*callMsg) bool
		run    func(c Context, p *graphLifetimeProbe) error
	}{
		{
			// The script of the extension_runner row: the loader is last used by GetExtensions, and the extensions it returned name it until the runner is built.
			name: "a loaded extension keeps its loader",
			run: func(c Context, p *graphLifetimeProbe) error {
				loader, err := NewDefaultResourceLoader(c, DefaultResourceLoaderOptions{Cwd: "/w"})
				if err != nil {
					return err
				}
				loaded, err := loader.GetExtensions()
				if err != nil {
					return err
				}
				p.track("loader", loader.object)
				loader = nil
				p.require("after GetExtensions")
				runtime.KeepAlive(loaded)
				return nil
			},
		},
		{
			name:   "the runner's construction names its loader, runtime, session manager and model registry",
			target: isNew("ExtensionRunner"),
			run: func(c Context, p *graphLifetimeProbe) error {
				loader, err := NewDefaultResourceLoader(c, DefaultResourceLoaderOptions{Cwd: "/w"})
				if err != nil {
					return err
				}
				loaded, err := loader.GetExtensions()
				if err != nil {
					return err
				}
				manager, err := SessionManagerInMemory(c, "/w", nil, nil)
				if err != nil {
					return err
				}
				models, err := ModelRuntimeCreate(c, ModelRuntimeCreateOptions{})
				if err != nil {
					return err
				}
				p.track("loader", loader.object)
				p.track("loaded runtime", loaded.Runtime.object)
				p.track("session manager", manager.object)
				p.track("model registry", models.object)
				loader = nil
				_, err = NewExtensionRunner(c, loaded.Extensions, loaded.Runtime, "/w", &manager, models)
				return err
			},
		},
		{
			name:   "the runtime of a loader's extensions names the loader",
			target: isNew("ExtensionRuntime"),
			run: func(c Context, p *graphLifetimeProbe) error {
				loader, err := NewDefaultResourceLoader(c, DefaultResourceLoaderOptions{Cwd: "/w"})
				if err != nil {
					return err
				}
				p.track("loader", loader.object)
				_, err = loader.GetExtensions()
				return err
			},
		},
		{
			name: "a method call names its receiver",
			target: func(call *callMsg) bool {
				_, _, method, _ := graphLifetimeCall(call)
				return call.Method == graphCall && method == "getTheme"
			},
			run: func(c Context, p *graphLifetimeProbe) error {
				settings, err := SettingsManagerInMemory(c, nil, nil)
				if err != nil {
					return err
				}
				p.track("settings manager", settings.object)
				_, err = settings.GetTheme()
				return err
			},
		},
		{
			name: "an async method call names its receiver",
			target: func(call *callMsg) bool {
				_, _, method, _ := graphLifetimeCall(call)
				return call.Method == graphAsync && method == "reload"
			},
			run: func(c Context, p *graphLifetimeProbe) error {
				settings, err := SettingsManagerInMemory(c, nil, nil)
				if err != nil {
					return err
				}
				p.track("settings manager", settings.object)
				return settings.Reload()
			},
		},
		{
			name:   "a resource loader's construction names its settings manager and runtime",
			target: isNew("DefaultResourceLoader"),
			run: func(c Context, p *graphLifetimeProbe) error {
				settings, err := SettingsManagerInMemory(c, nil, nil)
				if err != nil {
					return err
				}
				extensionRuntime, err := CreateExtensionRuntime(c)
				if err != nil {
					return err
				}
				p.track("settings manager", settings.object)
				p.track("runtime", extensionRuntime.object)
				_, err = NewDefaultResourceLoader(c, DefaultResourceLoaderOptions{Cwd: "/w", SettingsManager: &settings, Runtime: extensionRuntime})
				return err
			},
		},
		{
			name:   "a package manager's construction names its settings manager",
			target: isNew("DefaultPackageManager"),
			run: func(c Context, p *graphLifetimeProbe) error {
				settings, err := SettingsManagerInMemory(c, nil, nil)
				if err != nil {
					return err
				}
				p.track("settings manager", settings.object)
				_, err = NewDefaultPackageManager(c, PackageManagerOptions{Cwd: "/w", SettingsManager: settings})
				return err
			},
		},
		{
			// The stream's host call runs on its own goroutine after Stream returned, and lasts as long as the stream does.
			name:   "a model stream names its runtime",
			target: func(call *callMsg) bool { return call.Method == "modelStream" },
			run: func(c Context, p *graphLifetimeProbe) error {
				models, err := ModelRuntimeCreate(c, ModelRuntimeCreateOptions{})
				if err != nil {
					return err
				}
				p.track("model runtime", models.object)
				models.Complete(map[string]any{"provider": "p", "id": "m"}, map[string]any{}, nil)
				return nil
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext := New("graph-lifetime")
			probe := &graphLifetimeProbe{t: t}
			var scenarioErr error
			ext.Command("probe", "probe", func(ctx Context, _ string) error {
				scenarioErr = tc.run(ctx, probe)
				return nil
			})
			host, _, done := surfaceHost(t, ext, nil)
			defer surfaceShutdown(t, host, done)
			issued := 0
			runSurfaceCommand(t, host, "probe", func(call *callMsg) *callResultMsg {
				if tc.target != nil && tc.target(call) {
					probe.require("while " + call.Method + " was in flight")
				}
				return graphLifetimeAnswer(call, &issued)
			})
			if scenarioErr != nil {
				t.Fatal(scenarioErr)
			}
			if tc.target != nil && len(probe.named) == 0 {
				t.Fatal("the scenario tracked no object")
			}
		})
	}
}
