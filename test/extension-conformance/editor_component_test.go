package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// baseEditorHost stands in for the host's stock editor behind a delegated editor component: it holds text, answers the base operations a component's `super` calls, and records the component's output. The stock editor itself is covered by internal/codingagent TestDelegatedEditorBaseIsTheHostsStockEditor.
type baseEditorHost struct {
	mu      sync.Mutex
	text    string
	frames  [][]string
	inputs  chan struct{}
	baseOps []string
	// textArgs records the raw text argument of each text operation, by op.
	textArgs map[string][]string
	optIns   int
}

func (h *baseEditorHost) EditorEmbedWorkingStatusChanged() {
	h.mu.Lock()
	h.optIns++
	h.mu.Unlock()
}

func (h *baseEditorHost) EditorFrame(lines []string, _ int, _ bool) {
	h.mu.Lock()
	h.frames = append(h.frames, slices.Clone(lines))
	h.mu.Unlock()
}
func (h *baseEditorHost) EditorChanged(string, string)              {}
func (h *baseEditorHost) EditorSubmit(_ string, done func())        { done() }
func (h *baseEditorHost) EditorAction(extension.RemoteEditorAction) {}
func (h *baseEditorHost) EditorInputDone()                          { h.inputs <- struct{}{} }
func (h *baseEditorHost) EditorShortcut(string)                     {}
func (h *baseEditorHost) TerminalWrite(string)                      {}
func (h *baseEditorHost) SetShowHardwareCursor(bool)                {}
func (h *baseEditorHost) EditorClosed()                             {}

func (h *baseEditorHost) EditorBase(_ context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	var p struct {
		Data  string `json:"data"`
		Text  string `json:"text"`
		Width int    `json:"width"`
	}
	_ = sdkjson.Unmarshal(args, &p)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.baseOps = append(h.baseOps, op)
	if op == "setText" || op == "insertTextAtCursor" || op == "addToHistory" {
		var raw struct {
			Text json.RawMessage `json:"text"`
		}
		_ = json.Unmarshal(args, &raw)
		if h.textArgs == nil {
			h.textArgs = map[string][]string{}
		}
		h.textArgs[op] = append(h.textArgs[op], string(raw.Text))
	}
	switch op {
	case "handleInput":
		h.text += p.Data
	case "setText":
		h.text = p.Text
	case "insertTextAtCursor":
		h.text += p.Text
	case "getText", "getExpandedText":
		return sdkjson.Marshal(map[string]string{"text": h.text})
	case "getLines":
		return sdkjson.Marshal(map[string]any{"lines": strings.Split(h.text, "\n")})
	case "render":
		return sdkjson.Marshal(map[string]any{"lines": []string{"base:" + h.text}})
	case "addToHistory":
	default:
		return nil, fmt.Errorf("unexpected base op %q", op)
	}
	return json.RawMessage(`{}`), nil
}

func (h *baseEditorHost) snapshot() (string, [][]string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.text, slices.Clone(h.frames)
}

// delegatedEditorCases lists every non-Node SDK in every placement.
func delegatedEditorCases() []harnessCase {
	var cases []harnessCase
	for _, tc := range allHarnessCases() {
		if tc.name == "subprocess-go" || tc.name == "subprocess-rust" || tc.name == "subprocess-python" {
			cases = append(cases, tc)
		}
	}
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	return cases
}

func cleanupHarness(t *testing.T, h *harness) {
	t.Cleanup(func() {
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	})
}

