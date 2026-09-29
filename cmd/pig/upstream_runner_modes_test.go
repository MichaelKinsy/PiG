package main

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi print-mode.ts binds UI and mode independently before dispatching input.
func TestPrintAndJSONBindRunnerModeBeforeInput(t *testing.T) {
	for _, tc := range []struct {
		option string
		want   extension.ExtensionMode
	}{{"text", extension.ModePrint}, {"json", extension.ModeJSON}} {
		t.Run(tc.option, func(t *testing.T) {
			host := printModeTestHost(t, ai.NewFauxProvider(ai.FauxConfig{}))
			var got extension.ExtensionMode
			var hasUI bool
			var modeErr, uiErr error
			host.Extensions = []extension.Extension{{Path: "mode", Handlers: map[string][]extension.HandlerFn{"input": {func(args ...any) (any, error) {
				ctx := extension.FromContext(args[1].(context.Context))
				got, modeErr = ctx.Mode()
				hasUI, uiErr = ctx.HasUI()
				return extension.InputEventResultHandled{}, nil
			}}}}}
			result := runPrintModeForTest(t, host, printModeOptions{Mode: tc.option, InitialMessage: "probe"})
			if result.err != nil || result.stderr != "" || modeErr != nil || uiErr != nil {
				t.Fatalf("run=%+v modeError=%v uiError=%v", result, modeErr, uiErr)
			}
			if got != tc.want || hasUI {
				t.Fatalf("mode=%q hasUI=%v; want %q false", got, hasUI, tc.want)
			}
		})
	}
}
