package extensionconformance

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// inprocClickCards are the click cards of the in-process Go reference, written as Pi's renderers are: the state lives in a map keyed by the message
// (entry) identity, which Pi's WeakMap keys by the message object, and a MouseRegion over the text counts the click. Every SDK's card must draw and
// count the same.
type inprocClickCards struct {
	mu     sync.Mutex
	states map[*extension.RenderIdentity]*inprocClickState
}

type inprocClickState struct {
	mu     sync.Mutex
	clicks int
	last   string
}

func (c *inprocClickCards) card(identity *extension.RenderIdentity, prefix string, subject any, expanded bool) extension.Component {
	c.mu.Lock()
	if c.states == nil {
		c.states = map[*extension.RenderIdentity]*inprocClickState{}
	}
	state := c.states[identity]
	if state == nil {
		state = &inprocClickState{last: "none"}
		c.states[identity] = state
	}
	c.mu.Unlock()
	content := &conformanceWidthComponent{line: func(int) string {
		state.mu.Lock()
		defer state.mu.Unlock()
		return fmt.Sprintf("%s:%v:clicks=%d:last=%s:expanded=%t", prefix, subject, state.clicks, state.last, expanded)
	}}
	return tui.NewMouseRegion(content, func(event tui.TuiMouseEvent) *tui.TuiMouseEventResult {
		if event.Type != tui.MouseClick {
			return nil
		}
		state.mu.Lock()
		state.clicks++
		state.last = fmt.Sprintf("%d,%d", event.X, event.Y)
		state.mu.Unlock()
		return &tui.TuiMouseEventResult{Handled: true}
	})
}

func (c *inprocClickCards) messageRenderer(message extension.CustomMessage, options extension.MessageRenderOptions, _ extension.Theme) extension.Component {
	return c.card(options.Identity, "clickcard", message.Content, options.Expanded)
}

func (c *inprocClickCards) entryRenderer(entry extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
	return c.card(options.Identity, "clickentry", entry.Data, options.Expanded)
}

func mouseEvent(kind tui.TuiMouseEventType, x, y int) tui.TuiMouseEvent {
	return tui.TuiMouseEvent{Type: kind, Button: tui.MouseButtonLeft, X: x, Y: y, ScreenX: x + 2, ScreenY: y + 6, Width: 72, Height: 1, ClickCount: 1}
}

// TestRendererClickAcrossSDKs: Pi's custom message and entry renderers keep per-message state (a WeakMap keyed by the message object) and the
// component they return takes the mouse (packages/coding-agent/src/modes/interactive/components/custom-message.ts, custom-entry.ts: the renderer is
// called again on every rebuild with the same object; packages/tui/src/components/mouse-region.ts). In every SDK a left click on the card is
// answered as handled and counted by the extension's own state with the coordinates local to the card; a press is left unhandled; another message
// has its own state; and a rebuild of the same message (a new expansion) keeps the count. The counts and the click's coordinates are values none of
// the SDKs' fallbacks produce.
func TestRendererClickAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	for _, tc := range append(allHarnessCases(), packedSDKHarnessCases()...) {
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
			message := func(identity *extension.RenderIdentity, expanded bool) interface {
				Render(int) []string
				HandleMouse(tui.TuiMouseEvent) *tui.TuiMouseDispatchResult
			} {
				renderer := h.runner.MessageRenderer("conformance-click-message")
				if renderer == nil {
					t.Fatal("conformance-click-message has no renderer")
				}
				card, ok := renderer(extension.CustomMessage{CustomType: "conformance-click-message", Content: "body", Display: true}, extension.MessageRenderOptions{Expanded: expanded, Identity: identity}, nil).(interface {
					Render(int) []string
					HandleMouse(tui.TuiMouseEvent) *tui.TuiMouseDispatchResult
				})
				if !ok {
					t.Fatal("the message renderer's component takes no mouse")
				}
				return card
			}
			entry := func(identity *extension.RenderIdentity, expanded bool) interface {
				Render(int) []string
				HandleMouse(tui.TuiMouseEvent) *tui.TuiMouseDispatchResult
			} {
				renderer := h.runner.EntryRenderer("conformance-click-entry")
				if renderer == nil {
					t.Fatal("conformance-click-entry has no renderer")
				}
				card, ok := renderer(extension.CustomEntry{CustomType: "conformance-click-entry", Data: "payload"}, extension.EntryRenderOptions{Expanded: expanded, Identity: identity}, nil).(interface {
					Render(int) []string
					HandleMouse(tui.TuiMouseEvent) *tui.TuiMouseDispatchResult
				})
				if !ok {
					t.Fatal("the entry renderer's component takes no mouse")
				}
				return card
			}
			for _, kind := range []struct {
				name   string
				prefix string
				subj   string
				make   func(*extension.RenderIdentity, bool) interface {
					Render(int) []string
					HandleMouse(tui.TuiMouseEvent) *tui.TuiMouseDispatchResult
				}
			}{{"message", "clickcard", "body", message}, {"entry", "clickentry", "payload", entry}} {
				t.Run(kind.name, func(t *testing.T) {
					want := func(clicks int, last string, expanded bool) string {
						return fmt.Sprintf("%s:%s:clicks=%d:last=%s:expanded=%t", kind.prefix, kind.subj, clicks, last, expanded)
					}
					draws := func(card interface{ Render(int) []string }, line string) func() bool {
						return func() bool {
							lines := card.Render(72)
							return len(lines) == 1 && strings.TrimRight(lines[0], " ") == line
						}
					}
					identity := extension.NewRenderIdentity()
					card := kind.make(identity, false)
					waitFor(t, draws(card, want(0, "none", false)))

					if result := card.HandleMouse(mouseEvent(tui.MousePress, 5, 0)); result != nil {
						t.Fatalf("press answered as handled: %+v", result)
					}
					if result := card.HandleMouse(mouseEvent(tui.MouseClick, 5, 0)); result == nil || !result.Handled {
						t.Fatalf("click result = %+v, want handled", result)
					}
					waitFor(t, draws(card, want(1, "5,0", false)))
					if result := card.HandleMouse(mouseEvent(tui.MouseClick, 9, 0)); result == nil || !result.Handled {
						t.Fatalf("second click result = %+v, want handled", result)
					}
					waitFor(t, draws(card, want(2, "9,0", false)))

					other := kind.make(extension.NewRenderIdentity(), false)
					waitFor(t, draws(other, want(0, "none", false)))

					// Pi's CustomMessageComponent builds a new component for the same message when its expansion changes.
					rebuilt := kind.make(identity, true)
					waitFor(t, draws(rebuilt, want(2, "9,0", true)))
					if result := rebuilt.HandleMouse(mouseEvent(tui.MouseClick, 1, 0)); result == nil || !result.Handled {
						t.Fatalf("click on the rebuilt card = %+v, want handled", result)
					}
					waitFor(t, draws(rebuilt, want(3, "1,0", true)))
				})
			}
		})
	}
}

