package cli

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/coding/piglet"
	"github.com/MichaelKinsy/PiG/coding/pigletbuild/binarypiglet"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// exportActivePigletSource sets the variable the tools of this session inherit, or clears it, so a nested pig that starts no
// Piglet never reports its parent's. It runs after the Piglet resolves and before any tool starts.
//
// pig additive (D209): the active Piglet is exported to tool subprocesses as PIG_ACTIVE_PIGLET_SOURCE.
func exportActivePigletSource(p *piglet.Piglet, baked bool) {
	if value := piglet.ActiveSourceValue(p, baked); value != "" {
		_ = os.Setenv(piglet.ActiveSourceEnv, value)
		return
	}
	_ = os.Unsetenv(piglet.ActiveSourceEnv)
}

// inspectedPiglet is the Piglet a status or diagnose run reports on: the session's Piglet that the environment names, or this
// process's baked Piglet when it is a Piglet Binary.
type inspectedPiglet struct {
	// Source is the Piglet file path, or the Piglet Binary's name.
	Source string
	Name   string
	// Baked marks a Piglet Binary's Piglet.
	Baked bool
	// Open marks a Piglet Binary that accepts overlays (D202), whose Piglet discovery applies as for a Piglet file. The
	// discovery of any other Binary excludes every ambient extension and skill.
	Open bool
	// Piglet is nil when a Binary's Piglet is not this process's own or when the source could not load; Err says why.
	Piglet *piglet.Piglet
	Err    error
}

// inspectActivePiglet reports the active Piglet for the read-only commands, or nil when none is active.
//
// pig additive (D209): `pig status`, `pig diagnose` and `pig piglet show` read PIG_ACTIVE_PIGLET_SOURCE.
func inspectActivePiglet(cwd string) *inspectedPiglet {
	source := piglet.ActiveSourceFromEnv()
	if source == "" {
		if baked := binarypiglet.Default(); baked != nil {
			return &inspectedPiglet{Source: baked.Name, Name: baked.Name, Baked: true, Open: binarypiglet.OverlayBase() != nil, Piglet: baked}
		}
		return nil
	}
	if !piglet.ActiveSourceIsPath(source) {
		inspected := &inspectedPiglet{Source: source, Name: source, Baked: true}
		if baked := binarypiglet.Default(); baked != nil && baked.Name == source {
			inspected.Piglet, inspected.Open = baked, binarypiglet.OverlayBase() != nil
		} else {
			// Only the Binary itself knows whether it accepts overlays, and so which discovery applies.
			inspected.Err = fmt.Errorf("the active Piglet %q is a Piglet Binary and this pig is not that Binary, so status cannot apply its discovery", source)
		}
		return inspected
	}
	inspected := &inspectedPiglet{Source: source, Name: source}
	resolution, err := piglet.ResolveEffectiveWithOptions(source, piglet.ResolveOptions{Workspace: cwd})
	if err != nil {
		inspected.Err = fmt.Errorf("%s=%s did not load: %w", piglet.ActiveSourceEnv, source, err)
		return inspected
	}
	inspected.Piglet = resolution.Piglet
	inspected.Name = resolution.Piglet.Name
	return inspected
}

// ambientScopes returns the ambient scopes the Piglet allows for kind ("extensions" or "skills"); nil allows all.
func (i *inspectedPiglet) ambientScopes(kind string) *[]string {
	if i == nil || i.Err != nil {
		return nil
	}
	if i.Baked && !i.Open {
		empty := []string{}
		return &empty
	}
	return pigletAmbientSources(i.Piglet, kind)
}

// excludes reports whether the active Piglet's discovery leaves out an ambient resource of kind in scope.
func (i *inspectedPiglet) excludes(kind, scope string) bool {
	if kind != "extensions" && kind != "skills" {
		return false
	}
	return !packagemanager.PackageScopeEnabled(i.ambientScopes(kind), scope)
}
