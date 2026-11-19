package extensionconformance

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// loneUnit is the lone UTF-16 surrogate U+D83D as a Go string holds it (three WTF-8 bytes).
const loneUnit = "\xed\xa0\xbd"

// rustHoldsD648 pins divergence D648 (docs/parity/divergences/D648.md): the Rust SDK's frame decode replaces the unit with U+FFFD before a
// handler is called (a Rust String and a serde_json::Value cannot hold the unit), so a value a Rust handler hands back as it received it reaches the host with
// one U+FFFD. Every other SDK holds the unit. Set it to false when the Rust SDK keeps the unit: the Rust cells then assert what every other bridge's assert.
const rustHoldsD648 = true

// loneTap records the frames the host writes to an extension and the raw results and call arguments it reads from one.
type loneTap struct {
	mu  sync.Mutex
	out [][]byte
	in  [][]byte
}

// tapLone hands the frames of h's wire to a new tap; a harness without a wire (inproc-go) leaves it empty.
func tapLone(h *harness) *loneTap {
	tap := &loneTap{}
	if h.wire != nil {
		h.wire.lone.Store(tap)
	}
	return tap
}

func (tap *loneTap) outbound(frame []byte) {
	tap.mu.Lock()
	tap.out = append(tap.out, slices.Clone(frame))
	tap.mu.Unlock()
}

func (tap *loneTap) inbound(env *subprocess.Envelope) {
	var raw []byte
	switch {
	case env.Response != nil:
		raw = env.Response.Result
	case env.Call != nil:
		raw = env.Call.Args
	}
	if raw != nil {
		tap.mu.Lock()
		tap.in = append(tap.in, slices.Clone(raw))
		tap.mu.Unlock()
	}
}

func (tap *loneTap) reset() {
	tap.mu.Lock()
	tap.out, tap.in = nil, nil
	tap.mu.Unlock()
}

// forms names, for each frame that holds label, how every `label[...]` of the frame spells the unit, in the frame's order.
func (tap *loneTap) forms(inbound bool, label string) []string {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	frames := tap.out
	if inbound {
		frames = tap.in
	}
	var forms []string
	for _, frame := range frames {
		if found := loneForms(frame, label); len(found) > 0 && !slices.Contains(forms, strings.Join(found, ", ")) {
			forms = append(forms, strings.Join(found, ", "))
		}
	}
	return forms
}

func loneForms(data []byte, label string) []string {
	var forms []string
	mark := []byte(label + "[")
	for at := 0; ; {
		i := bytes.Index(data[at:], mark)
		if i < 0 {
			return forms
		}
		at += i + len(mark)
		end := bytes.IndexByte(data[at:], ']')
		if end < 0 {
			return append(forms, "unterminated")
		}
		forms = append(forms, loneFormName(string(data[at:at+end])))
		at += end
	}
}

func loneFormName(body string) string {
	switch body {
	case `\ud83d`, `\uD83D`:
		return `escape \ud83d`
	case loneUnit:
		return "bytes ed a0 bd"
	case "\xef\xbf\xbd":
		return "U+FFFD x1"
	case strings.Repeat("\xef\xbf\xbd", 3):
		return "U+FFFD x3"
	case `\ufffd`:
		return `escape \ufffd x1`
	case `\ufffd\ufffd\ufffd`:
		return `escape \ufffd x3`
	case "":
		return "empty"
	}
	return fmt.Sprintf("other % x", body)
}

func loneKept(forms []string) bool {
	for _, frame := range forms {
		for form := range strings.SplitSeq(frame, ", ") {
			if form != `escape \ud83d` && form != "bytes ed a0 bd" {
				return false
			}
		}
	}
	return len(forms) > 0
}

