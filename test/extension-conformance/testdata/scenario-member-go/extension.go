// Package member is the Go scenario member: a second extension of the SDK under test, loaded beside the scenario driver so that, when the scenario runs shared,
// both are members of one cell (one process). It registers one command and reads no Spec.
package member

import sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

// Extension is the conventional Go factory the host builds and runs.
func Extension() *sdk.Extension {
	extension := sdk.New("scenario-member-go")
	extension.Command("member-ping", "The cell member's command", func(sdk.Context, string) error { return nil })
	return extension
}
