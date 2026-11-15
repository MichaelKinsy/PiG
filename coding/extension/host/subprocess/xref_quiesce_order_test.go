package subprocess

import (
	"encoding/json"
	"sync"
	"testing"
)

// A listener realm that is already waiting in a synchronous call when a dispatch reaches it cannot drain microtasks, so it reports events.quiesce at once. The report precedes the response to the events.dispatch request on the wire. The Host reads one connection's frames in order, so the emit the response releases finds its emission settled: it returns without handing the emitter an emission to events.settle. Written after the response, the report raced the Host's next read and a varying share of the emits of one burst cost the emitter an events.settle round trip.
//
// The order is a property of what the realm writes, so the test reads it from the frames and needs no timing. Only the first emit of the burst finds its listener realm idle: that realm waits for events.proceed before it quiesces, and the emit is the one that settles.
func TestXrefListenerRealmReportsQuiesceBeforeItsDispatchResponse(t *testing.T) {
	skipWithoutNodeExtensions(t)
	t.Parallel()
	const emits = 50
	var mu sync.Mutex
	var seq int
	requestEmission := map[string]string{}
	completedAt := map[string]int{}
	responseAt := map[string]int{}
	quiesceAt := map[string]int{}
	settles := 0
	observe := func(h *Host) {
		h.SetOutboundObserver(func(ext string, frame []byte) {
			if ext != "xref-1" {
				return
			}
			var env Envelope
			if json.Unmarshal(frame, &env) != nil || env.Type != MsgRequest || env.Request == nil || env.Request.Method != methodEventsDispatch {
				return
			}
			var args struct {
				Emission string `json:"emission"`
			}
			_ = json.Unmarshal(env.Request.Args, &args)
			mu.Lock()
			requestEmission[env.ID] = args.Emission
			mu.Unlock()
		})
		h.SetInboundObserver(func(ext string, env *Envelope) {
			mu.Lock()
			defer mu.Unlock()
			seq++
			switch {
			case ext == "xref-0" && env.Type == MsgCall && env.Call != nil && env.Call.Method == callEventsSettle:
				settles++
			case ext != "xref-1":
			case env.Type == MsgRequestState && env.RequestState != nil && env.RequestState.State == "completed":
				completedAt[env.RequestState.RequestID] = seq
			case env.Type == MsgResponse:
				responseAt[env.ID] = seq
			case env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == notifyEventsQuiesce:
				var args struct {
					Emission string `json:"emission"`
				}
				_ = json.Unmarshal(env.Notify.Args, &args)
				quiesceAt[args.Emission] = seq
			}
		})
	}
	_, exts := xrefFixtureObserved(t, observe, []string{"isolated", "isolated"},
		`export default pi=>{`+xrefTool("burst", `const data={n:0};for(let i=0;i<50;i++)pi.events.emit("tick",data);return {content:[{type:"text",text:String(data.n)}]};`)+`};`,
		`export default pi=>{pi.events.on("tick",data=>{data.n=data.n+1;});};`)
	if got := xrefRunTool(t, exts["xref-0"], "burst"); got != "50" {
		t.Fatalf("burst result = %q, want 50 listener increments", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requestEmission) != emits {
		t.Fatalf("dispatch requests to the listener realm = %d, want %d", len(requestEmission), emits)
	}
	late := 0
	for id, emission := range requestEmission {
		quiesce, hasQuiesce := quiesceAt[emission]
		completed, hasCompleted := completedAt[id]
		response, hasResponse := responseAt[id]
		if !hasQuiesce || !hasCompleted || !hasResponse {
			t.Fatalf("dispatch %s of emission %s: quiesce read %v, completed state read %v, response read %v", id, emission, hasQuiesce, hasCompleted, hasResponse)
		}
		if completed > response {
			t.Errorf("dispatch %s: the completed state (frame %d) follows its response (frame %d)", id, completed, response)
		}
		if quiesce > completed {
			late++
		}
	}
	if late > 1 {
		t.Errorf("%d of %d emits: events.quiesce followed the completed state and response of its dispatch; only the emit that found the realm idle may (it waits for events.proceed)", late, emits)
	}
	if settles > 1 {
		t.Errorf("the emitter made %d events.settle calls for %d emits, want at most the 1 of the idle first emit", settles, emits)
	}
}
