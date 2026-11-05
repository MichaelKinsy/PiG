package subprocess

import (
	"encoding/json"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// nextToolRender returns the next render_tool request the host sent and the phase it renders.
func (e *renderExtension) nextToolRender(t *testing.T) (Envelope, string) {
	t.Helper()
	request := e.next(t, MsgRequest)
	if request.Request.Method != RequestRenderTool {
		t.Fatalf("request %q, want %q", request.Request.Method, RequestRenderTool)
	}
	var payload RenderToolPayload
	if err := json.Unmarshal(request.Request.Args, &payload); err != nil {
		t.Fatal(err)
	}
	return request, payload.Phase
}

// fail answers a request with a renderer that threw.
func (e *renderExtension) fail(t *testing.T, request Envelope) {
	t.Helper()
	e.write(t, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Error: &ErrorInfo{Message: "renderCall failed"}}})
}

// newOrderedCard returns the call and result proxies of one card as the card makes them: one session, and both renderers run on its first pass.
func newOrderedCard(ext *renderExtension, inactivity time.Duration) (call, result *toolRenderProxy) {
	session := &toolRenderSession{card: "ordered-card", conn: ext.conn}
	proxy := func(phase string) *toolRenderProxy {
		return &toolRenderProxy{session: session, tool: "card_tool", phase: phase, inactivity: inactivity, repaint: func() {}}
	}
	call, result = proxy("call"), proxy("result")
	pass(call, result)
	return call, result
}

// pass is one run of the card's renderers: upstream's updateDisplay runs renderCall and then renderResult.
func pass(call, result *toolRenderProxy) {
	call.update(RenderToolPayload{Phase: "call", Args: json.RawMessage(`{}`)})
	result.update(RenderToolPayload{Phase: "result", Args: json.RawMessage(`{}`), Result: &RenderToolResult{Content: []RenderToolContent{}}})
}

// Upstream runs renderCall and then renderResult synchronously, so the result renderer reads the state the call renderer wrote. The host sends
// the result render only after the call render was answered, whichever of the card's loops runs first.
func TestToolRenderProxiesSendTheResultRenderAfterTheCallRenderAnswered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ext := startRenderExtension(t)
		call, result := newOrderedCard(ext, time.Second)
		// The first frame draws the call region and then the result region.
		call.Render(80)
		result.Render(80)
		first, phase := ext.nextToolRender(t)
		if phase != "call" {
			t.Fatalf("first render request is the %s render, want the call render", phase)
		}
		if n := ext.sent(); n != 0 {
			t.Fatalf("the host sent %d requests while the call render was unanswered, want none", n)
		}
		ext.answer(t, first, "call lines")
		second, phase := ext.nextToolRender(t)
		if phase != "result" {
			t.Fatalf("second render request is the %s render, want the result render", phase)
		}
		ext.answer(t, second, "result lines")
		synctest.Wait()
		if got := call.Render(80); !slices.Equal(got, []string{"call lines"}) {
			t.Fatalf("call frame = %q", got)
		}
		if got := result.Render(80); !slices.Equal(got, []string{"result lines"}) {
			t.Fatalf("result frame = %q", got)
		}
		if n := ext.sent(); n != 0 {
			t.Fatalf("the host sent %d more requests, want none", n)
		}
	})
}

// A renderer that throws does not stop upstream's updateDisplay: renderResult runs after a renderCall that threw. A call render that failed
// releases the result render.
func TestToolRenderResultIsSentAfterACallRenderThatFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ext := startRenderExtension(t)
		call, result := newOrderedCard(ext, time.Second)
		call.Render(80)
		result.Render(80)
		first, _ := ext.nextToolRender(t)
		if n := ext.sent(); n != 0 {
			t.Fatalf("the host sent %d requests while the call render was unanswered, want none", n)
		}
		ext.fail(t, first)
		second, phase := ext.nextToolRender(t)
		if phase != "result" {
			t.Fatalf("render request after the failed call render is the %s render, want the result render", phase)
		}
		ext.answer(t, second, "result lines")
		synctest.Wait()
		if got := result.Render(80); !slices.Equal(got, []string{"result lines"}) {
			t.Fatalf("result frame = %q", got)
		}
	})
}

// A call render the renderer inactivity boundary (D56) abandoned holds the result render back until the boundary, not for good: the host sends
// the result render when it gives up on the call render, as it asks again for the abandoned render once the extension answered it. Upstream's
// extension runs renderers on the UI thread, so a renderer that never returns stalls Pi's UI; the host keeps the UI and bounds the stall.
func TestToolRenderResultIsNotHeldForeverByAnAbandonedCallRender(t *testing.T) {
	const inactivity = time.Second
	synctest.Test(t, func(t *testing.T) {
		ext := startRenderExtension(t)
		call, result := newOrderedCard(ext, inactivity)
		call.Render(80)
		result.Render(80)
		first, _ := ext.nextToolRender(t)
		start := time.Now()
		time.Sleep(inactivity - time.Millisecond)
		if n := ext.sent(); n != 0 {
			t.Fatalf("the host sent %d requests before the boundary, want none", n)
		}
		time.Sleep(time.Millisecond)
		cancel := ext.next(t, MsgCancel)
		if cancel.Cancel == nil || cancel.Cancel.RequestID != first.ID {
			t.Fatalf("cancel = %+v, want the cancel of %s", cancel, first.ID)
		}
		second, phase := ext.nextToolRender(t)
		if phase != "result" {
			t.Fatalf("render request after the abandoned call render is the %s render, want the result render", phase)
		}
		if waited := time.Since(start); waited != inactivity {
			t.Fatalf("the result render was sent after %v, want the boundary %v", waited, inactivity)
		}
		ext.answer(t, second, "result lines")
		synctest.Wait()
		if got := result.Render(80); !slices.Equal(got, []string{"result lines"}) {
			t.Fatalf("result frame = %q", got)
		}
		// The extension answers the abandoned call render late: it is drawn again, after the result render that was already sent.
		ext.answer(t, first, "late")
		synctest.Wait()
		call.Render(80)
		third, phase := ext.nextToolRender(t)
		if phase != "call" {
			t.Fatalf("retry is the %s render, want the call render", phase)
		}
		ext.answer(t, third, "call lines")
		synctest.Wait()
		if got := call.Render(80); !slices.Equal(got, []string{"call lines"}) {
			t.Fatalf("call frame after the retry = %q", got)
		}
	})
}

