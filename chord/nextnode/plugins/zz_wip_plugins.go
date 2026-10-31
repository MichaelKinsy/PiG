package plugins

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// WIP: stand-in for KPlugA's runtime and component types, dropped when the real ones are merged.

// PluginScript is one plugin's facet of a kind as shipped to a host (plain data): its build and its plugin record.
type PluginScript struct {
	Plugin Plugin
	Built  BuiltFacet
}

// Built is what a build made.
type Built struct {
	Facets  []BuiltFacet
	Plugins map[string]Plugin
	Removed []string
}

// RuntimeLoad is what PluginRuntime.Load takes besides the host.
type RuntimeLoad struct {
	Kind    string
	Core    []host.HostFacet
	Scripts []PluginScript
	Modules HostPackages
}

// PluginRuntime loads and reloads hosts with complete sets of shipped scripts.
type PluginRuntime interface {
	Load(h host.Host, options RuntimeLoad, ctx context.Context) (host.LoadReport, error)
	Reload(h host.Host, scripts []PluginScript, ctx context.Context) (host.ReloadReport, error)
}

type runtime struct{ evaluator Evaluator }

// NewPluginRuntime makes the plugin runtime over an Evaluator.
func NewPluginRuntime(evaluator Evaluator) PluginRuntime { return &runtime{evaluator} }

func (r *runtime) Load(h host.Host, options RuntimeLoad, ctx context.Context) (host.LoadReport, error) {
	facets := append([]host.HostFacet(nil), options.Core...)
	var report host.LoadReport
	for _, script := range options.Scripts {
		facet, err := r.evaluator.EvaluateFacet(script.Built, EvaluateOptions{Plugin: script.Plugin, HostPackages: options.Modules})
		if err != nil {
			report.Failed = append(report.Failed, host.PluginFailure{Plugin: script.Plugin.Name, Error: err})
			continue
		}
		facets = append(facets, host.HostFacet{Name: script.Plugin.Name, Facet: facet})
	}
	return report, h.Load(facets, ctx)
}

func (r *runtime) Reload(h host.Host, scripts []PluginScript, ctx context.Context) (host.ReloadReport, error) {
	panic("wip")
}

// RolloutHost is one host's result of a rollout step.
type RolloutHost struct {
	Host    string
	Report  *host.ReloadReport
	Stopped bool
	Err     error
}

// RolloutStep is what a rollout step does.
type RolloutStep struct {
	Name   string
	Kinds  []string
	Reload func(built Built, ctx context.Context) ([]RolloutHost, error)
}

// Plugins is the plugins component.
type Plugins interface {
	Scripts(kind string, ctx context.Context) ([]PluginScript, error)
	Current(kind string) ([]PluginScript, bool)
}
