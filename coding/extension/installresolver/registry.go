// Package installresolver is Pig's product-neutral seam for contributed
// package-source behavior. Optional catalog/product packages register through
// it so generic Pig packages (for example the piglet schema package) and the
// plugin-update path do not import a concrete product package.
//
// The registry carries product-neutral piglet-origin, materialization, and
// package-reinstall callbacks.
//
// pig additive (D18): product-neutral install and Piglet source resolver seams.
package installresolver

import (
	"fmt"
	"io"
	"strings"
	"sync"
)

// PigletSourceResolver acquires one typed Piglet source. The resolver is
// registered by source scheme and receives the opaque locator after the colon.
type PigletSourceResolver func(cwd, locator string) (root string, err error)

// Materializer resolves one source to a local root without adding it to Package
// settings or activating any resources. Piglets use this path for dependencies.
type Materializer func(cwd, source, scope string, stdout, stderr io.Writer) (root string, err error)

// Installer reinstalls one recorded package source through the core install
// pipeline.
type Installer func(cwd, source, scope string, stdout, stderr io.Writer) error

var registry struct {
	sync.RWMutex
	pigletSource  map[string]PigletSourceResolver
	sourceSchemes map[string]struct{}
	installer     Installer
	materializer  Materializer
}

// RegisterSourceScheme claims one explicit package/piglet source scheme.
// Duplicate claims are rejected so a source never depends on import order.
func RegisterSourceScheme(scheme string) error {
	scheme = strings.TrimSpace(scheme)
	if scheme == "" {
		return fmt.Errorf("source scheme is required")
	}
	for i, r := range scheme {
		if (i == 0 && (r < 'a' || r > 'z')) || (i > 0 && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-') {
			return fmt.Errorf("invalid source scheme %q", scheme)
		}
	}
	registry.Lock()
	defer registry.Unlock()
	if _, exists := registry.sourceSchemes[scheme]; exists {
		return fmt.Errorf("source scheme %q already registered", scheme)
	}
	if registry.sourceSchemes == nil {
		registry.sourceSchemes = map[string]struct{}{}
	}
	registry.sourceSchemes[scheme] = struct{}{}
	return nil
}

// SupportsSourceScheme reports whether a contributed resolver claimed scheme.
func SupportsSourceScheme(scheme string) bool {
	registry.RLock()
	defer registry.RUnlock()
	_, ok := registry.sourceSchemes[scheme]
	return ok
}

// RegisterPigletSourceResolver registers one exact typed source scheme.
func RegisterPigletSourceResolver(scheme string, resolver PigletSourceResolver) error {
	scheme = strings.TrimSpace(scheme)
	if resolver == nil {
		return fmt.Errorf("Piglet source resolver %q is nil", scheme)
	}
	registry.Lock()
	defer registry.Unlock()
	if _, supported := registry.sourceSchemes[scheme]; !supported {
		return fmt.Errorf("Piglet source scheme %q is not registered", scheme)
	}
	if registry.pigletSource == nil {
		registry.pigletSource = map[string]PigletSourceResolver{}
	}
	if _, exists := registry.pigletSource[scheme]; exists {
		return fmt.Errorf("Piglet source resolver %q is already registered", scheme)
	}
	registry.pigletSource[scheme] = resolver
	return nil
}

// ResolvePigletSource dispatches one typed source to its exact scheme owner.
func ResolvePigletSource(cwd, source string) (string, error) {
	scheme, locator, ok := strings.Cut(strings.TrimSpace(source), ":")
	if !ok || scheme == "" || locator == "" {
		return "", fmt.Errorf("Piglet source %q must use an explicit registered scheme", source)
	}
	registry.RLock()
	resolver := registry.pigletSource[scheme]
	registry.RUnlock()
	if resolver == nil {
		return "", fmt.Errorf("Piglet source scheme %q has no installed resolver", scheme)
	}
	return resolver(cwd, locator)
}

// SetMaterializer supplies the side-effect-free package source materializer.
func SetMaterializer(materializer Materializer) {
	registry.Lock()
	defer registry.Unlock()
	registry.materializer = materializer
}

// Materialize resolves one source without mutating Package settings.
func Materialize(cwd, source, scope string, stdout, stderr io.Writer) (string, error) {
	registry.RLock()
	materializer := registry.materializer
	registry.RUnlock()
	if materializer == nil {
		return "", fmt.Errorf("package materializer callback is not registered")
	}
	return materializer(cwd, source, scope, stdout, stderr)
}

// SetInstaller supplies the core package installer callback used when a
// contributed command needs to reinstall one of its recorded sources.
func SetInstaller(installer Installer) {
	registry.Lock()
	defer registry.Unlock()
	registry.installer = installer
}

// Install reinstalls a package source through the registered core installer.
func Install(cwd, source, scope string, stdout, stderr io.Writer) error {
	registry.RLock()
	installer := registry.installer
	registry.RUnlock()
	if installer == nil {
		return fmt.Errorf("package installer callback is not registered")
	}
	return installer(cwd, source, scope, stdout, stderr)
}