// A card runs its renderers again while the call render of the last pass runs: the next pass is sent in the card's order too, the call render
// before the result render, and the answer to the call render the pass overtook is discarded.
func TestToolRenderOrderHoldsAcrossPasses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ext := startRenderExtension(t)
		call, result := newOrderedCard(ext, time.Second)
		call.Render(80)
		result.Render(80)
		first, phase := ext.nextToolRender(t)
		if phase != "call" {
			t.Fatalf("first render request is the %s render, want the call render", phase)
		}
		pass(call, result)
		ext.answer(t, first, "stale call lines")
		var phases []string
		for range 2 {
			request, phase := ext.nextToolRender(t)
			phases = append(phases, phase)
			ext.answer(t, request, phase+" lines")
			synctest.Wait()
		}
		if !slices.Equal(phases, []string{"call", "result"}) {
			t.Fatalf("renders after the second pass = %v, want [call result]", phases)
		}
		if got := call.Render(80); !slices.Equal(got, []string{"call lines"}) {
			t.Fatalf("call frame = %q, want the second pass's", got)
		}
		if n := ext.sent(); n != 0 {
			t.Fatalf("the host sent %d more requests, want none", n)
		}
	})
}

// A call region that is not drawn waits for nothing: a card whose call proxy has no width yet (not drawn) sends its result render at once.
func TestToolRenderResultDoesNotWaitForACallThatIsNotDrawn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ext := startRenderExtension(t)
		_, result := newOrderedCard(ext, time.Second)
		result.Render(80)
		request, phase := ext.nextToolRender(t)
		if phase != "result" {
			t.Fatalf("render request is the %s render, want the result render", phase)
		}
		ext.answer(t, request, "result lines")
		synctest.Wait()
		if got := result.Render(80); !slices.Equal(got, []string{"result lines"}) {
			t.Fatalf("result frame = %q", got)
		}
	})
}

// The order is the order the card asked in, not the order its loops reach their turn: the result's loop, first at the door, waits for the call's.
func TestRenderOrderGrantsTurnsInTheOrderTheCardAsked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var order renderOrder
		call, result := &toolRenderProxy{}, &toolRenderProxy{}
		order.want(call)
		order.want(result)
		granted := make(chan string, 2)
		go func() { order.wait(result); granted <- "result" }()
		synctest.Wait()
		select {
		case who := <-granted:
			t.Fatalf("%s took the turn ahead of the call", who)
		default:
		}
		go func() { order.wait(call); granted <- "call" }()
		synctest.Wait()
		if who := <-granted; who != "call" {
			t.Fatalf("first turn went to %s, want the call", who)
		}
		synctest.Wait()
		select {
		case who := <-granted:
			t.Fatalf("%s took the turn while the call's request was running", who)
		default:
		}
		order.leave()
		synctest.Wait()
		if who := <-granted; who != "result" {
			t.Fatalf("second turn went to %s, want the result", who)
		}
	})
}

// A proxy that wants again moves behind the requests wanted before: its newer inputs are the card's latest ask.
func TestRenderOrderMovesAProxyThatWantsAgainBehindTheOthers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var order renderOrder
		call, result := &toolRenderProxy{}, &toolRenderProxy{}
		order.want(call)
		order.want(result)
		order.want(call)
		granted := make(chan string, 2)
		go func() { order.wait(call); granted <- "call" }()
		go func() { order.wait(result); granted <- "result" }()
		synctest.Wait()
		if who := <-granted; who != "result" {
			t.Fatalf("first turn went to %s, want the result that waited first", who)
		}
		order.leave()
		synctest.Wait()
		if who := <-granted; who != "call" {
			t.Fatalf("second turn went to %s, want the call", who)
		}
	})
}

// A loop whose want a newer request already carried clears it, so the want holds nobody back; and a loop that has no want takes a turn after the
// others instead of waiting for a grant nothing will give.
func TestRenderOrderDoesNotWedgeOnAStaleOrMissingWant(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var order renderOrder
		call, result, stray := &toolRenderProxy{}, &toolRenderProxy{}, &toolRenderProxy{}
		order.want(call)
		order.want(result)
		granted := make(chan string, 3)
		go func() { order.wait(result); granted <- "result" }()
		synctest.Wait()
		if len(granted) != 0 {
			t.Fatal("the result took the turn ahead of the call")
		}
		order.clear(call)
		synctest.Wait()
		if who := <-granted; who != "result" {
			t.Fatalf("turn went to %s, want the result once the call's stale want was cleared", who)
		}
		order.leave()
		go func() { order.wait(stray); granted <- "stray" }()
		synctest.Wait()
		if who := <-granted; who != "stray" {
			t.Fatalf("turn went to %s, want the proxy without a want", who)
		}
		order.leave()
	})
}