// loneSeen is what the SDK handler reported it held between the brackets of its value: UTF-16 units (Node), code points (Python, Rust, Lisp) or bytes (Go).
func loneSeen(h *harness, name string) string {
	prefix := "lone_seen:" + name + ":"
	deadline := time.Now().Add(5 * time.Second)
	for {
		notes := slices.Clone(h.ui.Recorded())
		for _, note := range slices.Backward(notes) {
			if rest, ok := strings.CutPrefix(note, prefix); ok {
				units := strings.Split(strings.TrimSuffix(rest, ":info"), ".")
				open, shut := slices.Index(units, "5b"), slices.Index(units, "5d")
				if open < 0 || shut < open {
					return "no marker " + rest
				}
				return strings.Join(units[open+1:shut], ".")
			}
		}
		if time.Now().After(deadline) {
			return "not reported"
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func loneDump(value any) string {
	encoded, err := jsstring.MarshalKeepingSurrogates(value)
	if err != nil {
		return fmt.Sprintf("marshal error %v: %+v", err, value)
	}
	return string(encoded)
}

// TestConformance_LoneSurrogatePassThroughAcrossSDKs pins what Pi does with a lone UTF-16 surrogate that passes through an extension handler unchanged: a Node
// extension runs in Pi's process, so the string a handler returns is the string it was given (runner.ts emitInput, emitToolResult, emitContext,
// emitBeforeAgentStart; ExtensionAPI.sendUserMessage, sendMessage, registerProvider), and the unit is removed only where a provider request is built
// (sanitizeSurrogates). An SDK extension is another process: the value crosses the JSON wire twice. For every SDK bridge, isolated, packed and fused, the
// frame the host writes carries the unit as the escape JSON.stringify writes, the handler holds the unit, and the host holds the unit again after the handler
// handed the value back.
func TestConformance_LoneSurrogatePassThroughAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	ctx := context.Background()
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
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
			tap := tapLone(h)
			t.Cleanup(func() {
				if h.wire != nil {
					h.wire.lone.Store(nil)
				}
			})
			action := func(label string) string {
				deadline := time.Now().Add(5 * time.Second)
				for {
					for _, entry := range slices.Clone(*h.actions) {
						if strings.Contains(entry, label+"[") {
							return entry
						}
					}
					if time.Now().After(deadline) {
						return "no host action recorded"
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			cells := []struct {
				value, label, seen string
				run                func() (string, error)
			}{
				{"input text (transform)", "IN", "input", func() (string, error) {
					runConformanceCommandArgs(t, h, "lone_passthrough", "input")
					result, err := h.runner.EmitInput(ctx, "IN["+loneUnit+"]", nil, "interactive", "")
					if transform, ok := result.(extension.InputEventResultTransform); ok {
						return transform.Text, err
					}
					return fmt.Sprintf("%T %+v", result, result), err
				}},
				{"before_agent_start systemPrompt", "SP", "before_agent_start", func() (string, error) {
					runConformanceCommandArgs(t, h, "lone_passthrough", "before_agent_start")
					result, err := h.runner.EmitBeforeAgentStart(ctx, extension.UserPromptTrigger("hello", nil), extension.BuildSystemPromptOptions{CustomPrompt: "SP[" + loneUnit + "]", Cwd: "/work/project"})
					if result == nil || result.SystemPrompt == nil {
						return "no systemPrompt in the result", err
					}
					return *result.SystemPrompt, err
				}},
				{"tool_result content", "TR", "tool_result", func() (string, error) {
					runConformanceCommandArgs(t, h, "lone_passthrough", "tool_result")
					result, err := h.runner.EmitToolResult(ctx, extension.CustomToolResultEvent{Type: "tool_result", ToolCallID: "call-lone", Input: map[string]any{},
						Content: []any{map[string]any{"type": "text", "text": "TR[" + loneUnit + "]"}}, ToolName: "lone_tool"})
					if result == nil {
						return "no result", err
					}
					return loneDump(result.Content), err
				}},
				{"context messages", "CX", "context", func() (string, error) {
					runConformanceCommandArgs(t, h, "lone_passthrough", "context")
					var message extension.AgentMessage
					if err := jsstring.UnmarshalKeepingSurrogates([]byte(`{"role":"user","content":"CX[\ud83d]","timestamp":1}`), &message); err != nil {
						return "", err
					}
					t.Logf("LONEPRE|%s|context message before the emit|%v", tc.name, loneForms([]byte(loneDump(message)), "CX"))
					got, err := h.runner.EmitContext(ctx, []extension.AgentMessage{message})
					return loneDump(got), err
				}},
				{"context messages, another message edited", "CE", "context_edit", func() (string, error) {
					runConformanceCommandArgs(t, h, "lone_passthrough", "context_edit")
					var held, other extension.AgentMessage
					if err := jsstring.UnmarshalKeepingSurrogates([]byte(`{"role":"user","content":"CE[\ud83d]","timestamp":1}`), &held); err != nil {
						return "", err
					}
					if err := jsstring.UnmarshalKeepingSurrogates([]byte(`{"role":"user","content":"plain","timestamp":2}`), &other); err != nil {
						return "", err
					}
					got, err := h.runner.EmitContext(ctx, []extension.AgentMessage{held, other})
					dump := loneDump(got)
					if err == nil && !strings.Contains(dump, `"edited"`) {
						err = fmt.Errorf("the handler's edit of the other message did not reach the host: %s", strings.ToValidUTF8(dump, "?"))
					}
					return dump, err
				}},
				{"sendUserMessage", "SU", "user", func() (string, error) {
					runConformanceCommandArgs(t, h, "lone_send", "user SU["+loneUnit+"]")
					return action("SU"), nil
				}},
				{"sendMessage content", "SM", "custom", func() (string, error) {
					runConformanceCommandArgs(t, h, "lone_send", "custom SM["+loneUnit+"]")
					return action("SM"), nil
				}},
				{"registerProvider name + compat", "PC", "provider", func() (string, error) {
					runConformanceCommandArgs(t, h, "lone_send", "provider PC["+loneUnit+"]")
					runtimes := []*extension.ExtensionRuntime{h.runner.Runtime()}
					if h.host != nil {
						runtimes = append(runtimes, h.host.Runtime())
					}
					for _, runtime := range runtimes {
						if runtime == nil {
							continue
						}
						for _, pending := range runtime.PendingProviderRegistrations() {
							if pending.Name == "lone-compat" {
								return loneDump(pending.Config), nil
							}
						}
					}
					return "no pending registration", nil
				}},
			}
			for _, cell := range cells {
				tap.reset()
				decoded, err := cell.run()
				seen := loneSeen(h, cell.seen)
				out, in, host := tap.forms(false, cell.label), tap.forms(true, cell.label), loneForms([]byte(decoded), cell.label)
				verdict := "SAME"
				switch {
				case loneKept(host):
				case strings.HasPrefix(decoded, "extension.InputEventResultContinue") && loneKept(in):
					verdict = "SAME (continue: the host found the returned text equal to the text it sent)"
				case len(out) == 0 && len(in) == 0:
					verdict = "DAMAGED, wire not observed"
				case len(out) > 0 && !loneKept(out):
					verdict = "DAMAGED BY THE HOST (write of the frame to the extension)"
				case !loneKept(in):
					verdict = "DAMAGED BY THE SDK"
				default:
					verdict = "DAMAGED BY THE HOST (read of the extension's frame)"
				}
				if err != nil {
					t.Errorf("%s: %v", cell.value, err)
				}
				if strings.Contains(tc.name, "rust") && rustHoldsD648 {
					if len(host) == 0 || slices.ContainsFunc(host, func(form string) bool { return form != "U+FFFD x1" }) {
						t.Errorf("%s: the host holds %v after a Rust handler handed the value back, want one U+FFFD for the unit (divergence D648)", cell.value, host)
					}
				} else if !strings.HasPrefix(verdict, "SAME") {
					t.Errorf("%s: %s: the host wrote %v, the handler held %s, the extension sent %v, the host holds %v; want the unit", cell.value, verdict, out, seen, in, host)
				}
				if len(host) == 0 {
					host = []string{"unobserved: " + strings.ToValidUTF8(decoded, "?")}
				}
				t.Logf("LONE|%s|%s|host wrote %v|sdk held %s|sdk sent %v|host holds %v|%s|err=%v", tc.name, cell.value, out, seen, in, host, verdict, err)
			}
		})
	}
}
