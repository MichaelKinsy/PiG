package cli

// pi: packages/coding-agent/src/modes/print-mode.ts

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

// Pi: packages/coding-agent/src/core/extensions/types.ts:1136 (InputEvent.images).
//
// mutation-checked: the mutant "runPrintMode never closes the runtime (no session_shutdown)" fails it.
// mutation-checked: the mutant "runPrintMode drops the initial images" fails it.
// mutation-checked: the mutant "runPrintMode drops the additional messages" fails it.
func TestPrintModeShutdownUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, mode, prompt string
		images             []ai.ImageContent
		additional         bool
	}{
		// .upstream/current/packages/coding-agent/test/print-mode.test.ts:94
		{name: "emits session_shutdown in text mode", mode: "text", prompt: "Say done", images: []ai.ImageContent{{MimeType: "image/png", Data: "abc"}}},
		// .upstream/current/packages/coding-agent/test/print-mode.test.ts:111
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
	// .upstream/current/packages/coding-agent/test/print-mode.test.ts:126
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

// .upstream/current/packages/coding-agent/test/print-mode.test.ts "warns on stderr and returns non-zero when the response was truncated" (#10799), and
// print-mode.ts: a final response with stopReason "length" prints its text to stdout, writes the warning to stderr and exits 1; stop, toolUse and the other reasons do not.
// A length stop below the model's output limit is recovered by the session first (agent-session.ts _checkCompaction), so a response that reaches print mode with that stop reason is one
// at the limit: the cases give the model a limit the scripted text reaches (the faux provider estimates the output tokens from the text), or no known limit.
//
// mutation-checked: the mutant "text mode ignores the length stop reason" fails it.
// mutation-checked: the mutant "text mode drops the partial text of a truncated response" fails it.
func TestPrintModeTruncatedResponseWarnsAndFailsUpstream(t *testing.T) {
	const warning = "Response was truncated: the model reached its output token limit.\n"
	for _, tc := range []struct {
		name, stopReason, text string
		wantTruncated          bool
		maxOutputTokens        int
	}{
		{name: "length at the output limit keeps the partial text and fails", stopReason: "length", text: "partial ans", wantTruncated: true, maxOutputTokens: 1},
		{name: "length with no text and no known limit still fails", stopReason: "length", wantTruncated: true},
		{name: "length of a model with no known limit keeps the text and fails", stopReason: "length", text: "cut off", wantTruncated: true},
		{name: "stop succeeds", stopReason: "stop", text: "full answer", maxOutputTokens: 8192},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			var blocks []ai.FauxContentBlock
			if tc.text != "" {
				blocks = append(blocks, ai.FauxText(tc.text))
			}
			step := ai.FauxStaticStep(ai.FauxResponse{Content: blocks, StopReason: tc.stopReason})
			provider.SetResponses([]ai.FauxResponseStep{step})
			host := printModeTestHost(t, provider)
			host.Session.Model.Capabilities.MaxOutputTokens = tc.maxOutputTokens
			result := runPrintModeForTest(t, host, printModeOptions{Mode: "text", InitialMessage: "go on"})
			wantStdout := ""
			if tc.text != "" {
				wantStdout = tc.text + "\n"
			}
			if result.stdout != wantStdout {
				t.Fatalf("stdout = %q, want %q", result.stdout, wantStdout)
			}
			if tc.wantTruncated {
				if !errors.Is(result.err, errPrintModeHandled) || result.stderr != warning {
					t.Fatalf("err = %v, stderr = %q; want errPrintModeHandled and the truncation warning", result.err, result.stderr)
				}
				return
			}
			if result.err != nil || result.stderr != "" {
				t.Fatalf("err = %v, stderr = %q; want a clean run", result.err, result.stderr)
			}
		})
	}
}
