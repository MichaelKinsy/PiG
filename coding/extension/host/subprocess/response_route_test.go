package subprocess

import (
	"context"
	"sync/atomic"
	"testing"
)

// A Host's connection reads the response to a request by the fast route (routeResponseFrame) unless the Host's inbound observer needs the frame: cross-process accounting needs the response to a request it expects (xrefHub.expect), and a diagnostic observer (Host.SetInboundObserver) needs every response. observeXref installs the observer; a spy in front of it shows which responses the connection decoded for it, which are the ones the fast route did not take.
func TestHostConnectionRoutesTheResponsesInboundDoesNotNeed(t *testing.T) {
	cases := []struct {
		name         string
		diagnostic   bool
		expected     bool
		frame        func(id, result string) string
		inboundReads bool
	}{
		{"SDK frame nobody needs", false, false, sdkFrame, false},
		{"SDK frame of a request cross-process accounting expects", false, true, sdkFrame, true},
		{"SDK frame with a diagnostic observer", true, false, sdkFrame, true},
		{"Python frame, which the fast route declines", false, false, pythonFrame, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
			if tc.diagnostic {
				host.SetInboundObserver(func(string, *Envelope) {})
			}
			conn, extSide := newPipeConn()
			host.observeXref(conn)
			hostInbound := conn.inbound
			var responses atomic.Int32
			conn.inbound = func(env *Envelope) {
				if env.Type == MsgResponse {
					responses.Add(1)
				}
				hostInbound(env)
			}
			peer := startResponsePeer(t, conn, extSide)
			if tc.expected {
				host.xref.expect(conn, "answered")
			}
			type outcome struct {
				resp *Envelope
				err  error
			}
			done := make(chan outcome, 1)
			go func() {
				resp, err := conn.Request(context.Background(), eventRequest("answered"))
				done <- outcome{resp, err}
			}()
			<-peer.requests
			peer.writeFrame(tc.frame("answered", `"the answer"`))
			got := <-done
			if got.err != nil || got.resp == nil || got.resp.ID != "answered" || got.resp.Response == nil || string(got.resp.Response.Result) != `"the answer"` {
				t.Fatalf("the request received %+v, %v", got.resp, got.err)
			}
			releaseResponse(got.resp)
			if read := responses.Load() == 1; read != tc.inboundReads || responses.Load() > 1 {
				t.Fatalf("inbound read %d responses, want it to read the response: %t", responses.Load(), tc.inboundReads)
			}
			if tc.expected && host.xref.expects(conn, "answered") {
				t.Fatal("cross-process accounting still expects a response it read")
			}
		})
	}
}
