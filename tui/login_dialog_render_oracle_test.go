package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type ldRenderLink struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
}

type ldRenderStep struct {
	Op          string         `json:"op"`
	Lines       []string       `json:"lines"`
	Message     string         `json:"message"`
	Links       []ldRenderLink `json:"links"`
	CloseHint   bool           `json:"closeHint"`
	URI         string         `json:"uri,omitempty"`
	Code        string         `json:"code,omitempty"`
	Placeholder string         `json:"placeholder,omitempty"`
	Keys        string         `json:"keys,omitempty"`
	Width       int            `json:"width"`
}

type ldRenderProbe struct {
	Theme        string         `json:"theme"`
	ProviderID   string         `json:"providerId"`
	ProviderName string         `json:"providerName"`
	Title        string         `json:"title"`
	Steps        []ldRenderStep `json:"steps"`
}

type ldRenderFrame struct {
	Events []string `json:"events"`
	Rows   []string `json:"rows"`
}

func ldRenderProbes() []ldRenderProbe {
	rng := rand.New(rand.NewPCG(8, 40))
	pick := func(items []string) string { return items[rng.IntN(len(items))] }
	messages := []string{"Open the page", "Waiting for authorization...", "日本語のメッセージ", "A considerably longer message that has to wrap at the narrow widths the probes use", "", "x"}
	keys := []string{"a", "b", "x", "日", "\x7f", "\x1b[D", "\x1b[C", "\x01", "\x05", "\r", "\x1b", "\x03", "paste me", "\x15", "\x0b"}
	widths := []int{12, 20, 33, 40, 60, 80, 120}
	var probes []ldRenderProbe
	for range 500 {
		probe := ldRenderProbe{Theme: pick([]string{"dark", "light"}), ProviderID: pick([]string{"anthropic", "openai-codex", "github-copilot", "x"}), Steps: []ldRenderStep{}}
		if rng.IntN(2) == 0 {
			probe.ProviderName = pick([]string{"Claude Pro/Max", "日本語", "Provider"})
		}
		if rng.IntN(4) == 0 {
			probe.Title = pick([]string{"Custom title", "", "日本語 title"})
		}
		// A prompt may be shown while an earlier one is unanswered, after a cancel, or after the input was submitted: Pi renders the one live input at every prompt position until a submit replaces them.
		for range 1 + rng.IntN(10) {
			step := ldRenderStep{Width: widths[rng.IntN(len(widths))], Lines: []string{}, Links: []ldRenderLink{}}
			switch rng.IntN(9) {
			case 0:
				step.Op = "details"
				for range rng.IntN(4) {
					step.Lines = append(step.Lines, pick(messages))
				}
			case 1:
				step.Op = "info"
				step.Message = pick(messages)
				for range rng.IntN(3) {
					step.Links = append(step.Links, ldRenderLink{URL: pick([]string{"https://example.test/a", "https://example.test/a/very/long/path/that/wraps?x=1&y=2"}), Label: pick([]string{"", "Docs", "日本語"})})
				}
				step.CloseHint = rng.IntN(2) == 0
			case 2:
				step.Op = "waiting"
				step.Message = pick(messages)
			case 3:
				step.Op = "progress"
				step.Message = pick(messages)
			case 4:
				step.Op = "deviceCode"
				step.URI = pick([]string{"https://example.test/device", "https://example.test/a/very/long/verification/path/that/wraps"})
				step.Code = pick([]string{"ABCD-1234", "日本語"})
			case 5, 6:
				step.Op = pick([]string{"manual", "prompt"})
				step.Message = pick(messages)
				if step.Op == "prompt" {
					step.Placeholder = pick([]string{"", "sk-...", "日本語"})
				}
			default:
				step.Op = "keys"
				step.Keys = pick(keys)
			}
			probe.Steps = append(probe.Steps, step)
		}
		probes = append(probes, probe)
	}
	return probes
}

