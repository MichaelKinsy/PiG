package viewcarrier

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// redrawer answers an intent the way a handler that redraws on a click does: it replaces the view of the mount the
// intent came from, then answers done.
type redrawer struct {
	reg     *uiview.Registry
	once    sync.Once
	updated chan struct{}
}

func (p *redrawer) Invoke(_ context.Context, inv uiview.Invocation) (uiview.Result, error) {
	a, err := uiview.DecodeUpdate(fmt.Appendf(nil, `{"mount":{"key":%q,"epoch":%d,"rev":%d},"view":{"root":{"kind":"text","text":"redrawn"}}%s}`, inv.Mount.Key, inv.Mount.Epoch, inv.Rev, intentDecls))
	if err != nil {
		return uiview.Result{}, err
	}
	a.Answering = inv.Ticket
	if _, err := p.reg.Update("p", a); err != nil {
		return uiview.Result{}, err
	}
	p.once.Do(func() { close(p.updated) })
	return uiview.Result{Outcome: uiview.OutcomeDone, Answer: json.RawMessage(`{"sum":3}`)}, nil
}

// TestCarrierWritesTheHandlersUpdateBeforeTheIntentReply pins the order a client relies on: when it reads how its intent
// ended it has already been written the view the handler drew. The writer is held behind a stalled reader when the
// handler answers, so a reply that did not wait for the queued event would be written first.
func TestCarrierWritesTheHandlersUpdateBeforeTheIntentReply(t *testing.T) {
	f := newFixture(t)
	p := &redrawer{reg: f.h.reg, updated: make(chan struct{})}
	f.h.inv = p
	att := f.attach(4096)
	ref := f.mount("k", "x", intentDecls)
	f.settled()
	f.w.stall()
	ref = f.updateWith(ref, "busy", intentDecls)
	eventually(t, "the writer never took the event", func() bool {
		f.c.mu.Lock()
		defer f.c.mu.Unlock()
		return f.c.att.flight == 1
	})
	if r := f.do(fmt.Sprintf(`{"id":"i1","type":"ui_view_intent","attachment":%s,"plugin":"p","mount":{"key":"k","epoch":%d,"rev":%d},"intent":"add","arg":{}}`, att, ref.Epoch, ref.Rev)); r != nil {
		t.Fatalf("the intent replied before its handler ran: %v", r)
	}
	<-p.updated
	eventually(t, "the intent never settled", func() bool {
		f.c.mu.Lock()
		defer f.c.mu.Unlock()
		_, inFlight := f.c.intents[`"i1"`]
		return !inFlight
	})
	// The handler has answered and its event is queued behind the stalled writer: the reply is not written before it.
	for range 50 {
		if r := f.w.response("i1", CommandIntent); r != nil {
			t.Fatalf("the reply %v was written while the handler's ui_view event was still queued", r)
		}
		time.Sleep(time.Millisecond)
	}
	f.w.resume()
	eventually(t, "the intent never replied", func() bool { return f.w.response("i1", CommandIntent) != nil })
	r := f.w.response("i1", CommandIntent)
	if data, _ := r["data"].(map[string]any); r["success"] != true || data["outcome"] != "done" {
		t.Fatalf("intent reply = %v, want done", r)
	}
	event, reply := -1, -1
	for i, line := range f.w.lines() {
		switch {
		case strings.HasPrefix(line, `{"type":"ui_view"`) && strings.Contains(line, `"redrawn"`) && strings.Contains(line, fmt.Sprintf(`"rev":%d`, ref.Rev+1)):
			event = i
		case strings.Contains(line, `"id":"i1"`):
			reply = i
		}
	}
	if event < 0 || reply < 0 || event > reply {
		t.Fatalf("the redrawn view is line %d and the intent reply line %d of\n%s", event, reply, strings.Join(f.w.lines(), ""))
	}
}
