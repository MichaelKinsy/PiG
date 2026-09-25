// Package fusepack holds Go extensions fused into a Piglet Binary. The build
// copies compatible extension source under exts/ and generates
// registry_generated.go with lazy factory serve functions. Register installs a
// FusedResolver so the host loads those extensions in-process (D31).
//
// Stock Pig compiles this package with an empty registry, so Register is a no-op.
package fusepack

import (
	"fmt"
	"net"
	"sort"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// fusedServes maps an extension name to its in-process serve func. Populated by
// generated code in registry_generated.go via registerFactory; empty in stock Pig.
var fusedServes = map[string]func(net.Conn) error{}

// registerFactory records one fused extension factory without constructing the
// extension. Construction is deferred until the host actually loads a session,
// so pre-session CLI commands and version/build operations have no extension
// initialization side effects.
func registerFactory(name string, factory sdk.Factory) {
	fusedServes[name] = func(conn net.Conn) (err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("construct fused extension %q: %v", name, recovered)
			}
			_ = conn.Close()
		}()
		ext := factory()
		if ext == nil {
			return fmt.Errorf("construct fused extension %q: factory returned nil", name)
		}
		return ext.RunWithConn(conn)
	}
}

type resolver struct{}

func (resolver) FusedServe(cfg subprocess.ExtConfig) (func(net.Conn) error, bool) {
	serve, ok := fusedServes[cfg.Name]
	return serve, ok
}

// Register installs the fused-extension resolver if this Piglet Binary compiled any in.
// No-op in stock pig (empty map), so the subprocess cell path is unaffected.
func Register() {
	if len(fusedServes) == 0 {
		return
	}
	subprocess.SetFusedResolver(resolver{})
}

// FusedConfigs returns a name-only ExtConfig for each fused extension so the
// startup loader includes them even though their source is not on this machine.
// LoadAll routes these to LoadInProcess (no source needed). A richer piglet or
// CLI config for the same name still wins the config merge. Empty in stock pig.
func FusedConfigs() []subprocess.ExtConfig {
	names := make([]string, 0, len(fusedServes))
	for name := range fusedServes {
		names = append(names, name)
	}
	sort.Strings(names)
	cfgs := make([]subprocess.ExtConfig, 0, len(names))
	for _, name := range names {
		cfgs = append(cfgs, subprocess.ExtConfig{Name: name, Enabled: true})
	}
	return cfgs
}