// TestRendererClickOutsideTheRegionFallsThrough: a Node renderer's card in a padded Box answers a click only where its MouseRegion is (Pi's
// Box.handleMouse leaves the padding unhandled, and the unhandled click falls through to text selection). The host hit-tests the rectangles the runtime
// found with Pi's own dispatch, so a click on the padding is left unhandled and does not count, and a click inside is handled and counted.
func TestRendererClickOutsideTheRegionFallsThrough(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	for _, tc := range []harnessCase{{name: "subprocess-node", make: makeSubprocessNodeHarness, extName: "node-sdk-fixture"}, {name: "subprocess-node-packed", make: makeSubprocessNodePackedHarness, extName: "node-sdk-fixture"}} {
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
			renderer := h.runner.MessageRenderer("conformance-click-padded")
			if renderer == nil {
				t.Fatal("conformance-click-padded has no renderer")
			}
			card, ok := renderer(extension.CustomMessage{CustomType: "conformance-click-padded", Content: "body", Display: true}, extension.MessageRenderOptions{Identity: extension.NewRenderIdentity()}, nil).(interface {
				Render(int) []string
				HandleMouse(tui.TuiMouseEvent) *tui.TuiMouseDispatchResult
			})
			if !ok {
				t.Fatal("the renderer's component takes no mouse")
			}
			draws := func(line string) func() bool {
				return func() bool {
					lines := card.Render(72)
					return len(lines) == 1 && strings.TrimSpace(lines[0]) == line
				}
			}
			waitFor(t, draws("clickpadded:body:clicks=0:last=none:expanded=false"))
			if result := card.HandleMouse(mouseEvent(tui.MouseClick, 1, 0)); result != nil {
				t.Fatalf("click on the padding handled: %+v", result)
			}
			if result := card.HandleMouse(mouseEvent(tui.MouseClick, 70, 0)); result != nil {
				t.Fatalf("click on the right padding handled: %+v", result)
			}
			if result := card.HandleMouse(mouseEvent(tui.MouseClick, 10, 0)); result == nil || !result.Handled {
				t.Fatalf("click inside the region = %+v, want handled", result)
			}
			// The count is 1: the padding clicks reached nothing, and the region saw the click in its own coordinates (the Box shifts them by its padding).
			waitFor(t, draws("clickpadded:body:clicks=1:last=7,0:expanded=false"))
		})
	}
}