func runLdRenderWithPig(probe ldRenderProbe) []ldRenderFrame {
	SetCapabilities(TerminalCapabilities{TrueColor: true, Hyperlinks: true})
	SetTheme(probe.Theme)
	SetKeybindings(NewKeybindingsManager(TUIKeybindingDefinitionsFor(HostKeybindingPlatform()), nil))
	var eventsMu sync.Mutex
	var events []string
	record := func(event string) {
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
	}
	var titles []string
	if probe.Title != "" {
		titles = append(titles, probe.Title)
	}
	dialog := NewLoginDialogComponent(nil, probe.ProviderID, func(success bool, message string) {
		record(fmt.Sprintf("complete:%v:%s", success, message))
	}, probe.ProviderName, titles...)
	dialog.SetMaskSecretInput(false)
	focus := func(dialog *LoginDialogComponent) { dialog.SetFocused(true) }
	focus(dialog)
	var frames []ldRenderFrame
	// An input the probe leaves pending is never answered, so the goroutines watching them end with the probe.
	probeDone := make(chan struct{})
	defer close(probeDone)
	watch := func(label string, ch <-chan string) {
		go func() {
			var value string
			var ok bool
			select {
			case value, ok = <-ch:
			case <-probeDone:
				return
			}
			if ok {
				record(fmt.Sprintf("%s:resolved:%s", label, value))
			} else {
				record(fmt.Sprintf("%s:rejected:Login cancelled", label))
			}
		}()
	}
	for _, step := range probe.Steps {
		eventsMu.Lock()
		events = nil
		eventsMu.Unlock()
		switch step.Op {
		case "details":
			dialog.ShowDetails(step.Lines)
		case "info":
			links := make([]AuthInfoLink, len(step.Links))
			for i, link := range step.Links {
				links[i] = AuthInfoLink(link)
			}
			dialog.ShowInfo(step.Message, links, step.CloseHint)
		case "waiting":
			dialog.ShowWaiting(step.Message)
		case "progress":
			dialog.ShowProgress(step.Message)
		case "deviceCode":
			dialog.ShowDeviceCode(OAuthDeviceCodeInfo{VerificationURI: step.URI, UserCode: step.Code})
		case "manual":
			watch("manual", dialog.ShowManualInput(step.Message))
		case "prompt":
			watch("prompt", dialog.ShowPrompt(step.Message, step.Placeholder))
		case "keys":
			dialog.HandleInput(step.Keys)
		}
		time.Sleep(2 * time.Millisecond)
		eventsMu.Lock()
		observed := append([]string{}, events...)
		eventsMu.Unlock()
		frames = append(frames, ldRenderFrame{Events: observed, Rows: dialog.Render(step.Width)})
	}
	return frames
}

// LoginDialogComponent (login-dialog.ts) against pinned Pi over 500 seeded flows: details, provider info with links and the close hint, waiting and progress lines, the device code, the manual-code and ordinary prompts with a placeholder, typing, submit and cancel, in both themes at widths 12 to 120 with and without a custom title and provider name; the rows after every step and the completion and prompt results must agree.
func TestLoginDialogRenderMatchesPi(t *testing.T) {
	probes := ldRenderProbes()
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/login_dialog_render.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected [][]ldRenderFrame
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	previousCaps, previousKeys := GetCapabilities(), GetKeybindings()
	t.Cleanup(func() {
		SetCapabilities(previousCaps)
		SetKeybindings(previousKeys)
	})
	failures := 0
	for i, probe := range probes {
		got := runLdRenderWithPig(probe)
		raw, _ := json.Marshal(got)
		var normalized []ldRenderFrame
		_ = json.Unmarshal(raw, &normalized)
		if reflect.DeepEqual(normalized, expected[i]) {
			continue
		}
		if failures++; failures > 5 {
			continue
		}
		for k := range probe.Steps {
			if !reflect.DeepEqual(normalized[k], expected[i][k]) {
				steps, _ := json.Marshal(probe.Steps[:k+1])
				g, _ := json.Marshal(normalized[k])
				e, _ := json.Marshal(expected[i][k])
				t.Errorf("probe %d (%s %q %q)\n  steps %.900s\n  Pig %.900s\n  Pi  %.900s", i, probe.Theme, probe.ProviderName, probe.Title, steps, g, e)
				break
			}
		}
	}
	if failures > 5 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
