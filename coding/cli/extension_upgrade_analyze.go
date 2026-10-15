//go:build !pig_strip_extension_sdk_go

package cli

import (
	"context"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// analyzeExtension type checks the extension the way its build sees it and plans the rewrites.
func (u *extensionUpgrader) analyzeExtension(ctx context.Context, config subprocess.ExtConfig) (*upgrade.Result, error) {
	var result *upgrade.Result
	plan := func(options upgrade.LoadOptions) error {
		session, err := upgrade.Open(ctx, options)
		if err != nil {
			return err
		}
		result, err = session.Plan()
		return err
	}
	roots := append([]string{config.Source}, config.GoWorkspaceModules...)
	if isGoFactoryExtension(config) {
		err := runtimecell.WithGoBuildModule(ctx, goExtensionOf(config), u.sdkDir(), func(module runtimecell.GoBuildModule) error {
			return plan(upgrade.LoadOptions{Dir: module.Dir, Roots: roots, Patterns: []string{"."}, Command: module.Command, Env: module.Env})
		})
		return result, err
	}
	builder := subprocess.NewBuilderWithConfigRoot(filepath.Join(u.cacheRoot(), "ext"), u.configRoot)
	err := builder.WithGoListing(config.Source, func(listing subprocess.GoListing) error {
		return plan(upgrade.LoadOptions{Dir: config.Source, Roots: roots, Flags: listing.Flags, Command: listing.Command, Env: listing.Env})
	})
	return result, err
}
