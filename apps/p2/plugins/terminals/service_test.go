package terminals

import (
	"context"
	"net"
	"os"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Ports test/p2/terminal-service.test.ts: closing an archived session's terminals (`TerminalService.CloseSession`): no
// host is started when none runs, and a host running older code, which does not know `closeSession`, has them closed with
// the viewer protocol (list, then attach and force-close each). The old host here is a fake speaking that protocol.

func fakeTerminal(id, sessionID string) TerminalInfo {
	return TerminalInfo{ID: id, SessionID: sessionID, Index: 0, Cwd: "/", Cols: 80, Rows: 24}
}

func TestClosingASessionStartsNoHostAndClosesAnOutdatedHostsTerminalsAsThePanelDoes(t *testing.T) {
	directory := t.TempDir()
	service := NewTerminalService(directory)
	defer service.Close()

	// No host runs: nothing to close, and none is started.
	if err := service.CloseSession(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if conn := ConnectRunningTerminalHost(directory); conn != nil {
		conn.Close()
		t.Fatal("a host was started")
	}

	// A host running older code: it only knows the viewer requests.
	var mu sync.Mutex
	terminals := []TerminalInfo{fakeTerminal("a", "s1"), fakeTerminal("b", "s1"), fakeTerminal("c", "s2")}
	var requests []TerminalRequest
	address, err := HostAddress(directory)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(address)
	listener, err := net.Listen("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				write := func(event TerminalEvent) {
					frame, _ := EncodeFrame(event.JSON())
					_, _ = conn.Write(frame)
				}
				mu.Lock()
				running := len(terminals)
				mu.Unlock()
				write(TerminalEvent{Type: EventHello, Version: "older", Running: running, Pid: 1})
				attached := ""
				_ = ReadFrames(conn, func(frame Frame) {
					request, err := ParseRequest(frame.JSON)
					if err != nil {
						return
					}
					mu.Lock()
					defer mu.Unlock()
					requests = append(requests, request)
					switch {
					case request.Type == RequestList:
						var listed []TerminalInfo
						for _, each := range terminals {
							if each.SessionID == request.SessionID {
								listed = append(listed, each)
							}
						}
						write(TerminalEvent{Type: EventList, Terminals: listed})
					case request.Type == RequestAttach:
						attached = request.TerminalID
					case request.Type == RequestClose && request.Force != nil && *request.Force && attached != "":
						for index, each := range terminals {
							if each.ID == attached {
								terminals = append(terminals[:index], terminals[index+1:]...)
								break
							}
						}
						write(TerminalEvent{Type: EventClosed, TerminalID: attached})
						attached = ""
					default:
						write(TerminalEvent{Type: EventError, Message: "Unknown request"})
					}
				})
			}()
		}
	}()
	if err := service.CloseSession(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	force := true
	mu.Lock()
	want := []TerminalRequest{
		{Type: RequestList, SessionID: "s1"},
		{Type: RequestAttach, TerminalID: "a"},
		{Type: RequestClose, Force: &force},
		{Type: RequestAttach, TerminalID: "b"},
		{Type: RequestClose, Force: &force},
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("requests = %+v, want %+v", requests, want)
	}
	var ids []string
	for _, each := range terminals {
		ids = append(ids, each.ID)
	}
	if !reflect.DeepEqual(ids, []string{"c"}) {
		t.Fatalf("terminals = %v", ids)
	}
	// A session without terminals: one listing, nothing closed.
	requests = nil
	mu.Unlock()
	if err := service.CloseSession(context.Background(), "s3"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(requests, []TerminalRequest{{Type: RequestList, SessionID: "s3"}}) {
		t.Fatalf("requests = %+v", requests)
	}
}

// pingSocket is a viewer's WebSocket as the view sees it: it records what it is sent.
type pingSocket struct {
	mu   sync.Mutex
	sent []string
	open bool
}

func (s *pingSocket) Open() bool          { return s.open }
func (s *pingSocket) BufferedAmount() int { return 0 }
func (s *pingSocket) SendText(text string) {
	s.mu.Lock()
	s.sent = append(s.sent, text)
	s.mu.Unlock()
}
func (s *pingSocket) SendBinary([]byte)  {}
func (s *pingSocket) Terminate()         {}
func (s *pingSocket) Close(int, string) {}
func (s *pingSocket) texts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

// Ports test/p2/terminal-ping.test.ts: the server pings a viewer's terminal socket (TerminalPingInterval): an idle terminal
// sends nothing else.
func TestAViewersTerminalSocketIsPingedWhileOpenAndNoLongerOnceClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		socket := &pingSocket{open: true}
		binding := NewTerminalView(t.TempDir(), "session", "version").Bind(socket)
		time.Sleep(TerminalPingInterval - 1)
		synctest.Wait()
		if got := socket.texts(); len(got) != 0 {
			t.Fatalf("sent early: %v", got)
		}
		time.Sleep(1)
		time.Sleep(TerminalPingInterval)
		synctest.Wait()
		if got := socket.texts(); !reflect.DeepEqual(got, []string{`{"type":"ping"}`, `{"type":"ping"}`}) {
			t.Fatalf("sent = %v", got)
		}
		binding.Closed()
		time.Sleep(TerminalPingInterval * 3)
		synctest.Wait()
		if got := len(socket.texts()); got != 2 {
			t.Fatalf("pinged after close: %d", got)
		}
	})
}