func waitInstalledEditor(t *testing.T, h *harness) extension.RemoteEditor {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if editor := h.ui.installedEditor(); editor != nil {
			return editor
		}
		if time.Now().After(deadline) {
			t.Fatal("the extension's editor never reached the host")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Pi's setEditorComponent factory returns a CustomEditor subclass: its handleInput sees the host's keys and calls super, its render wraps super.render, and the host's setText reaches the override. Every SDK reaches the host's default editor through the same ui.editor.base operations.
func TestEditorComponentSubclassesTheHostEditor(t *testing.T) {
	t.Parallel()
	for _, tc := range delegatedEditorCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if !h.runner.ExecuteCommand(context.Background(), "editor-install", "") {
				t.Fatal("fixture has no editor-install command")
			}
			var editor extension.RemoteEditor
			deadline := time.Now().Add(10 * time.Second)
			for editor = h.ui.installedEditor(); editor == nil; editor = h.ui.installedEditor() {
				if time.Now().After(deadline) {
					t.Fatal("the extension's editor never reached the host")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if delegated, ok := editor.(extension.DelegatedRemoteEditor); !ok || !delegated.Delegated() {
				t.Fatal("the installed editor is not a delegated component")
			}
			host := &baseEditorHost{inputs: make(chan struct{}, 16)}
			editor.Bind(host)
			editor.Configure(extension.RemoteEditorConfig{Focused: true})

			waitInput := func() {
				t.Helper()
				select {
				case <-host.inputs:
				case <-time.After(10 * time.Second):
					t.Fatal("the component never finished the key")
				}
			}
			// The override swallows "q", rewrites "a" to "A", and calls super for the rest.
			for _, key := range []string{"q", "a", "b"} {
				editor.Input(key)
				waitInput()
			}
			text, frames := host.snapshot()
			if text != "Ab" {
				t.Fatalf("super text = %q; want %q (q swallowed, a rewritten, b inherited)", text, "Ab")
			}
			if last := frames[len(frames)-1]; !slices.Equal(last, []string{"[custom editor]", "base:Ab"}) {
				t.Fatalf("frame = %q; want the override's row over super.render", last)
			}
			// The host's setText reaches the override, which upper-cases and calls super.
			editor.SetText("hello")
			deadline = time.Now().Add(10 * time.Second)
			for {
				text, frames = host.snapshot()
				if text == "HELLO" && strings.Join(frames[len(frames)-1], "|") == "[custom editor]|base:HELLO" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("after setText: text=%q frames=%q", text, frames)
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

// Pi's editor answers as soon as the factory builds it, so a factory may call super. The host binds its editor after install, once its UI is ready; a base call made before that waits instead of failing.
func TestEditorComponentFactoryCanCallSuperBeforeTheHostBinds(t *testing.T) {
	t.Parallel()
	for _, tc := range delegatedEditorCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			cleanupHarness(t, h)
			done := make(chan bool, 1)
			go func() { done <- h.runner.ExecuteCommand(context.Background(), "editor-install-eager", "") }()
			editor := waitInstalledEditor(t, h)
			select {
			case <-done:
				t.Fatal("the factory returned before the host bound its editor: its base call did not wait")
			case <-time.After(300 * time.Millisecond):
			}
			host := &baseEditorHost{inputs: make(chan struct{}, 16)}
			editor.Bind(host)
			select {
			case ok := <-done:
				if !ok {
					t.Fatal("the command failed")
				}
			case <-time.After(20 * time.Second):
				t.Fatal("the factory never returned after the host bound its editor")
			}
			if !slices.Contains(h.ui.Recorded(), "eager:eager:info") {
				t.Fatalf("notifications = %q; want the factory's own base calls answered (setText then getText)", h.ui.Recorded())
			}
		})
	}
}

// Pi reads embedWorkingStatus from the editor the factory returned, and the host places the status in its border. A delegated component declares it after its factory returned.
func TestEditorComponentEmbedOptInReachesTheHost(t *testing.T) {
	t.Parallel()
	for _, tc := range delegatedEditorCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			cleanupHarness(t, h)
			if !h.runner.ExecuteCommand(context.Background(), "editor-install-embed", "") {
				t.Fatal("fixture has no editor-install-embed command")
			}
			editor := waitInstalledEditor(t, h)
			host := &baseEditorHost{inputs: make(chan struct{}, 16)}
			editor.Bind(host)
			deadline := time.Now().Add(20 * time.Second)
			for !editor.EmbedWorkingStatus() {
				if time.Now().After(deadline) {
					t.Fatal("the component's embedWorkingStatus never reached the host")
				}
				time.Sleep(5 * time.Millisecond)
			}
			for {
				host.mu.Lock()
				seen := host.optIns
				host.mu.Unlock()
				if seen > 0 {
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("the host's editor was never told to re-place the status")
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

// pi-tui's Editor holds JavaScript strings: setText, insertTextAtCursor and addToHistory take lone UTF-16 units through unchanged, and a surrogate pair stays a pair.
func TestEditorComponentCarriesLoneSurrogates(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, text string }{
		{"high", "raw:\xed\xa0\xbd"},
		{"low", "raw:\xed\xb8\x80"},
		{"pair", "raw:😀"},
		{"between", "raw:A\xed\xa0\xbdB"},
	}
	for _, tc := range delegatedEditorCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			cleanupHarness(t, h)
			if !h.runner.ExecuteCommand(context.Background(), "editor-install", "") {
				t.Fatal("fixture has no editor-install command")
			}
			editor := waitInstalledEditor(t, h)
			host := &baseEditorHost{inputs: make(chan struct{}, 16)}
			editor.Bind(host)
			editor.Configure(extension.RemoteEditorConfig{Focused: true})
			for _, c := range cases {
				editor.SetText(c.text)
				editor.InsertTextAtCursor(c.text)
				editor.AddToHistory(c.text)
			}
			want := len(cases)
			deadline := time.Now().Add(20 * time.Second)
			for {
				host.mu.Lock()
				done := len(host.textArgs["setText"]) >= want && len(host.textArgs["insertTextAtCursor"]) >= want && len(host.textArgs["addToHistory"]) >= want
				got := map[string][]string{}
				for op, args := range host.textArgs {
					got[op] = slices.Clone(args)
				}
				host.mu.Unlock()
				if done {
					for op, args := range got {
						for i, c := range cases {
							var text string
							if err := sdkjson.Unmarshal([]byte(args[i]), &text); err != nil || text != c.text {
								t.Errorf("%s case %s reached the host as %s; want the UTF-16 units of %q", op, c.name, args[i], c.text)
							}
						}
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("the host saw %v", got)
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
}

// Render rows and getLines carry JavaScript strings too: a row of super.render and a line of getLines keep their lone UTF-16 units from the host's editor through the component to the screen, in every SDK. The fixture's "L" key writes the joined getLines back as text.
func TestEditorComponentCarriesLoneSurrogatesInRowsAndLines(t *testing.T) {
	t.Parallel()
	for _, tc := range delegatedEditorCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			cleanupHarness(t, h)
			if !h.runner.ExecuteCommand(context.Background(), "editor-install", "") {
				t.Fatal("fixture has no editor-install command")
			}
			editor := waitInstalledEditor(t, h)
			host := &baseEditorHost{inputs: make(chan struct{}, 16)}
			editor.Bind(host)
			editor.Configure(extension.RemoteEditorConfig{Focused: true})
			const text = "raw:A\xed\xa0\xbdB\xed\xb8\x80C😀"
			editor.SetText(text)
			deadline := time.Now().Add(20 * time.Second)
			for {
				_, frames := host.snapshot()
				if len(frames) > 0 && len(frames[len(frames)-1]) == 2 && frames[len(frames)-1][1] == "base:"+text {
					break
				}
				if time.Now().After(deadline) {
					_, frames = host.snapshot()
					t.Fatalf("frames = %q; want the row of super.render to keep its lone surrogates: %q", frames, "base:"+text)
				}
				time.Sleep(5 * time.Millisecond)
			}
			editor.Input("L")
			select {
			case <-host.inputs:
			case <-time.After(20 * time.Second):
				t.Fatal("the component never finished the key")
			}
			if got, _ := host.snapshot(); got != "raw:lines="+text {
				t.Fatalf("text after getLines = %q; want %q", got, "raw:lines="+text)
			}
		})
	}
}
