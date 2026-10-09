package subprocess

import (
	"encoding/json"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// A tool renderer's RenderResult.view reaches the card as it does for
// message and entry renderers (D107): an authoritative view is what the
// terminal draws and what a frontend gets as the result's structure, and a
// rejected view keeps the card's last frame.
func TestToolRenderProxyDrawsAndReportsTheRenderersView(t *testing.T) {
	hostEnd, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })
	conn := NewConn("ext", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("test done") })
	proxy := &toolRenderProxy{session: &toolRenderSession{conn: conn}, tool: "card_tool", phase: "result", inactivity: 10 * time.Second}

	answer := func(result string) {
		t.Helper()
		request := readLivenessEnvelope(t, peer)
		if request.Request == nil || request.Request.Method != RequestRenderTool {
			t.Fatalf("request = %+v, want render_tool", request)
		}
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: json.RawMessage(result)}})
	}
	want := tui.NewPaddedText("hello", 1, 0, nil).Render(40)
	proxy.Render(40)
	answer(`{"view":{"root":{"kind":"text","text":"hello","paddingX":1,"paddingY":0}}}`)
	waitForLines(t, proxy, 40, want...)
	view := proxy.FrontendView(40)
	if view == nil || view.Root.Kind != frontend.ViewKindText || view.Root.Text != "hello" || view.Root.Rows != len(want) {
		t.Fatalf("result view = %+v, want the text node", view)
	}

	type rendered struct {
		lines            []string
		failed, answered bool
	}
	now := make(chan rendered, 1)
	go func() {
		lines, failed, answered := proxy.RenderNow(40)
		now <- rendered{lines, failed, answered}
	}()
	answer(`{"view":{"root":{"kind":"no-such-kind"}}}`)
	if got := <-now; got.answered || got.failed {
		t.Fatalf("a rejected view = %+v, want the last frame kept", got)
	}
	if got := proxy.Render(40); !slices.Equal(got, want) || proxy.FrontendView(40) == nil {
		t.Fatalf("after a rejected view the card drew %q, want the last frame %q", got, want)
	}

	proxy.update(RenderToolPayload{Phase: "result"})
	answer(`{"lines":["after"]}`)
	waitForLines(t, proxy, 40, "after")
	if proxy.FrontendView(40) != nil {
		t.Fatal("a lines-only result kept the previous view")
	}
	if got := proxy.Render(40); !slices.Equal(got, []string{"after"}) {
		t.Fatalf("lines = %q", got)
	}

	// An authoritative view draws live: the host animates a loader without
	// asking the renderer again.
	proxy.update(RenderToolPayload{Phase: "result"})
	answer(`{"view":{"root":{"kind":"loader","message":"wait","indicator":{"frames":["<a>","<b>"],"intervalMs":20}}}}`)
	deadline := time.Now().Add(10 * time.Second)
	seen := map[string]bool{}
	for time.Now().Before(deadline) && (!seen["<a>"] || !seen["<b>"]) {
		for _, frame := range []string{"<a>", "<b>"} {
			if strings.Contains(strings.Join(proxy.Render(40), "\n"), frame) {
				seen[frame] = true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !seen["<a>"] || !seen["<b>"] {
		t.Fatalf("the loader drew frames %v, want both without a new request", seen)
	}
}
