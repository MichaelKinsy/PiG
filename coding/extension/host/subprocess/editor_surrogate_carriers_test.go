//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui"
)

var editorSurrogateCases = []struct {
	name  string
	wire  string
	units []uint16
}{
	{"empty", `""`, nil},
	{"highD83D", `"\ud83d"`, []uint16{0xd83d}},
	{"lowDE00", `"\ude00"`, []uint16{0xde00}},
	{"literal-backslash-u", `"\\ud83d\\ude00"`, []uint16{92, 117, 100, 56, 51, 100, 92, 117, 100, 101, 48, 48}},
	{"emoji", `"😀"`, []uint16{0xd83d, 0xde00}},
	{"intermediate", `"A\ud83d"`, []uint16{65, 0xd83d}},
}

func isolateEditorCarrierEnvironment(t *testing.T) {
	t.Helper()
	shortSockDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", filepath.Join(home, "pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", filepath.Join(home, "pig", "agent"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi", "agent"))
}

func loadEditorCarrierExtension(t *testing.T, ui extension.UIContext, factory string) *extension.Extension {
	t.Helper()
	source := filepath.Join(t.TempDir(), "editor-carriers.mjs")
	if err := os.WriteFile(source, []byte(factory), 0o600); err != nil {
		t.Fatal(err)
	}
	host := newTestHost(t)
	t.Cleanup(func() { host.Shutdown("editor carrier test complete") })
	host.SetUIBridge(newTestBridge(ui))
	loaded, err := host.Load(t.Context(), ExtConfig{Name: "editor-carriers", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:2558-2560
// Pi passes JS strings unchanged to/from the editor. The JSON wire must retain each UTF-16 unit, including an unmatched half.
func TestEditorSurrogateHostCalls(t *testing.T) {
	isolateEditorCarrierEnvironment(t)
	for _, tc := range editorSurrogateCases {
		t.Run(tc.name, func(t *testing.T) {
			ui := &mockUIContext{editorText: jsstring.FromUTF16(tc.units)}
			bridge := newTestBridge(ui)
			for _, method := range []string{"ui.setEditorText", "ui.pasteToEditor"} {
				if result, err := call(bridge, method, `{"text":`+tc.wire+`}`); err != nil || result == nil || result.Error != nil {
					t.Fatalf("%s: result=%+v err=%v", method, result, err)
				}
			}
			for method, calls := range map[string][]string{"set": ui.setEditorTextCalls, "paste": ui.pasteToEditorCalls} {
				if len(calls) != 1 || !slices.Equal(jsstring.ToUTF16(calls[0]), tc.units) {
					t.Errorf("%s received %q, want UTF-16 %04x", method, calls, tc.units)
				}
			}
			result, err := call(bridge, "ui.getEditorText", `{}`)
			if err != nil || result == nil || result.Error != nil {
				t.Fatalf("get: result=%+v err=%v", result, err)
			}
			// Exact JSON.stringify spelling from Pi/Node, not a round trip through the codec under test.
			if got, want := string(result.Result), `{"text":`+tc.wire+`}`; got != want {
				t.Errorf("get wire=%s, want %s", got, want)
			}
		})
	}
}

type editorCarrierUI struct {
	*testUIContext
	editor  *tui.Editor
	applied chan string
}

func (u *editorCarrierUI) setNativeText(text string) {
	u.uiStateMu.Lock()
	defer u.uiStateMu.Unlock()
	u.editor.SetText(text)
	u.editorText = u.editor.GetExpandedText()
}

func (u *editorCarrierUI) SetEditorText(text string) {
	u.setNativeText(text)
	u.applied <- text
}

func (u *editorCarrierUI) PasteToEditor(text string) {
	u.uiStateMu.Lock()
	u.editor.HandleInput("\x1b[200~" + text + "\x1b[201~")
	u.editorText = u.editor.GetExpandedText()
	u.uiStateMu.Unlock()
	u.applied <- text
}

// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:2558-2560
// upstream: packages/tui/src/components/editor.ts:1083-1130,1257-1324
func TestEditorNodeSurrogateGetSetPaste(t *testing.T) {
	isolateEditorCarrierEnvironment(t)
	ui := &editorCarrierUI{testUIContext: newTestUIContext(), editor: tui.NewEditor(), applied: make(chan string, 1)}
	reports := make(chan string, 1)
	ui.onNotify = func(message, _ string) { reports <- message }
	loaded := loadEditorCarrierExtension(t, ui, `export default function(pi) {
  pi.registerCommand("set", {handler: (args,ctx) => ctx.ui.setEditorText(JSON.parse(args))});
  pi.registerCommand("paste", {handler: (args,ctx) => ctx.ui.pasteToEditor(JSON.parse(args))});
  pi.registerCommand("report", {handler: (_args,ctx) => {
    const text = ctx.ui.getEditorText();
    ctx.ui.notify(JSON.stringify(Array.from({length:text.length}, (_,i) => text.charCodeAt(i))), "info");
  }});
}`)
	report := func(t *testing.T, want []uint16) {
		t.Helper()
		if err := loaded.Commands["report"].Handler(t.Context(), ""); err != nil {
			t.Fatal(err)
		}
		message := awaitTerminalInputSignal(t, reports, "Node editor report missing")
		var got []uint16
		if err := json.Unmarshal([]byte(message), &got); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("Node getter UTF-16=%04x, want %04x", got, want)
		}
	}
	for _, tc := range editorSurrogateCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, operation := range []string{"set", "paste"} {
				t.Run(operation, func(t *testing.T) {
					ui.setNativeText("A")
					if err := loaded.Commands[operation].Handler(t.Context(), tc.wire); err != nil {
						t.Fatal(err)
					}
					applied := awaitTerminalInputSignal(t, ui.applied, "editor host call missing")
					if got := jsstring.ToUTF16(applied); !slices.Equal(got, tc.units) {
						t.Errorf("%s host argument UTF-16=%04x, want %04x", operation, got, tc.units)
					}
					want := tc.units
					if operation == "paste" {
						want = append([]uint16{65}, want...)
					}
					if got := jsstring.ToUTF16(ui.GetEditorText()); !slices.Equal(got, want) {
						t.Errorf("native editor UTF-16=%04x, want %04x", got, want)
					}
					// A new command dispatch publishes current host text before the synchronous Node getter runs.
					report(t, want)
				})
			}
		})
	}
	t.Run("expanded-paste", func(t *testing.T) {
		// Pi collapses pastes longer than 1000 UTF-16 units; getEditorText returns expanded content, not the marker.
		want := slices.Repeat([]uint16{65, 0xd83d, 66, 0xde00}, 1024)
		ui.setNativeText("")
		if err := loaded.Commands["paste"].Handler(t.Context(), `"`+string(slices.Repeat([]byte(`A\ud83dB\ude00`), 1024))+`"`); err != nil {
			t.Fatal(err)
		}
		applied := awaitTerminalInputSignal(t, ui.applied, "large paste host call missing")
		if got := jsstring.ToUTF16(applied); !slices.Equal(got, want) {
			t.Errorf("large paste argument UTF-16=%04x, want %04x", got, want)
		}
		report(t, want)
	})
}

