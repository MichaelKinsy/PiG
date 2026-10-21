package testfixture

import (
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// headlessComponent records that its Render ran. Pi never runs a custom component outside the TUI (rpc-mode.ts custom(), runner.ts noOpUIContext).
type headlessComponent struct{ mark func(string) }

func (c headlessComponent) Render(int) []string {
	c.mark("go:custom")
	return []string{"go:custom"}
}

// HandleInput marks too: the TUI harness answers the overlay with a key as soon as it opens, which can close it before the first render.
func (c headlessComponent) HandleInput(string) (sdk.RemoteComponentResult, error) {
	c.mark("go:custom")
	return sdk.RemoteComponentResult{Done: true, Value: "closed"}, nil
}

// registerHeadlessUIProbe adds the command the TestConformance_HeadlessModesNeverRunUIClosures row drives. Its argument is a file; every closure
// the probe hands the SDK appends a marker line naming this SDK when it runs, so an SDK that ignores the call cannot look clean.
func registerHeadlessUIProbe(ext *sdk.Extension) {
	ext.Command("headless-ui-probe", "Hand the SDK footer, header and custom component closures", func(ctx sdk.Context, path string) error {
		mark := func(line string) {
			file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return
			}
			defer func() { _ = file.Close() }()
			_, _ = file.WriteString(line + "\n")
		}
		if err := ctx.SetFooterRenderer(func(int) []string {
			mark("go:footer")
			return []string{"go:footer"}
		}); err != nil {
			return err
		}
		if err := ctx.SetHeaderRenderer(func(int) []string {
			mark("go:header")
			return []string{"go:header"}
		}); err != nil {
			return err
		}
		if _, err := ctx.Custom(headlessComponent{mark: mark}, nil); err != nil {
			return err
		}
		// A factory the SDK cannot carry: Pi's RPC and no-op UIs resolve undefined for it, the TUI SDK reports it unsupported.
		if value, err := ctx.Custom(struct{}{}, nil); err != nil {
			mark("go:custom-opaque:error")
		} else if value != nil {
			mark("go:custom-opaque:value")
		} else {
			mark("go:custom-opaque:undefined")
		}
		return nil
	})
}
