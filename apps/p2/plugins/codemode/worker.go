// Package codemode is p2's code mode plugin (apps/p2/plugins/codemode): the `codemode` tool runs model-written JavaScript
// that calls the conversation's other tools as nested durable calls (`await tools.read({ path })`), and survives worker
// restarts by replaying its journal. Durable's code mode extension is the one PiG ships (durable/codemode).
package codemode

import (
	"context"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/codemode/contract"
	modelscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/models/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/durable"
	dcodemode "github.com/MichaelKinsy/PiG/durable/codemode"
)

// Ports apps/p2/plugins/codemode/worker.ts.

// lazySandbox is the sandbox the tool reads when a script starts: a running script keeps the sandbox it started on.
type lazySandbox struct{ native host.Native[any] }

func (s lazySandbox) Start(ctx context.Context, code string, options cm.RunOptions) (*cm.Run, error) {
	value, err := s.native.Value()
	if err != nil {
		return nil, err
	}
	return value.(*cm.Sandbox).Start(ctx, code, options)
}

func serveRuntime(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
	harnessOf := p2runtime.HarnessOf(self)
	worker := p2runtime.WorkerOf(self)
	// Natives close in reverse order: running calls first, then the sandbox, which ends VMs and worker threads. Only a
	// removal (the object ends) cancels running calls; after a worker stop Durable replays them in the next worker, and a
	// reload drains them: the old sandbox closes once its scripts finished, the new one opens at once.
	calls := newRunningCalls()
	sandbox := self.Open(
		func(context.Context) (any, error) {
			return cm.NewSandbox(cm.SandboxOptions{MemoryLimitBytes: contract.MemoryLimitBytes})
		},
		func(value any, reason host.NativeCloseReason, _ context.Context) error {
			opened := value.(*cm.Sandbox)
			if reason.Kind == host.NativeCloseReload {
				go func() {
					<-calls.idle()
					_ = opened.Close()
				}()
				return nil
			}
			return opened.Close()
		},
	)
	self.Open(
		func(context.Context) (any, error) { return calls, nil },
		func(value any, reason host.NativeCloseReason, ctx context.Context) error {
			if reason.Kind != host.NativeCloseEnd {
				return nil
			}
			parts, err := worker()
			if err != nil || parts.Liveness.Stopping() {
				return nil
			}
			harnessParts, err := harnessOf()
			if err != nil {
				return nil
			}
			session, err := harnessParts.Durable()
			if err != nil {
				return nil
			}
			value.(*runningCalls).cancel(ctx, session)
			return nil
		},
	)
	// The tool reads the sandbox when a script starts: a running script keeps the sandbox it started on.
	extension := dcodemode.CreateExtension(dcodemode.ToolOptions{Sandbox: lazySandbox{sandbox}})
	tracked := &p2runtime.P2Extension{Extension: extension}
	for _, tool := range extension.Tools {
		copied := *tool
		execute := tool.Execute
		copied.Execute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return calls.track(api, func() (durable.ToolExecutionResult, error) { return execute(ctx, args, api) })
		}
		tracked.Tools = append(tracked.Tools, p2runtime.P2Tool{ToolRegistration: &copied})
	}
	// The `codemode` setting (default on in p2) installs and uninstalls the extension live, plugin loaded or not. A script
	// already running keeps its tool; the next request no longer offers it.
	settings := self.Get(modelscontract.WorkerModels, delta.NewJsonObject(0))
	switches := self.Writable(p2runtime.Keyed[*bool]{p2runtime.Entry(extension.Name, boolPointer(true))})
	self.On(settings.Member("settings"), func(call chord.WatchCall[delta.JsonValue], _ host.Ctx) *host.Handled {
		on := true
		if object, ok := call.Value.(*delta.JsonObject); ok {
			on = object.Value("codemode") != false
		}
		_ = switches.Set(p2runtime.Keyed[*bool]{p2runtime.Entry(extension.Name, boolPointer(on))})
		return nil
	})
	self.(host.StoreSelf).Contribute(p2runtime.ExtensionSwitches.Registry, switches)
	self.(host.StoreSelf).Contribute(p2runtime.Extensions.Registry, p2runtime.Keyed[*p2runtime.P2Extension]{p2runtime.Entry(extension.Name, tracked)})
	return contracts.Implementation{}, nil
}

func boolPointer(value bool) *bool { return &value }

// NewFacet is a new value of the plugin's worker facet (a reload with it replaces what the plugin contributed).
func NewFacet() host.Facet {
	return host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		return facet.Serve(contract.CodemodeRuntime, host.LocalFactory(serveRuntime), host.ServeOptions{})
	})
}

func init() {
	workerFacet = NewFacet()
	hosts.RegisterPlugin(hosts.Plugin{Name: "codemode", Worker: &workerFacet})
}

// workerFacet is the plugin's worker facet, set at init.
var workerFacet host.Facet

// Facet is the plugin's worker facet.
func Facet() host.Facet { return workerFacet }