type editorCarrierRemoteHost struct {
	*testEditorHost
	mu       sync.Mutex
	text     string
	expanded string
	done     chan struct{}
}

func (h *editorCarrierRemoteHost) EditorChanged(text, expanded string) {
	h.mu.Lock()
	h.text, h.expanded = text, expanded
	h.mu.Unlock()
	h.testEditorHost.EditorChanged(text, expanded)
}

func (h *editorCarrierRemoteHost) EditorInputDone() { h.done <- struct{}{} }

type editorCarrierRemoteUI struct {
	*testUIContext
	installed chan extension.RemoteEditor
}

func (u *editorCarrierRemoteUI) SetEditorComponent(value extension.EditorFactory) {
	u.testUIContext.SetEditorComponent(value)
	if editor := remoteEditorOfFactory(value); editor != nil {
		u.installed <- editor
	}
}

// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:2767-2851
// upstream: packages/tui/src/components/editor.ts:1083-1194
func TestEditorNodeSurrogateRemoteText(t *testing.T) {
	isolateEditorCarrierEnvironment(t)
	ui := &editorCarrierRemoteUI{testUIContext: newTestUIContext(), installed: make(chan extension.RemoteEditor, 1)}
	loaded := loadEditorCarrierExtension(t, ui, `import {CustomEditor} from "@earendil-works/pi-coding-agent";
export default function(pi) {
  pi.registerCommand("install", {handler: (_args,ctx) => ctx.ui.setEditorComponent((tui,theme,keys) => new CustomEditor(tui,theme,keys))});
}`)
	if err := loaded.Commands["install"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	// Installation uses an ordered UI notification, so command completion is not the host UI's installation acknowledgement.
	remote := awaitTerminalInputSignal(t, ui.installed, "remote editor not installed")
	recorder := &editorCarrierRemoteHost{testEditorHost: &testEditorHost{ui: ui.testUIContext}, done: make(chan struct{}, 1)}
	remote.Bind(recorder)
	defer remote.Close()
	assertSnapshot := func(t *testing.T, want []uint16) {
		t.Helper()
		awaitTerminalInputSignal(t, recorder.done, "remote editor input barrier missing")
		recorder.mu.Lock()
		text, expanded := recorder.text, recorder.expanded
		recorder.mu.Unlock()
		for field, value := range map[string]string{"text": text, "expanded": expanded} {
			if got := jsstring.ToUTF16(value); !slices.Equal(got, want) {
				t.Errorf("remote %s UTF-16=%04x, want %04x", field, got, want)
			}
		}
	}
	for _, tc := range editorSurrogateCases {
		t.Run(tc.name, func(t *testing.T) {
			text := jsstring.FromUTF16(tc.units)
			remote.SetText(text)
			remote.Input("")
			assertSnapshot(t, tc.units)
			remote.SetText("A")
			remote.InsertTextAtCursor(text)
			remote.Input("")
			assertSnapshot(t, append([]uint16{65}, tc.units...))
			remote.SetText("A")
			remote.Input(text)
			assertSnapshot(t, append([]uint16{65}, tc.units...))
		})
	}
	t.Run("intermediate-pair", func(t *testing.T) {
		remote.SetText("A")
		remote.Input(jsstring.FromUTF16([]uint16{0xd83d}))
		assertSnapshot(t, []uint16{65, 0xd83d})
		remote.Input(jsstring.FromUTF16([]uint16{0xde00}))
		assertSnapshot(t, []uint16{65, 0xd83d, 0xde00})
	})
}

// upstream: packages/tui/src/autocomplete.ts:251-279,353-372,389-467
// Provider callbacks receive editor lines/prefix/items as JS strings; both the async suggestion and synchronous completion carriers must preserve them.
func TestEditorNodeSurrogateAutocomplete(t *testing.T) {
	isolateEditorCarrierEnvironment(t)
	ui := newTestUIContext()
	loaded := loadEditorCarrierExtension(t, ui, `export default function(pi) {
  pi.registerCommand("install", {handler: (_args,ctx) => ctx.ui.addAutocompleteProvider(current => ({
    async getSuggestions(lines,line,col,options) {
      await current.getSuggestions(lines,line,col,options);
      const text = lines[line];
      return {items:[{value:text,label:text,description:text}],prefix:text};
    },
    applyCompletion(lines,line,col,item,prefix) {
      const base = current.applyCompletion(lines,line,col,item,prefix);
      return {lines:[...base.lines,item.value,item.label,item.description,prefix],cursorLine:base.cursorLine,cursorCol:base.cursorCol};
    }
  }))});
}`)
	if err := loaded.Commands["install"].Handler(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	provider := ui.autocompleteProvider()
	if provider == nil {
		t.Fatal("autocomplete provider not installed")
	}
	for _, tc := range editorSurrogateCases {
		t.Run(tc.name, func(t *testing.T) {
			text := jsstring.FromUTF16(tc.units)
			lines := []string{"before", text, "after"}
			item := extension.AutocompleteItem{Value: text, Label: text, Description: text}
			got, err := provider.GetSuggestions(t.Context(), lines, 1, len(tc.units), false)
			if err != nil {
				t.Fatal(err)
			}
			want := &extension.AutocompleteSuggestions{Items: []extension.AutocompleteItem{item}, Prefix: text}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("suggestions=%#v, want %#v (UTF-16 %04x)", got, want, tc.units)
			}
			// Supply an independent item, not the suggestion result, so one corrupt direction cannot hide the other.
			applied, err := provider.ApplyCompletion(t.Context(), lines, 1, len(tc.units), item, text)
			if err != nil {
				t.Fatal(err)
			}
			wantApplied := extension.AutocompleteCompletion{Lines: []string{"before", text, "after", text, text, text, text}, CursorLine: 1, CursorCol: len(tc.units)}
			if !reflect.DeepEqual(applied, wantApplied) {
				t.Errorf("completion=%#v, want %#v (UTF-16 %04x)", applied, wantApplied, tc.units)
			}
		})
	}
}
