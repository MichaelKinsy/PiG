//go:build pig_strip_extension_sdk_go

package cli

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/upgrade"
)

// pig additive (D92): a Piglet Binary that compiled out the embedded Go SDK has no staged SDK to build the analysis driver from (extension_upgrade_analyze.go), so it cannot type check a Go extension against it.
func (u *extensionUpgrader) analyzeExtension(context.Context, subprocess.ExtConfig) (*upgrade.Result, error) {
	return nil, errors.New("this Piglet Binary compiled out the Go extension SDK, so it cannot analyze a Go extension for an upgrade")
}

func (u *extensionUpgrader) checkRewrite(context.Context, subprocess.ExtConfig, *upgrade.Result) (rewriteCheck, error) {
	return rewriteCheck{}, errors.New("this Piglet Binary compiled out the Go extension SDK, so it cannot build a Go extension for an upgrade")
}
