package subprocess

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"
)

type heldRequest struct {
	resp *Envelope
	err  error
}

// holdRequest starts a request on conn and returns when its frame reached the peer.
func holdRequest(t *testing.T, conn *Conn, peer net.Conn) <-chan heldRequest {
	t.Helper()
	got := make(chan heldRequest, 1)
	go func() {
		resp, err := conn.Request(t.Context(), &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: "event"}})
		got <- heldRequest{resp, err}
	}()
	if frame := readRetiringPeerFrame(t, peer, "the peer received no request"); frame.Type != MsgRequest {
		t.Fatalf("first frame = %+v, want the request", frame)
	}
	return got
}

func awaitHeldRequest(t *testing.T, got <-chan heldRequest) heldRequest {
	t.Helper()
	select {
	case held := <-got:
		return held
	case <-t.Context().Done():
		t.Fatal("the request did not end")
		return heldRequest{}
	}
}

// The answer a connection makes up for a request still pending when the host closes it is the connection's own, not the extension's: it carries the mark managedExt.answerError reads. A retirement ends the other way: the retiring peer closes its side once its teardown is done, which ends the read loop with a transport failure, so the pending request ends with that failure as its error, which the caller's own classification of a failed call already handles.
func TestAHostCloseAnswersPendingRequestsWithTheMark(t *testing.T) {
	for _, retire := range []bool{false, true} {
		name := "close"
		if retire {
			name = "retire"
		}
		t.Run(name, func(t *testing.T) {
			hostEnd, peerEnd := net.Pipe()
			defer func() { _ = peerEnd.Close() }()
			conn := NewConn("held", hostEnd)
			conn.Start(t.Context())
			got := holdRequest(t, conn, peerEnd)
			closed := make(chan error, 1)
			go func() {
				if retire {
					closed <- conn.Retire("session swap")
					return
				}
				closed <- conn.Close("session swap")
			}()
			if frame := readRetiringPeerFrame(t, peerEnd, "the peer received no shutdown frame"); frame.Type != MsgShutdown {
				t.Fatalf("second frame = %+v, want the shutdown frame", frame)
			}
			if retire {
				// A retiring peer ends its own side once its teardown is done.
				_ = peerEnd.Close()
			}
			held := awaitHeldRequest(t, got)
			if retire {
				if held.err == nil || held.resp != nil {
					t.Fatalf("pending request of a retired peer = %+v, %v, want the transport failure as its error", held.resp, held.err)
				}
			} else {
				if held.err != nil || held.resp == nil || held.resp.Response == nil || held.resp.Response.Error == nil {
					t.Fatalf("pending request = %+v, %v, want the connection's error answer", held.resp, held.err)
				}
				info := held.resp.Response.Error
				if info.Code != "" || info.Message != "connection closed" || !info.closedByHost {
					t.Fatalf("answer = %+v, want code \"\", message \"connection closed\" and the mark", *info)
				}
			}
			select {
			case <-closed:
			case <-t.Context().Done():
				t.Fatal("the close did not return")
			}
		})
	}
}

// A transport failure is not an answer: the request ends with the failure as its error, so no mark is made and the caller's own classification of a failed call applies.
func TestATransportFailureEndsAPendingRequestWithAnErrorNotAMarkedAnswer(t *testing.T) {
	hostEnd, peerEnd := net.Pipe()
	defer func() { _ = peerEnd.Close() }()
	conn := NewConn("held", hostEnd)
	conn.Start(t.Context())
	got := holdRequest(t, conn, peerEnd)
	_ = peerEnd.Close()
	held := awaitHeldRequest(t, got)
	if held.err == nil || held.resp != nil {
		t.Fatalf("pending request after the peer vanished = %+v, %v, want the transport failure as its error", held.resp, held.err)
	}
	_ = conn.Close("after the failure")
}

// Nothing of the connection's own answer leaves the process: the mark is no wire field, so a frame that carries the error is the same with and without it, and a peer's frame cannot set it.
func TestTheClosedByHostMarkIsNoWireField(t *testing.T) {
	marked, err := json.Marshal(&ErrorInfo{Message: "connection closed", closedByHost: true})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := json.Marshal(&ErrorInfo{Message: "connection closed"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(marked, plain) {
		t.Fatalf("the mark reaches the wire: %s against %s", marked, plain)
	}
	var back ErrorInfo
	if err := json.Unmarshal([]byte(`{"message":"connection closed","closedByHost":true}`), &back); err != nil {
		t.Fatal(err)
	}
	if back.closedByHost {
		t.Fatal("a peer's frame set the host's mark")
	}
}
