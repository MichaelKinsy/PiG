package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func printShutdownProbe(events *[]extension.SessionShutdownEvent, inputs *[]extension.InputEvent) extension.Extension {
	return extension.Extension{Path: "print-shutdown-probe", Handlers: map[string][]extension.HandlerFn{
		"session_shutdown": {func(args ...any) (any, error) {
			*events = append(*events, args[0].(extension.SessionShutdownEvent))
			return nil, nil
		}},
		"input": {func(args ...any) (any, error) {
			*inputs = append(*inputs, args[0].(extension.InputEvent))
			return nil, nil
		}},
	}}
}

func TestPrintModeShutdownUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, mode, prompt string
		images             []ai.ImageContent
		additional         bool
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/print-mode.test.ts:94
		{name: "emits session_shutdown in text mode", mode: "text", prompt: "Say done", images: []ai.ImageContent{{MimeType: "image/png", Data: "abc"}}},
		// .upstream/v0.87.1/packages/coding-agent/test/print-mode.test.ts:111
		{name: "emits session_shutdown in json mode", mode: "json", prompt: "hello", additional: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses(fauxSteps(fauxTextResponse("done")))
			host := printModeTestHost(t, provider)
			var shutdowns []extension.SessionShutdownEvent
			var inputs []extension.InputEvent
			host.Extensions = []extension.Extension{printShutdownProbe(&shutdowns, &inputs)}
			opts := printModeOptions{Mode: tc.mode, InitialMessage: tc.prompt, InitialImages: tc.images}
			if tc.additional {
				opts.InitialMessage = ""
				opts.Messages = []string{tc.prompt}
			}
			result := runPrintModeForTest(t, host, opts)
			if result.err != nil || result.stderr != "" {
				t.Fatalf("result=%+v", result)
			}
			var wantImages []extension.ImageContent
			for _, image := range tc.images {
				wantImages = append(wantImages, image)
			}
			if len(inputs) != 1 || inputs[0].Text != tc.prompt || !reflect.DeepEqual(inputs[0].Images, wantImages) {
				t.Fatalf("input=%+v, want %q images=%+v", inputs, tc.prompt, tc.images)
			}
			want := []extension.SessionShutdownEvent{{Type: "session_shutdown", Reason: "quit"}}
			if !reflect.DeepEqual(shutdowns, want) {
				t.Fatalf("shutdowns=%+v, want %+v", shutdowns, want)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/print-mode.test.ts:126
	t.Run("emits session_shutdown and returns non-zero on assistant error", func(t *testing.T) {
		provider := ai.NewFauxProvider(ai.FauxConfig{})
		provider.SetResponses(fauxSteps(ai.FauxResponse{StopReason: "error", ErrorMessage: "provider failure"}))
		host := printModeTestHost(t, provider)
		prior := runPrintModeForTest(t, host, printModeOptions{Mode: "text", InitialMessage: "seed error"})
		if !errors.Is(prior.err, errPrintModeHandled) || prior.stderr != "provider failure\n" {
			t.Fatalf("seed error=%+v", prior)
		}
		files, err := filepath.Glob(filepath.Join(host.Session.SessionDir, "*.jsonl"))
		if err != nil || len(files) != 1 {
			t.Fatalf("session files=%q err=%v", files, err)
		}
		host.ResumePath = files[0]
		var shutdowns []extension.SessionShutdownEvent
		var inputs []extension.InputEvent
		host.Extensions = []extension.Extension{printShutdownProbe(&shutdowns, &inputs)}
		result := runPrintModeForTest(t, host, printModeOptions{Mode: "text"})
		if !errors.Is(result.err, errPrintModeHandled) || result.stderr != "provider failure\n" || result.stdout != "" {
			t.Fatalf("result=%+v", result)
		}
		if len(inputs) != 0 {
			t.Fatalf("no-message mode sent a prompt: %+v", inputs)
		}
		want := []extension.SessionShutdownEvent{{Type: "session_shutdown", Reason: "quit"}}
		if !reflect.DeepEqual(shutdowns, want) {
			t.Fatalf("shutdowns=%+v, want %+v", shutdowns, want)
		}
	})
}
