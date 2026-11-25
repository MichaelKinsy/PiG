package subprocess

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Issue #567: a live kit.Loader returned from an EntryViewRenderer, MessageViewRenderer,
// or ToolViewRenderer must advance inside a caching tui.Container without a new RPC
// response or explicit Invalidate. The host ticks the loader and calls repaint, so the
// render proxy must report IsDirty() == true while its authoritative view animates,
// while static views remain clean after consumption so the cache stays effective.
func TestRenderProxyLiveLoaderAdvancesInCachingContainer(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, conn *Conn) (proxy *renderProxyComponent, parent *tui.Container)
	}{
		{
			name: "message view renderer proxy",
			build: func(t *testing.T, conn *Conn) (*renderProxyComponent, *tui.Container) {
				proxy := newRenderProxyComponent(
					"fixture",
					"probe-msg",
					extension.CustomMessage{CustomType: "probe-msg", Content: "fallback", Display: true},
					extension.MessageRenderOptions{},
					conn,
					time.Second,
					func() {},
				)
				msg := tui.NewCustomMessageComponent(&tui.CustomMessage{CustomType: "probe-msg", Content: "fallback"}, func(*tui.CustomMessage, tui.MessageRenderOptions) tui.Component {
					return proxy
				}, nil, 1, nil, "", "")
				return proxy, tui.NewContainer(msg)
			},
		},
		{
			name: "entry view renderer proxy",
			build: func(t *testing.T, conn *Conn) (*renderProxyComponent, *tui.Container) {
				proxy := newEntryRenderProxyComponent(
					"fixture",
					"probe-entry",
					extension.CustomEntry{Type: "custom", ID: "e1", CustomType: "probe-entry"},
					extension.EntryRenderOptions{},
					conn,
					time.Second,
					func() {},
				)
				return proxy, tui.NewContainer(proxy)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hostEnd, peer := net.Pipe()
			conn := NewConn("loader-dirty-test", hostEnd)
			conn.Start(t.Context())
			t.Cleanup(func() {
				_ = peer.Close()
				_ = hostEnd.Close()
				<-conn.done
			})

			proxy, parent := tc.build(t, conn)
			_ = parent.Render(60)
			req := readFramed(t, peer)
			writeFramed(t, peer, Envelope{
				Type: MsgResponse,
				ID:   req.ID,
				Response: &ResponsePayload{
					Result: json.RawMessage(`{"view":{"root":{"kind":"loader","message":"running probe","indicator":{"frames":["<F1>","<F2>"],"intervalMs":10}}}}`),
				},
			})

			deadline := time.Now().Add(2 * time.Second)
			seen := map[string]bool{}
			for time.Now().Before(deadline) && (!seen["<F1>"] || !seen["<F2>"]) {
				rendered := strings.Join(parent.Render(60), "\n")
				for _, frame := range []string{"<F1>", "<F2>"} {
					if strings.Contains(rendered, frame) {
						seen[frame] = true
					}
				}
				time.Sleep(5 * time.Millisecond)
			}
			if !seen["<F1>"] || !seen["<F2>"] {
				t.Fatalf("caching container froze loader; saw frames %v (proxy IsDirty=%t)", seen, proxy.IsDirty())
			}

			// Static replacement stops animation: after one render consumes changed, proxy reports clean.
			proxy.SetExpanded(true)
			req2 := readFramed(t, peer)
			writeFramed(t, peer, Envelope{
				Type: MsgResponse,
				ID:   req2.ID,
				Response: &ResponsePayload{
					Result: json.RawMessage(`{"view":{"root":{"kind":"text","text":"settled"}}}`),
				},
			})
			waitForRenderProxy(t, func() bool {
				return strings.Contains(strings.Join(parent.Render(60), "\n"), "settled")
			})
			if proxy.IsDirty() {
				t.Fatalf("static view still reported IsDirty=true after render")
			}
		})
	}
}

func TestToolRenderProxyLiveLoaderAdvancesInCachingToolCard(t *testing.T) {
	hostEnd, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	conn := NewConn("tool-loader-dirty-test", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })

	proxy := &toolRenderProxy{
		session:    &toolRenderSession{conn: conn},
		tool:       "probe_tool",
		phase:      "call",
		inactivity: 10 * time.Second,
	}
	card := tui.NewToolExecutionComponent("probe_tool", "call-1", json.RawMessage(`{}`), tui.ToolExecutionOptions{}, nil, nil, "")
	card.SetDefinition(&tui.ToolDefinitionRenderers{
		Call: func(tui.ToolRenderInput) (tui.Component, bool) { return proxy, true },
	}, json.RawMessage(`{}`))
	// Simulate a settled/completed card whose renderer view still carries a loader, or a running card cached by parent.
	parent := tui.NewContainer(card)

	_ = parent.Render(60)
	req := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{
		Type: MsgResponse,
		ID:   req.ID,
		Response: &ResponsePayload{
			Result: json.RawMessage(`{"view":{"root":{"kind":"loader","message":"running tool","indicator":{"frames":["<T1>","<T2>"],"intervalMs":10}}}}`),
		},
	})

	deadline := time.Now().Add(2 * time.Second)
	seen := map[string]bool{}
	for time.Now().Before(deadline) && (!seen["<T1>"] || !seen["<T2>"]) {
		rendered := strings.Join(parent.Render(60), "\n")
		for _, frame := range []string{"<T1>", "<T2>"} {
			if strings.Contains(rendered, frame) {
				seen[frame] = true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !seen["<T1>"] || !seen["<T2>"] {
		t.Fatalf("caching container froze tool card loader; saw frames %v (proxy IsDirty=%t, card IsDirty=%t)", seen, proxy.IsDirty(), card.IsDirty())
	}
}
