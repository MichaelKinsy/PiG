package terminals

import (
	"context"
	"errors"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"net"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// Ports test/p2/terminal-agent-host.test.ts: the terminal host's calls for the agent's terminal tool, against a real host
// and pi-env daemon: opens and inputs done once per key, the read cursor and its ring overflow, waits (quiet, match, exit,
// timeout, cancel), plain text, the alternate screen as screen text, captures, session list pushes to the viewers, the
// agent's terminal cap, closing a session, and an outdated host refusing calls.

// hostConn is a host connection that records events and makes calls.
type hostConn struct {
	t       *testing.T
	conn    net.Conn
	mu      sync.Mutex
	events  []TerminalEvent
	output  [][]byte
	replies map[int]chan TerminalEvent
	next    int
	closed  chan struct{}
}

func dialHost(t *testing.T, directory string) *hostConn {
	t.Helper()
	conn, err := ConnectTerminalHost(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	client := &hostConn{t: t, conn: conn, replies: map[int]chan TerminalEvent{}, next: 1, closed: make(chan struct{})}
	go func() {
		defer close(client.closed)
		_ = ReadFrames(conn, func(frame Frame) {
			client.mu.Lock()
			defer client.mu.Unlock()
			if frame.Raw {
				client.output = append(client.output, frame.Bytes)
				return
			}
			event, err := ParseEvent(frame.JSON)
			if err != nil {
				return
			}
			if event.Type == EventReply {
				if reply := client.replies[event.ID]; reply != nil {
					reply <- event
				}
				return
			}
			client.events = append(client.events, event)
		})
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return client
}

func (c *hostConn) send(request TerminalRequest) {
	c.t.Helper()
	frame, err := EncodeFrame(request.JSON())
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.conn.Write(frame); err != nil {
		c.t.Fatal(err)
	}
}

func (c *hostConn) seen(match func(TerminalEvent) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.events {
		if match(event) {
			return true
		}
	}
	return false
}

// pendingCall is a call started: its id and its result.
type pendingCall struct {
	id     int
	result chan callReply
}

func (p pendingCall) wait() (HostResult, error) {
	out := <-p.result
	return out.result, out.err
}

// start starts a call: its id, and its result (an error with the host's error).
func (c *hostConn) start(call HostCall) pendingCall {
	c.t.Helper()
	c.mu.Lock()
	id := c.next
	c.next++
	reply := make(chan TerminalEvent, 1)
	c.replies[id] = reply
	c.mu.Unlock()
	result := make(chan callReply, 1)
	go func() {
		event := <-reply
		if event.Err != nil {
			result <- callReply{err: errors.New(*event.Err)}
		} else {
			result <- callReply{result: *event.Result}
		}
	}()
	c.send(TerminalRequest{Type: RequestCall, ID: id, Call: &call})
	return pendingCall{id: id, result: result}
}

func (c *hostConn) call(call HostCall) (HostResult, error) {
	c.t.Helper()
	return c.start(call).wait()
}

func (c *hostConn) mustCall(call HostCall) HostResult {
	c.t.Helper()
	result, err := c.call(call)
	if err != nil {
		c.t.Fatalf("%s: %v", call.Op, err)
	}
	return result
}

func (c *hostConn) read(call HostCall) TerminalRead {
	c.t.Helper()
	call.Op = OpRead
	result := c.mustCall(call)
	if result.Kind != KindRead {
		c.t.Fatalf("kind = %s", result.Kind)
	}
	return result.Read
}

func matchWait(pattern string, timeoutMs int) *TerminalWait {
	return &TerminalWait{Until: WaitMatch, Pattern: &pattern, TimeoutMs: timeoutMs}
}

func rejects(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil || !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Fatalf("error = %v, want one matching %s", err, pattern)
	}
}

func shutdownHost(t *testing.T, directory string) {
	last := dialHost(t, directory)
	last.send(TerminalRequest{Type: RequestShutdown})
	select {
	case <-last.closed:
	case <-time.After(30 * time.Second):
		t.Error("the host did not shut down")
	}
}

func TestTerminalHostTheAgentsCalls(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	useDaemon(t)
	directory := t.TempDir()
	cwd, err := os.MkdirTemp("", "p2-agent-terminal-cwd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cwd) })
	viewer := dialHost(t, directory)
	agent := dialHost(t, directory)
	t.Cleanup(func() { shutdownHost(t, directory) })
	open := func(sessionID, key, command, name string) TerminalInfo {
		t.Helper()
		result := agent.mustCall(HostCall{Op: OpOpen, SessionID: sessionID, Key: key, Cwd: cwd, Cols: 60, Rows: 10, Command: command, Name: name})
		if result.Kind != KindTerminal {
			t.Fatalf("kind = %s", result.Kind)
		}
		return result.Terminal
	}
	send := func(target, key string, keys []string, literal bool, sessionID string) (HostResult, error) {
		return agent.call(HostCall{Op: OpSend, SessionID: sessionID, Key: key, Target: target, Keys: keys, Literal: literal})
	}
	sendOK := func(target, key string, keys ...string) {
		t.Helper()
		if _, err := send(target, key, keys, false, "s1"); err != nil {
			t.Fatal(err)
		}
	}
	prompt := func(pattern string, timeoutMs int) *TerminalWait { return matchWait(pattern, timeoutMs) }

	t.Run("opens once per key, types keys once per key, reads from the cursor", func(t *testing.T) {
		viewer.send(TerminalRequest{Type: RequestList, SessionID: "s1"})
		terminal := open("s1", "open-1", "printf 'hi-%s\\n' there", "dev")
		if terminal.Index != 1 || !terminal.Agent || terminal.Name != "dev" {
			t.Fatalf("terminal = %+v", terminal)
		}
		if again := open("s1", "open-1", "", ""); again.ID != terminal.ID {
			t.Fatalf("the same key opened another terminal: %s, %s", again.ID, terminal.ID)
		}
		// The viewer that listed the session hears of it.
		poll(t, func() bool {
			return viewer.seen(func(event TerminalEvent) bool {
				if event.Type != EventTerminals {
					return false
				}
				for _, each := range event.Terminals {
					if each.ID == terminal.ID {
						return true
					}
				}
				return false
			})
		})
		// The program's output, then the next prompt.
		first := agent.read(HostCall{SessionID: "s1", Target: "dev", Wait: matchWait("^hi-there\\n.*[$#] $", 10_000)})
		if first.Waited != WaitMatch {
			t.Fatalf("waited %q: %q", first.Waited, first.Text)
		}
		if first.From != 0 || !regexp.MustCompile(`(?m)^hi-there$`).MatchString(first.Text) {
			t.Fatalf("first = %+v", first)
		}
		cursor := agent.mustCall(HostCall{Op: OpCursor, SessionID: "s1", Target: ":1"})
		if cursor.Kind != KindCursor || cursor.Offset <= 0 {
			t.Fatalf("cursor = %+v", cursor)
		}
		from := cursor.Offset
		// The same key twice: typed once.
		sendOK("1", "send-1", "echo o''ne", "Enter")
		sendOK("1", "send-1", "echo o''ne", "Enter")
		typed := agent.read(HostCall{SessionID: "s1", Target: "1", Wait: prompt("one\\n.*[$#] $", 10_000)})
		if typed.From != from {
			t.Fatalf("typed.from = %d, want %d", typed.From, from)
		}
		if got := len(regexp.MustCompile(`(?m)^one$`).FindAllString(typed.Text, -1)); got != 1 {
			t.Fatalf("typed %d times: %q", got, typed.Text)
		}
		// A read repeated from the same start (a replayed call) gets the same output again.
		again := agent.read(HostCall{SessionID: "s1", Target: "1", From: &from})
		if !strings.HasPrefix(again.Text, typed.Text) {
			t.Fatalf("again = %q, typed = %q", again.Text, typed.Text)
		}
		// The cursor moved past it: nothing new.
		if text := agent.read(HostCall{SessionID: "s1", Target: "1"}).Text; text != "" {
			t.Fatalf("text = %q", text)
		}
		listed := agent.mustCall(HostCall{Op: OpList, SessionID: "s1"})
		if listed.Kind != KindList || len(listed.Windows) != 1 || listed.Windows[0].Terminal.ID != terminal.ID {
			t.Fatalf("listed = %+v", listed)
		}
		_, err := send("9", "send-2", []string{"x"}, false, "s1")
		rejects(t, err, `can't find window: 9`)
		_, err = send("1", "send-3", []string{"x"}, false, "s2")
		rejects(t, err, `can't find window: 1`)
	})

	t.Run("waits: match, quiet, timeout with the foreground job, cancel, exit", func(t *testing.T) {
		sendOK("1", "wait-1", "sleep 0.3; echo la''te", "Enter")
		late := agent.read(HostCall{SessionID: "s1", Target: "1", Wait: matchWait("^late", 5000)})
		if late.Waited != WaitMatch {
			t.Fatalf("waited %q", late.Waited)
		}
		quiet := agent.read(HostCall{SessionID: "s1", Target: "1", Wait: &TerminalWait{Until: WaitQuiet, QuietMs: 200, TimeoutMs: 5000}})
		if quiet.Waited != WaitQuiet {
			t.Fatalf("waited %q", quiet.Waited)
		}
		sendOK("1", "wait-2", "sleep 30", "Enter")
		timedOut := agent.read(HostCall{SessionID: "s1", Target: "1", Wait: matchWait("never", 500)})
		if timedOut.Waited != "timeout" || !strings.Contains(timedOut.Foreground, "sleep") {
			t.Fatalf("timedOut = %q, foreground %q", timedOut.Waited, timedOut.Foreground)
		}
		waiting := agent.start(HostCall{Op: OpRead, SessionID: "s1", Target: "1", Wait: &TerminalWait{Until: WaitExit, TimeoutMs: 60_000}})
		time.Sleep(100 * time.Millisecond)
		agent.send(TerminalRequest{Type: RequestCancel, ID: waiting.id})
		_, err := waiting.wait()
		rejects(t, err, `cancelled`)
		sendOK("1", "wait-3", "C-c")
		agent.read(HostCall{SessionID: "s1", Target: "1", Wait: prompt("[$#] $", 10_000)})
		sendOK("1", "wait-4", "exit 3", "Enter")
		exited := agent.read(HostCall{SessionID: "s1", Target: "1", Wait: &TerminalWait{Until: WaitExit, TimeoutMs: 10_000}})
		if exited.Waited != WaitExit || exited.Terminal.Exited == nil || exited.Terminal.Exited.Code == nil || *exited.Terminal.Exited.Code != 3 || exited.Terminal.Exited.Error != nil {
			t.Fatalf("exited = %+v", exited)
		}
		if exited.Foreground != "" {
			t.Fatalf("foreground = %q", exited.Foreground)
		}
		_, err = send("1", "wait-5", []string{"x"}, false, "s1")
		rejects(t, err, `window 1 has exited`)
	})

	t.Run("plain text, the alternate screen, captures and ring overflow", func(t *testing.T) {
		terminal := open("s1", "open-2", "", "")
		// The exited window 1 keeps its number: this one is 2.
		if terminal.Index != 2 {
			t.Fatalf("index = %d", terminal.Index)
		}
		agent.read(HostCall{SessionID: "s1", Target: "2", Wait: prompt("[$#] $", 10_000)})
		sendOK("2", "plain-1", "printf 'a\\033[31mred\\033[0m\\r\\n10%%\\r50%%\\r100%%\\n'", "Enter")
		plain := agent.read(HostCall{SessionID: "s1", Target: "2", Wait: matchWait("100%\\n", 10_000)})
		agentPlain := plain.Text
		if !regexp.MustCompile(`(?m)^ared$`).MatchString(agentPlain) || !regexp.MustCompile(`(?m)^100%$`).MatchString(agentPlain) {
			t.Fatalf("plain text = %q", agentPlain)
		}
		if strings.Contains(agentPlain, "\x1b") || strings.Contains(agentPlain, "10%\r") {
			t.Fatalf("plain text keeps escapes: %q", agentPlain)
		}
		// A full-screen program: the read returns its screen.
		sendOK("2", "plain-2", "printf '\\033[?1049h\\033[H\\033[2JFULL SCREEN\\n'; sleep 1; printf '\\033[?1049l'", "Enter")
		full := agent.read(HostCall{SessionID: "s1", Target: "2", Wait: matchWait("^FULL SCREEN$", 5000)})
		if full.Screen == nil || strings.Split(*full.Screen, "\n")[0] != "FULL SCREEN" {
			t.Fatalf("screen = %v", full.Screen)
		}
		agent.read(HostCall{SessionID: "s1", Target: "2", Wait: prompt("[$#] $", 5000)})
		if agent.read(HostCall{SessionID: "s1", Target: "2"}).Screen != nil {
			t.Fatal("the screen is still reported after the program left the alternate screen")
		}
		sendOK("2", "plain-3", "clear; seq 1 100; printf '\\033[32mgreen\\033[0m\\n'", "Enter")
		agent.read(HostCall{SessionID: "s1", Target: "2", Wait: prompt("green\\n.*[$#] $", 10_000)})
		capture := func(scrollback int, escapes bool) string {
			result := agent.mustCall(HostCall{Op: OpCapture, SessionID: "s1", Target: "2", Scrollback: scrollback, Escapes: escapes})
			if result.Kind != KindCapture {
				return ""
			}
			return result.Text
		}
		screen := strings.Split(capture(0, false), "\n")
		// Ten rows: the last numbers, the green line and the prompt.
		if len(screen) > 10 {
			t.Fatalf("screen has %d rows", len(screen))
		}
		if got := screen[len(screen)-3 : len(screen)-1]; !reflect.DeepEqual(got, []string{"100", "green"}) {
			t.Fatalf("screen tail = %q", got)
		}
		if !regexp.MustCompile(`[$#]$`).MatchString(screen[len(screen)-1]) {
			t.Fatalf("last row = %q", screen[len(screen)-1])
		}
		scrolled := strings.Split(capture(20, false), "\n")
		if len(scrolled) != len(screen)+20 {
			t.Fatalf("scrolled has %d rows, want %d", len(scrolled), len(screen)+20)
		}
		first := 0
		for _, ch := range screen[0] {
			first = first*10 + int(ch-'0')
		}
		if want := itoa3(first - 20); scrolled[0] != want {
			t.Fatalf("scrolled[0] = %q, want %q", scrolled[0], want)
		}
		if !regexp.MustCompile("\x1b\\[0;38;2;\\d+;\\d+;\\d+mgreen\x1b\\[0m").MatchString(capture(0, true)) {
			t.Fatalf("styled capture = %q", capture(0, true))
		}
		// More output than the ring keeps: the read reports what it lost and returns the last part.
		sendOK("2", "plain-4", "head -c 1500000 /dev/zero | tr '\\0' x; echo; echo E''ND", "Enter")
		flood := agent.read(HostCall{SessionID: "s1", Target: "2", Wait: matchWait("^END$", 30_000)})
		if flood.Skipped <= 0 || flood.Truncated <= 0 || !regexp.MustCompile(`(?m)^END$`).MatchString(flood.Text) {
			t.Fatalf("flood: skipped %d truncated %d", flood.Skipped, flood.Truncated)
		}
	})

	t.Run("the agent's cap, closing once per key, closing a session", func(t *testing.T) {
		other := dialHost(t, directory)
		other.send(TerminalRequest{Type: RequestList, SessionID: "s2"})
		// The owner's own terminals do not count against the agent's cap.
		other.send(TerminalRequest{Type: RequestOpen, SessionID: "s2", Cwd: cwd, Cols: 40, Rows: 5})
		poll(t, func() bool { return other.seen(func(event TerminalEvent) bool { return event.Type == EventAttached }) })
		for index := 0; index < 8; index++ {
			open("s2", "cap-"+itoa3(index), "", "")
		}
		_, err := agent.call(HostCall{Op: OpOpen, SessionID: "s2", Key: "cap-8", Cwd: cwd, Cols: 60, Rows: 10})
		rejects(t, err, `the agent has 8 terminals running in this session already`)
		closed := agent.mustCall(HostCall{Op: OpClose, SessionID: "s2", Key: "close-1", Target: "2"})
		if closed.Kind != KindClosed {
			t.Fatalf("kind = %s", closed.Kind)
		}
		if again := agent.mustCall(HostCall{Op: OpClose, SessionID: "s2", Key: "close-1", Target: "2"}); !reflect.DeepEqual(again, closed) {
			t.Fatalf("again = %+v, want %+v", again, closed)
		}
		_, err = agent.call(HostCall{Op: OpClose, SessionID: "s2", Key: "close-2", Target: "2"})
		rejects(t, err, `can't find window: 2`)
		open("s2", "cap-8", "", "")
		if done := agent.mustCall(HostCall{Op: OpCloseSession, SessionID: "s2"}); done.Kind != KindDone {
			t.Fatalf("done = %+v", done)
		}
		list := agent.mustCall(HostCall{Op: OpList, SessionID: "s2"})
		if list.Kind != KindList || len(list.Windows) != 0 {
			t.Fatalf("list = %+v", list)
		}
		poll(t, func() bool {
			other.mu.Lock()
			defer other.mu.Unlock()
			var last *TerminalEvent
			for index := range other.events {
				if other.events[index].Type == EventTerminals {
					last = &other.events[index]
				}
			}
			return last != nil && len(last.Terminals) == 0
		})
		// Session s1 is untouched.
		if kind := agent.mustCall(HostCall{Op: OpList, SessionID: "s1"}).Kind; kind != KindList {
			t.Fatalf("kind = %s", kind)
		}
	})

	t.Run("a host running other code refuses calls", func(t *testing.T) {
		calls := NewHostCalls(directory, "another-version")
		defer calls.Close()
		_, err := calls.Call(context.Background(), HostCall{Op: OpList, SessionID: "s1"})
		if err == nil || err.Error() != OutdatedHost {
			t.Fatalf("error = %v", err)
		}
	})
}

func ptrInt64(value int64) *int64 { return &value }

func itoa3(value int) string {
	text := ""
	negative := value < 0
	if negative {
		value = -value
	}
	for {
		text = string(rune('0'+value%10)) + text
		value /= 10
		if value == 0 {
			break
		}
	}
	if negative {
		text = "-" + text
	}
	return text
}
